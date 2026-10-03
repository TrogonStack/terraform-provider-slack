package provider

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework-jsontypes/jsontypes"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var (
	_ resource.Resource                = &appResource{}
	_ resource.ResourceWithImportState = &appResource{}
)

func newApp() resource.Resource { return &appResource{} }

type appResource struct {
	client *apiClient
}

type appResourceModel struct {
	Id                types.String         `tfsdk:"id"`
	Manifest          jsontypes.Normalized `tfsdk:"manifest"`
	ClientId          types.String         `tfsdk:"client_id"`
	ClientSecret      types.String         `tfsdk:"client_secret"`
	SigningSecret     types.String         `tfsdk:"signing_secret"`
	VerificationToken types.String         `tfsdk:"verification_token"`
	OAuthAuthorizeURL types.String         `tfsdk:"oauth_authorize_url"`
}

func (r *appResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_app"
}

func (r *appResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	createdOnly := func(description string, sensitive bool) schema.StringAttribute {
		return schema.StringAttribute{
			Computed:            true,
			Sensitive:           sensitive,
			MarkdownDescription: description,
			PlanModifiers: []planmodifier.String{
				stringplanmodifier.UseStateForUnknown(),
			},
		}
	}
	resp.Schema = schema.Schema{
		MarkdownDescription: `Manages a Slack app through its app manifest.

Needs ` + "`app_configuration_token`" + ` in the provider configuration. Every create and
update first sends the manifest to ` + "`apps.manifest.validate`" + `, so an invalid manifest
fails with Slack's own error for each field before anything changes.

Destroying this resource deletes the app with ` + "`apps.manifest.delete`" + `.

Slack returns the app credentials only once, when the app is created. They are
kept in state from then on. An imported app has no credentials in state, and
the import cannot recover them. An import also stores the exported manifest, so the
first plan after it shows an update to the configured manifest.

Creating the app does not install it. A workspace admin installs it once by
opening ` + "`oauth_authorize_url`" + `, which issues the bot token. Installing again is
needed only when the manifest changes the requested scopes.

Slack fills in defaults when it exports a manifest. A refresh keeps the
configured manifest while every value in it still matches the exported one,
so those defaults never show as drift. A missing key counts as a match when the
configured value is ` + "`false`" + `, an empty string, an empty list, or an object that holds only such values.`,
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "The app ID, e.g. `A012ABCD0A0`.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"manifest": schema.StringAttribute{
				Required:            true,
				CustomType:          jsontypes.NormalizedType{},
				MarkdownDescription: "The app manifest as JSON. Use `jsonencode` to build it. See https://docs.slack.dev/reference/app-manifest for the schema.",
			},
			"client_id":           createdOnly("The app's client ID. Known only for an app created by this resource.", false),
			"client_secret":       createdOnly("The app's client secret. Known only for an app created by this resource.", true),
			"signing_secret":      createdOnly("The secret Slack uses to sign requests to the app. Known only for an app created by this resource.", true),
			"verification_token":  createdOnly("The app's verification token. Known only for an app created by this resource.", true),
			"oauth_authorize_url": createdOnly("The URL a workspace admin opens to install the app and issue its bot token. Known only for an app created by this resource.", false),
		},
	}
}

func (r *appResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	client, ok := req.ProviderData.(*apiClient)
	if !ok {
		resp.Diagnostics.AddError("Unexpected Resource Configure Type", fmt.Sprintf("Expected *apiClient, got: %T", req.ProviderData))
		return
	}
	client.requireAppConfigurationToken(&resp.Diagnostics, "slack_app")
	r.client = client
}

