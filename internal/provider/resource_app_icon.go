package provider

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"os"
	"path/filepath"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

const (
	minAppIconPixels = 512
	maxAppIconPixels = 2000
)

var (
	_ resource.Resource                   = &appIconResource{}
	_ resource.ResourceWithConfigure      = &appIconResource{}
	_ resource.ResourceWithValidateConfig = &appIconResource{}
)

func newAppIcon() resource.Resource { return &appIconResource{} }

type appIconResource struct {
	client *apiClient
}

type appIconResourceModel struct {
	Id     types.String `tfsdk:"id"`
	AppId  types.String `tfsdk:"app_id"`
	File   types.String `tfsdk:"file"`
	Url    types.String `tfsdk:"url"`
	Sha256 types.String `tfsdk:"sha256"`
}

func (r *appIconResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_app_icon"
}

func (r *appIconResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: `Sets the icon of a Slack app with ` + "`apps.icon.set`" + `.

Needs ` + "`app_configuration_token`" + ` in the provider configuration. Slack accepts a
square image between 512 and 2000 pixels on each side. Set exactly one of ` + "`file`" + ` or ` + "`url`" + `.

Slack has no method that reads an app icon, so this resource cannot detect an
icon changed outside Terraform. Refresh keeps the state as it is. To restore the
icon, change ` + "`sha256`" + ` or ` + "`url`" + `, or replace the resource.

Slack has no method that removes an app icon. Destroying this resource only removes
it from state, and the app keeps its current icon.

` + "`apps.icon.set`" + ` is a Tier 1 method, so Slack may rate limit repeated changes.`,
		Attributes: map[string]schema.Attribute{
			"id": rsId(),
			"app_id": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "The ID of the app, e.g. `A012ABCD0A0`. Changing it replaces the resource.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"file": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "The path of the image to upload. Slack takes a PNG, JPEG or GIF image between 512 and 2000 pixels on each side. Needs `sha256`. Conflicts with `url`.",
				Validators: []validator.String{
					stringvalidator.ExactlyOneOf(path.MatchRoot("url")),
					stringvalidator.AlsoRequires(path.MatchRoot("sha256")),
				},
			},
			"url": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "The URL of the image for Slack to download. Conflicts with `file`. Changing it uploads the icon again.",
			},
			"sha256": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "The hash of the image `file`, from `filesha256(path)`. A new hash uploads the icon again. Needs `file`.",
				Validators: []validator.String{
					stringvalidator.ConflictsWith(path.MatchRoot("url")),
				},
			},
		},
	}
}

func (r *appIconResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	client, ok := req.ProviderData.(*apiClient)
	if !ok {
		resp.Diagnostics.AddError("Unexpected Resource Configure Type", fmt.Sprintf("Expected *apiClient, got: %T", req.ProviderData))
		return
	}
	client.requireAppConfigurationToken(&resp.Diagnostics, "slack_app_icon")
	r.client = client
}

func (r *appIconResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var config appIconResourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() || !isConfigured(config.File) {
		return
	}
	content, err := os.ReadFile(config.File.ValueString())
	if err != nil {
		return
	}
	if err := checkAppIconDimensions(content); err != nil {
		resp.Diagnostics.AddAttributeError(path.Root("file"), "Invalid App Icon", err.Error())
	}
}

func checkAppIconDimensions(content []byte) error {
	size, _, err := image.DecodeConfig(bytes.NewReader(content))
	if err != nil {
		return fmt.Errorf("the file is not a PNG, JPEG or GIF image: %w", err)
	}
	if size.Width < minAppIconPixels || size.Height < minAppIconPixels || size.Width > maxAppIconPixels || size.Height > maxAppIconPixels {
		return fmt.Errorf("the image is %dx%d pixels, but Slack needs %d to %d pixels on each side", size.Width, size.Height, minAppIconPixels, maxAppIconPixels)
	}
	if size.Width != size.Height {
		return fmt.Errorf("the image is %dx%d pixels, but Slack needs a square image", size.Width, size.Height)
	}
	return nil
}

func (r *appIconResource) apply(ctx context.Context, plan *appIconResourceModel) error {
	appID := plan.AppId.ValueString()
	if isConfigured(plan.Url) {
		return r.client.apps.setIconFromURL(ctx, appID, plan.Url.ValueString())
	}
	content, err := os.ReadFile(plan.File.ValueString())
	if err != nil {
		return fmt.Errorf("reading the icon file: %w", err)
	}
	sum := sha256.Sum256(content)
	if actual := hex.EncodeToString(sum[:]); !strings.EqualFold(actual, plan.Sha256.ValueString()) {
		return fmt.Errorf("the file has sha256 %s, but the plan has %s; the file changed after the plan, so plan again", actual, plan.Sha256.ValueString())
	}
	if err := checkAppIconDimensions(content); err != nil {
		return err
	}
	return r.client.apps.setIconFromFile(ctx, appID, filepath.Base(plan.File.ValueString()), content)
}

func (r *appIconResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan appIconResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.apply(ctx, &plan); err != nil {
		resp.Diagnostics.AddError("API Error", fmt.Sprintf("Unable to set app icon: %s", describeSlackError(err)))
		return
	}
	plan.Id = plan.AppId
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *appIconResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state appIconResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *appIconResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan appIconResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.apply(ctx, &plan); err != nil {
		resp.Diagnostics.AddError("API Error", fmt.Sprintf("Unable to set app icon: %s", describeSlackError(err)))
		return
	}
	plan.Id = plan.AppId
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *appIconResource) Delete(_ context.Context, _ resource.DeleteRequest, _ *resource.DeleteResponse) {
}
