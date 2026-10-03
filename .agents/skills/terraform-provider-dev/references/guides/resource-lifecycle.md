# Resource Lifecycle

## Interface

A resource must implement `resource.Resource`:

```go
type Resource interface {
    Metadata(context.Context, MetadataRequest, *MetadataResponse)
    Schema(context.Context, SchemaRequest, *SchemaResponse)
    Create(context.Context, CreateRequest, *CreateResponse)
    Read(context.Context, ReadRequest, *ReadResponse)
    Update(context.Context, UpdateRequest, *UpdateResponse)
    Delete(context.Context, DeleteRequest, *DeleteResponse)
}
```

Optional interfaces:

- `resource.ResourceWithConfigure`: receive provider client
- `resource.ResourceWithImportState`: support `terraform import`
- `resource.ResourceWithUpgradeState`: handle schema migrations
- `resource.ResourceWithModifyPlan`: resource-level plan modification
- `resource.ResourceWithValidateConfig`: resource-level validation

Both resources in this provider (`conversationResource`, `usergroupResource`) implement `resource.Resource` and `resource.ResourceWithImportState`. Neither currently needs the others.

## Registration

Add a constructor function to the provider's `Resources()` method:

```go
func newFoo() resource.Resource { return &fooResource{} }

// In provider.go:
func (p *slackProvider) Resources(ctx context.Context) []func() resource.Resource {
    return []func() resource.Resource{
        newConversation,
        newUsergroup,
        newFoo,
    }
}
```

## Metadata

Sets the resource type name as it appears in Terraform configurations:

```go
func (r *conversationResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
    resp.TypeName = req.ProviderTypeName + "_conversation"
}
```