func (r *appResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan appResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	manifest := plan.Manifest.ValueString()
	if err := r.client.apps.validateManifest(ctx, "", manifest); err != nil {
		resp.Diagnostics.AddAttributeError(path.Root("manifest"), "Invalid Manifest", fmt.Sprintf("Slack rejected the app manifest: %s", describeSlackError(err)))
		return
	}

	created, err := r.client.apps.createManifest(ctx, manifest)
	if err != nil {
		resp.Diagnostics.AddError("API Error", fmt.Sprintf("Unable to create app: %s", describeSlackError(err)))
		return
	}

	plan.Id = types.StringValue(created.AppID)
	plan.ClientId = types.StringValue(created.Credentials.ClientID)
	plan.ClientSecret = types.StringValue(created.Credentials.ClientSecret)
	plan.SigningSecret = types.StringValue(created.Credentials.SigningSecret)
	plan.VerificationToken = types.StringValue(created.Credentials.VerificationToken)
	plan.OAuthAuthorizeURL = types.StringValue(created.OAuthAuthorizeURL)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *appResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state appResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	exported, err := r.client.apps.exportManifest(ctx, state.Id.ValueString())
	if hasSlackError(err, errAppNotFound) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("API Error", fmt.Sprintf("Unable to read app: %s", describeSlackError(err)))
		return
	}

	keep, err := manifestStillMatches(state.Manifest, exported)
	if err != nil {
		resp.Diagnostics.AddError("API Error", fmt.Sprintf("Unable to compare the exported app manifest: %s", err))
		return
	}
	if !keep {
		state.Manifest = jsontypes.NewNormalizedValue(string(exported))
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *appResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state appResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	id := state.Id.ValueString()
	manifest := plan.Manifest.ValueString()
	if err := r.client.apps.validateManifest(ctx, id, manifest); err != nil {
		resp.Diagnostics.AddAttributeError(path.Root("manifest"), "Invalid Manifest", fmt.Sprintf("Slack rejected the app manifest: %s", describeSlackError(err)))
		return
	}
	if err := r.client.apps.updateManifest(ctx, id, manifest); err != nil {
		resp.Diagnostics.AddError("API Error", fmt.Sprintf("Unable to update app: %s", describeSlackError(err)))
		return
	}

	plan.Id = state.Id
	plan.ClientId = state.ClientId
	plan.ClientSecret = state.ClientSecret
	plan.SigningSecret = state.SigningSecret
	plan.VerificationToken = state.VerificationToken
	plan.OAuthAuthorizeURL = state.OAuthAuthorizeURL
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *appResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state appResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	err := r.client.apps.deleteManifest(ctx, state.Id.ValueString())
	if err != nil && !hasSlackError(err, errAppNotFound, errInvalidAppID) {
		resp.Diagnostics.AddError("API Error", fmt.Sprintf("Unable to delete app: %s", describeSlackError(err)))
	}
}

func (r *appResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

func manifestStillMatches(state jsontypes.Normalized, exported json.RawMessage) (bool, error) {
	if state.IsNull() || state.IsUnknown() {
		return false, nil
	}
	var want, got any
	if err := json.Unmarshal([]byte(state.ValueString()), &want); err != nil {
		return false, fmt.Errorf("decoding the manifest in state: %w", err)
	}
	if err := json.Unmarshal(exported, &got); err != nil {
		return false, fmt.Errorf("decoding the exported manifest: %w", err)
	}
	return manifestCovers(want, got), nil
}

func manifestCovers(want, got any) bool {
	switch w := want.(type) {
	case map[string]any:
		g, ok := got.(map[string]any)
		if !ok {
			return false
		}
		for key, wv := range w {
			gv, present := g[key]
			if !present {
				if !isZeroManifestValue(wv) {
					return false
				}
				continue
			}
			if !manifestCovers(wv, gv) {
				return false
			}
		}
		return true
	case []any:
		g, ok := got.([]any)
		if !ok || len(g) != len(w) {
			return false
		}
		used := make([]bool, len(g))
		for _, wv := range w {
			matched := false
			for i, gv := range g {
				if !used[i] && manifestCovers(wv, gv) {
					used[i] = true
					matched = true
					break
				}
			}
			if !matched {
				return false
			}
		}
		return true
	default:
		return want == got
	}
}

func isZeroManifestValue(v any) bool {
	switch t := v.(type) {
	case bool:
		return !t
	case string:
		return t == ""
	case []any:
		return len(t) == 0
	case map[string]any:
		for _, nested := range t {
			if !isZeroManifestValue(nested) {
				return false
			}
		}
		return true
	default:
		return false
	}
}
