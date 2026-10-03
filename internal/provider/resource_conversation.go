package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/boolplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/slack-go/slack"
)

var (
	_ resource.Resource                = &conversationResource{}
	_ resource.ResourceWithImportState = &conversationResource{}
)

func newConversation() resource.Resource { return &conversationResource{} }

type conversationResource struct {
	client *apiClient
}

type conversationResourceModel struct {
	Id         types.String `tfsdk:"id"`
	Name       types.String `tfsdk:"name"`
	IsPrivate  types.Bool   `tfsdk:"is_private"`
	Topic      types.String `tfsdk:"topic"`
	Purpose    types.String `tfsdk:"purpose"`
	IsArchived types.Bool   `tfsdk:"is_archived"`
	Created    types.String `tfsdk:"created"`
}

func (r *conversationResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_conversation"
}

func (r *conversationResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: `Manages a Slack channel-based conversation, public or private.

Destroying this resource archives the conversation with ` + "`conversations.archive`" + `.
The Slack Web API cannot delete a conversation outside the Enterprise Grid
admin methods, so the archived conversation and its name stay in the
workspace. Creating a conversation with the name of an archived one fails
with ` + "`name_taken`" + ` until the archived conversation is renamed or unarchived and
imported.

Changing ` + "`is_private`" + ` replaces the resource. The archived original keeps its name,
so change ` + "`name`" + ` in the same apply or the replacement fails with ` + "`name_taken`" + `.
Changing ` + "`name`" + `, ` + "`topic`" + `,
` + "`purpose`" + ` or ` + "`is_archived`" + ` updates the conversation in place. Setting the topic
or purpose of an imported conversation requires the bot to be a member of it.`,
		Attributes: map[string]schema.Attribute{
			"id": rsId(),
			"name": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "The conversation name. Slack accepts lowercase letters, numbers, hyphens and underscores, up to 80 characters.",
			},
			"is_private": schema.BoolAttribute{
				Optional:            true,
				Computed:            true,
				Default:             booldefault.StaticBool(false),
				MarkdownDescription: "Whether the conversation is a private channel. Defaults to `false`. Changing it replaces the resource.",
				PlanModifiers: []planmodifier.Bool{
					boolplanmodifier.RequiresReplace(),
				},
			},
			"topic": schema.StringAttribute{
				Optional:            true,
				Computed:            true,
				MarkdownDescription: "The conversation topic. Leave unset to leave the topic unmanaged.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"purpose": schema.StringAttribute{
				Optional:            true,
				Computed:            true,
				MarkdownDescription: "The conversation purpose, shown as its description. Leave unset to leave the purpose unmanaged.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"is_archived": schema.BoolAttribute{
				Optional:            true,
				Computed:            true,
				Default:             booldefault.StaticBool(false),
				MarkdownDescription: "Whether the conversation is archived. Defaults to `false`, so a conversation archived outside Terraform is unarchived on the next apply.",
			},
			"created": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "RFC3339 timestamp of when the conversation was created.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
		},
	}
}

func (r *conversationResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	client, ok := req.ProviderData.(*apiClient)
	if !ok {
		resp.Diagnostics.AddError("Unexpected Resource Configure Type", fmt.Sprintf("Expected *apiClient, got: %T", req.ProviderData))
		return
	}
	client.requireBotToken(&resp.Diagnostics, "slack_conversation")
	r.client = client
}

