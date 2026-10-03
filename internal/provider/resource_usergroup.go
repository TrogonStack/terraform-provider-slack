package provider

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework-validators/setvalidator"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/setplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/slack-go/slack"
)

var (
	_ resource.Resource                = &usergroupResource{}
	_ resource.ResourceWithImportState = &usergroupResource{}
)

func newUsergroup() resource.Resource { return &usergroupResource{} }

type usergroupResource struct {
	client *apiClient
}

type usergroupResourceModel struct {
	Id          types.String `tfsdk:"id"`
	Name        types.String `tfsdk:"name"`
	Handle      types.String `tfsdk:"handle"`
	Description types.String `tfsdk:"description"`
	Channels    types.Set    `tfsdk:"channels"`
	Users       types.Set    `tfsdk:"users"`
}

func (r *usergroupResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_usergroup"
}

func (r *usergroupResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: `Manages a Slack user group.

Destroying this resource disables the user group with ` + "`usergroups.disable`" + `.
The Slack Web API has no method to delete a user group, and a disabled user
group keeps its name and handle. When ` + "`usergroups.create`" + ` reports
` + "`name_already_exists`" + ` or ` + "`handle_already_exists`" + ` and the existing user group is
disabled, the provider enables it and takes it over instead of failing.

A user group disabled outside Terraform is removed from state and created
again on the next apply. User groups are only available on paid Slack plans.`,
		Attributes: map[string]schema.Attribute{
			"id": rsId(),
			"name": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "The user group name. Must be unique among user groups in the workspace.",
			},
			"handle": schema.StringAttribute{
				Optional:            true,
				Computed:            true,
				MarkdownDescription: "The mention handle, without the leading `@`. Must be unique among channels, users and user groups.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"description": schema.StringAttribute{
				Optional:            true,
				Computed:            true,
				MarkdownDescription: "A short description of the user group.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"channels": schema.SetAttribute{
				Optional:            true,
				Computed:            true,
				ElementType:         types.StringType,
				MarkdownDescription: "IDs of the default channels for the user group. Leave unset to leave them unmanaged.",
				PlanModifiers: []planmodifier.Set{
					setplanmodifier.UseStateForUnknown(),
				},
			},
			"users": schema.SetAttribute{
				Optional:            true,
				Computed:            true,
				ElementType:         types.StringType,
				MarkdownDescription: "IDs of the users in the user group. `usergroups.users.update` requires at least one user. Leave unset to leave membership unmanaged.",
				PlanModifiers: []planmodifier.Set{
					setplanmodifier.UseStateForUnknown(),
				},
				Validators: []validator.Set{
					setvalidator.SizeAtLeast(1),
				},
			},
		},
	}
}

func (r *usergroupResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	client, ok := req.ProviderData.(*apiClient)
	if !ok {
		resp.Diagnostics.AddError("Unexpected Resource Configure Type", fmt.Sprintf("Expected *apiClient, got: %T", req.ProviderData))
		return
	}
	client.requireBotToken(&resp.Diagnostics, "slack_usergroup")
	r.client = client
}