This produces `slack_conversation` as the resource type (`req.ProviderTypeName` is `"slack"`, set in the provider's own `Metadata`). `usergroupResource.Metadata` does the same with `"_usergroup"`, producing `slack_usergroup`.

## Configure

Receive the provider-configured API client:

```go
func (r *fooResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
    if req.ProviderData == nil {
        return
    }
    client, ok := req.ProviderData.(*apiClient)
    if !ok {
        resp.Diagnostics.AddError("Unexpected Resource Configure Type",
            fmt.Sprintf("Expected *apiClient, got: %T", req.ProviderData))
        return
    }
    r.client = client
}
```

The `nil` check is required: Configure is called during validation when provider data is not yet available.

## Create

Contract:

- Read plan data from `req.Plan`
- Perform the API creation call
- Set ALL attribute values (including computed) in `resp.State`
- Unknown values in plan MUST become known in state (error otherwise)
- On error, the resource is marked tainted for recreation on next plan, unless state was already written

`conversationResource.Create` calls `CreateConversationContext`, then makes follow-up calls only for the attributes the practitioner actually configured, then re-reads the conversation through `findConversation` because the create response does not reliably carry the topic or purpose once they are set separately:

```go
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
```

`usergroupResource.Create` follows the same read-plan, call-API, re-read, apply-to-model shape, but adds a retry path: `usergroups.create` can fail with `name_already_exists` or `handle_already_exists`, and when it does, `reenableDisabled` looks for a disabled user group with a matching name or handle and takes it over instead of failing outright:

```go
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
```

`reenableDisabled` lists user groups including disabled ones, matches by name or (if configured) handle, and returns a plain error, "a user group with this name or handle already exists and is enabled; import it instead", when no disabled match exists. `TestAccUsergroup_RecreateReenablesDisabled` and `TestAccUsergroup_EnabledDuplicateFails` (`resource_usergroup_test.go`) exercise both branches.

### Partial Create: `keepPartialCreate`

If a follow-up call after the initial create fails (setting the topic, setting the purpose, archiving), the conversation already exists in Slack with a real ID. Returning an error from Create without writing any state would leave Terraform believing creation never started, and a retried apply would try `CreateConversationContext` again against a channel name that is now taken. `keepPartialCreate` re-reads whatever was actually created and writes it to state before returning the error, so the practitioner's next apply operates against the real, partially-configured conversation instead of colliding on the name:

```go
func (r *conversationResource) keepPartialCreate(ctx context.Context, plan *conversationResourceModel, resp *resource.CreateResponse) {
    info, err := r.findConversation(ctx, plan.Id.ValueString())
    if err != nil || info == nil {
        return
    }
    applyConversation(plan, info)
    resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}
```

`usergroupResource` has the identical helper for the same reason, guarding the gap between `usergroups.create` succeeding and `usergroups.users.update` (setting the initial members) failing.

## Read

Contract:

- Read prior state from `req.State`
- Perform the API read call
- If the resource no longer exists: call `resp.State.RemoveResource(ctx)` and return
- Otherwise, update all state values to reflect current API state

```go
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
```

`findConversation` collapses "gone" into a single `nil, nil` result, so Read only has one branch to handle, whether the channel was archived past recovery or never existed.

The Slack Web API has no `usergroups.info`, so `findUsergroup` cannot look a group up by ID directly; it lists every group (including disabled ones) and filters client-side:

```go
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
```

`usergroupResource.Read` treats a group with a non-zero `DateDelete` the same as a missing one: both mean the group should be removed from state, since Slack disables rather than deletes user groups.

## Update

Contract:

- Read plan data from `req.Plan` (the desired new state)
- Perform the API update call
- Set state to reflect the actual post-update values
- All values in state MUST match plan values (or Terraform produces an "inconsistent result" error)

```go
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
    // ...purpose follows the same shape...
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
```

Every call in Update is conditional: a field is only touched when the plan actually changes it, both to avoid unnecessary API calls and because unconfigured attributes (`topic`, `purpose`, `handle`, `description`, `channels`, `users`) must never be pushed to Slack (see `references/guides/plan-modification.md`).

## Delete

Contract:

- Read prior state from `req.State`
- Perform the API deletion
- If already deleted: return without error (idempotent)
- No need to modify state: framework removes it automatically on success

The Slack Web API cannot delete a conversation or a user group outside Enterprise Grid admin methods, so Delete archives or disables instead:

```go
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
```

`usergroupResource.Delete` re-reads the group first and returns early, without calling the API, if it is already missing or already disabled, since `usergroups.disable` is not itself idempotent against a group that is already disabled:

```go
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
```

## Handling Slack Errors

Slack reports a failed call as HTTP 200 with `"ok": false` and an error code, which `slack-go` surfaces as a `slack.SlackErrorResponse`. `errors.go` wraps this with two helpers that unwrap with `errors.As` rather than a type assertion or an `err.Error()` string comparison, since a wrapped error would fail a direct assertion:

```go
func hasSlackError(err error, codes ...string) bool {
    code := slackErrorCode(err)
    if code == "" {
        return false
    }
    for _, c := range codes {
        if code == c {
            return true
        }
    }
    return false
}

func isNotFound(err error) bool {
    return hasSlackError(err, errChannelNotFound)
}
```

The resources use these to turn specific, expected error codes into non-errors rather than failing the apply:

- `archive` ignores `already_archived` from `conversations.archive`
- `Update` ignores `not_archived` from `conversations.unarchive`
- `Create` treats `name_already_exists` and `handle_already_exists` from `usergroups.create` as a signal to retry via `reenableDisabled`, not as a failure

There is no Slack Web API equivalent of a wrapped `Success bool` / `Message string` RPC response. Every call either returns a transport `error` (already carrying the Slack error code via `slack.SlackErrorResponse`) or succeeds; there is no second, separate success flag to check on top of that.

## Related Framework References

| File                                | Contents                                  |
| ------------------------------------ | -------------------------------------------- |
| `framework/resources/index.mdx`     | Resource type definition, full interface  |
| `framework/resources/create.mdx`    | Create method details and caveats         |
| `framework/resources/read.mdx`      | Read method and state refresh             |
| `framework/resources/update.mdx`    | Update method and plan consistency        |
| `framework/resources/delete.mdx`    | Delete method                             |
| `framework/resources/configure.mdx` | Configure method, provider data injection |