func (r *conversationResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan conversationResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	created, err := r.client.slack.CreateConversationContext(ctx, slack.CreateConversationParams{
		ChannelName: plan.Name.ValueString(),
		IsPrivate:   plan.IsPrivate.ValueBool(),
	})
	if err != nil {
		resp.Diagnostics.AddError("API Error", fmt.Sprintf("Unable to create conversation: %s", err))
		return
	}
	plan.Id = types.StringValue(created.ID)

	if isConfigured(plan.Topic) && plan.Topic.ValueString() != created.Topic.Value {
		if _, err := r.client.slack.SetTopicOfConversationContext(ctx, created.ID, plan.Topic.ValueString()); err != nil {
			resp.Diagnostics.AddError("API Error", fmt.Sprintf("Unable to set conversation topic: %s", err))
			r.keepPartialCreate(ctx, &plan, resp)
			return
		}
	}
	if isConfigured(plan.Purpose) && plan.Purpose.ValueString() != created.Purpose.Value {
		if _, err := r.client.slack.SetPurposeOfConversationContext(ctx, created.ID, plan.Purpose.ValueString()); err != nil {
			resp.Diagnostics.AddError("API Error", fmt.Sprintf("Unable to set conversation purpose: %s", err))
			r.keepPartialCreate(ctx, &plan, resp)
			return
		}
	}
	if plan.IsArchived.ValueBool() {
		if err := r.archive(ctx, created.ID); err != nil {
			resp.Diagnostics.AddError("API Error", fmt.Sprintf("Unable to archive conversation: %s", err))
			r.keepPartialCreate(ctx, &plan, resp)
			return
		}
	}

	info, err := r.findConversation(ctx, created.ID)
	if err != nil {
		resp.Diagnostics.AddError("API Error", fmt.Sprintf("Unable to read created conversation: %s", err))
		return
	}
	if info == nil {
		resp.Diagnostics.AddError("API Error", "Conversation was created but could not be found immediately afterward")
		return
	}

	applyConversation(&plan, info)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *conversationResource) keepPartialCreate(ctx context.Context, plan *conversationResourceModel, resp *resource.CreateResponse) {
	info, err := r.findConversation(ctx, plan.Id.ValueString())
	if err != nil || info == nil {
		return
	}
	applyConversation(plan, info)
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *conversationResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state conversationResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	info, err := r.findConversation(ctx, state.Id.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("API Error", fmt.Sprintf("Unable to read conversation: %s", err))
		return
	}
	if info == nil {
		resp.State.RemoveResource(ctx)
		return
	}

	applyConversation(&state, info)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *conversationResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state conversationResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	id := state.Id.ValueString()

	if state.IsArchived.ValueBool() && !plan.IsArchived.ValueBool() {
		if err := r.client.slack.UnArchiveConversationContext(ctx, id); err != nil && !hasSlackError(err, errNotArchived) {
			resp.Diagnostics.AddError("API Error", fmt.Sprintf("Unable to unarchive conversation: %s", err))
			return
		}
	}
	if plan.Name.ValueString() != state.Name.ValueString() {
		if _, err := r.client.slack.RenameConversationContext(ctx, id, plan.Name.ValueString()); err != nil {
			resp.Diagnostics.AddError("API Error", fmt.Sprintf("Unable to rename conversation: %s", err))
			return
		}
	}
	if isConfigured(plan.Topic) && plan.Topic.ValueString() != state.Topic.ValueString() {
		if _, err := r.client.slack.SetTopicOfConversationContext(ctx, id, plan.Topic.ValueString()); err != nil {
			resp.Diagnostics.AddError("API Error", fmt.Sprintf("Unable to set conversation topic: %s", err))
			return
		}
	}
	if isConfigured(plan.Purpose) && plan.Purpose.ValueString() != state.Purpose.ValueString() {
		if _, err := r.client.slack.SetPurposeOfConversationContext(ctx, id, plan.Purpose.ValueString()); err != nil {
			resp.Diagnostics.AddError("API Error", fmt.Sprintf("Unable to set conversation purpose: %s", err))
			return
		}
	}
	if plan.IsArchived.ValueBool() && !state.IsArchived.ValueBool() {
		if err := r.archive(ctx, id); err != nil {
			resp.Diagnostics.AddError("API Error", fmt.Sprintf("Unable to archive conversation: %s", err))
			return
		}
	}

	info, err := r.findConversation(ctx, id)
	if err != nil {
		resp.Diagnostics.AddError("API Error", fmt.Sprintf("Unable to read updated conversation: %s", err))
		return
	}
	if info == nil {
		resp.Diagnostics.AddError("API Error", "Conversation was updated but could not be found afterward")
		return
	}

	applyConversation(&plan, info)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *conversationResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state conversationResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if err := r.archive(ctx, state.Id.ValueString()); err != nil {
		if isNotFound(err) {
			return
		}
		resp.Diagnostics.AddError("API Error", fmt.Sprintf("Unable to archive conversation: %s", err))
		return
	}
}

func (r *conversationResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

func (r *conversationResource) archive(ctx context.Context, id string) error {
	err := r.client.slack.ArchiveConversationContext(ctx, id)
	if hasSlackError(err, errAlreadyArchived) {
		return nil
	}
	return err
}

func (r *conversationResource) findConversation(ctx context.Context, id string) (*slack.Channel, error) {
	info, err := r.client.slack.GetConversationInfoContext(ctx, &slack.GetConversationInfoInput{ChannelID: id})
	if err != nil {
		if isNotFound(err) {
			return nil, nil
		}
		return nil, err
	}
	return info, nil
}

func applyConversation(model *conversationResourceModel, info *slack.Channel) {
	model.Id = types.StringValue(info.ID)
	model.Name = types.StringValue(info.Name)
	model.IsPrivate = types.BoolValue(info.IsPrivate)
	model.Topic = types.StringValue(info.Topic.Value)
	model.Purpose = types.StringValue(info.Purpose.Value)
	model.IsArchived = types.BoolValue(info.IsArchived)
	model.Created = timestampValue(info.Created)
}