func (r *usergroupResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan usergroupResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	channels, d := setStrings(ctx, plan.Channels)
	resp.Diagnostics.Append(d...)
	users, d := setStrings(ctx, plan.Users)
	resp.Diagnostics.Append(d...)
	if resp.Diagnostics.HasError() {
		return
	}

	created, err := r.client.slack.CreateUserGroupContext(ctx, slack.UserGroup{
		Name:        plan.Name.ValueString(),
		Handle:      plan.Handle.ValueString(),
		Description: plan.Description.ValueString(),
		Prefs:       slack.UserGroupPrefs{Channels: channels},
	})
	if hasSlackError(err, errNameAlreadyExists, errHandleAlreadyExists) {
		created, err = r.reenableDisabled(ctx, plan, channels)
	}
	if err != nil {
		resp.Diagnostics.AddError("API Error", fmt.Sprintf("Unable to create user group: %s", err))
		return
	}
	plan.Id = types.StringValue(created.ID)

	if users != nil {
		if _, err := r.client.slack.UpdateUserGroupMembersContext(ctx, created.ID, strings.Join(users, ",")); err != nil {
			resp.Diagnostics.AddError("API Error", fmt.Sprintf("Unable to set user group members: %s", err))
			r.keepPartialCreate(ctx, &plan, resp)
			return
		}
	}

	group, err := r.findUsergroup(ctx, created.ID)
	if err != nil {
		resp.Diagnostics.AddError("API Error", fmt.Sprintf("Unable to read created user group: %s", err))
		return
	}
	if group == nil {
		resp.Diagnostics.AddError("API Error", "User group was created but could not be found immediately afterward")
		return
	}

	resp.Diagnostics.Append(applyUsergroup(ctx, &plan, group)...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *usergroupResource) keepPartialCreate(ctx context.Context, plan *usergroupResourceModel, resp *resource.CreateResponse) {
	group, err := r.findUsergroup(ctx, plan.Id.ValueString())
	if err != nil || group == nil {
		return
	}
	resp.Diagnostics.Append(applyUsergroup(ctx, plan, group)...)
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *usergroupResource) reenableDisabled(ctx context.Context, plan usergroupResourceModel, channels []string) (slack.UserGroup, error) {
	groups, err := r.client.slack.GetUserGroupsContext(ctx, slack.GetUserGroupsOptionIncludeDisabled(true))
	if err != nil {
		return slack.UserGroup{}, err
	}
	var disabled *slack.UserGroup
	for i := range groups {
		g := &groups[i]
		if g.DateDelete == 0 {
			continue
		}
		if g.Name == plan.Name.ValueString() || (isConfigured(plan.Handle) && g.Handle == plan.Handle.ValueString()) {
			disabled = g
			break
		}
	}
	if disabled == nil {
		return slack.UserGroup{}, fmt.Errorf("a user group with this name or handle already exists and is enabled; import it instead")
	}

	if _, err := r.client.slack.EnableUserGroupContext(ctx, disabled.ID); err != nil {
		return slack.UserGroup{}, err
	}
	return r.client.slack.UpdateUserGroupContext(ctx, disabled.ID, usergroupUpdateOptions(plan, channels)...)
}

func (r *usergroupResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state usergroupResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	group, err := r.findUsergroup(ctx, state.Id.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("API Error", fmt.Sprintf("Unable to read user group: %s", err))
		return
	}
	if group == nil || group.DateDelete != 0 {
		resp.State.RemoveResource(ctx)
		return
	}

	resp.Diagnostics.Append(applyUsergroup(ctx, &state, group)...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *usergroupResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state usergroupResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	channels, d := setStrings(ctx, plan.Channels)
	resp.Diagnostics.Append(d...)
	users, d := setStrings(ctx, plan.Users)
	resp.Diagnostics.Append(d...)
	if resp.Diagnostics.HasError() {
		return
	}

	id := state.Id.ValueString()

	if _, err := r.client.slack.UpdateUserGroupContext(ctx, id, usergroupUpdateOptions(plan, channels)...); err != nil {
		resp.Diagnostics.AddError("API Error", fmt.Sprintf("Unable to update user group: %s", err))
		return
	}
	if users != nil && !plan.Users.Equal(state.Users) {
		if _, err := r.client.slack.UpdateUserGroupMembersContext(ctx, id, strings.Join(users, ",")); err != nil {
			resp.Diagnostics.AddError("API Error", fmt.Sprintf("Unable to set user group members: %s", err))
			return
		}
	}

	group, err := r.findUsergroup(ctx, id)
	if err != nil {
		resp.Diagnostics.AddError("API Error", fmt.Sprintf("Unable to read updated user group: %s", err))
		return
	}
	if group == nil {
		resp.Diagnostics.AddError("API Error", "User group was updated but could not be found afterward")
		return
	}

	resp.Diagnostics.Append(applyUsergroup(ctx, &plan, group)...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *usergroupResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state usergroupResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	group, err := r.findUsergroup(ctx, state.Id.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("API Error", fmt.Sprintf("Unable to read user group: %s", err))
		return
	}
	if group == nil || group.DateDelete != 0 {
		return
	}

	if _, err := r.client.slack.DisableUserGroupContext(ctx, group.ID); err != nil {
		resp.Diagnostics.AddError("API Error", fmt.Sprintf("Unable to disable user group: %s", err))
		return
	}
}

func (r *usergroupResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

// findUsergroup lists instead of looking up by ID because the Slack Web API has
// no method that reads a single user group.
func (r *usergroupResource) findUsergroup(ctx context.Context, id string) (*slack.UserGroup, error) {
	groups, err := r.client.slack.GetUserGroupsContext(ctx,
		slack.GetUserGroupsOptionIncludeDisabled(true),
		slack.GetUserGroupsOptionIncludeUsers(true),
	)
	if err != nil {
		return nil, err
	}
	for i := range groups {
		if groups[i].ID == id {
			return &groups[i], nil
		}
	}
	return nil, nil
}

func usergroupUpdateOptions(plan usergroupResourceModel, channels []string) []slack.UpdateUserGroupsOption {
	opts := []slack.UpdateUserGroupsOption{
		slack.UpdateUserGroupsOptionName(plan.Name.ValueString()),
	}
	if isConfigured(plan.Handle) {
		opts = append(opts, slack.UpdateUserGroupsOptionHandle(plan.Handle.ValueString()))
	}
	if isConfigured(plan.Description) {
		description := plan.Description.ValueString()
		opts = append(opts, slack.UpdateUserGroupsOptionDescription(&description))
	}
	if channels != nil {
		opts = append(opts, slack.UpdateUserGroupsOptionChannels(channels))
	}
	return opts
}

func applyUsergroup(ctx context.Context, model *usergroupResourceModel, group *slack.UserGroup) diag.Diagnostics {
	var diags diag.Diagnostics

	model.Id = types.StringValue(group.ID)
	model.Name = types.StringValue(group.Name)
	model.Handle = types.StringValue(group.Handle)
	model.Description = types.StringValue(group.Description)

	channels := append(append([]string{}, group.Prefs.Channels...), group.Prefs.Groups...)
	sort.Strings(channels)
	channelSet, d := types.SetValueFrom(ctx, types.StringType, channels)
	diags.Append(d...)
	model.Channels = channelSet

	users := append([]string{}, group.Users...)
	sort.Strings(users)
	userSet, d := types.SetValueFrom(ctx, types.StringType, users)
	diags.Append(d...)
	model.Users = userSet

	return diags
}
