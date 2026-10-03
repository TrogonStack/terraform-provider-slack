# State Management

## Import

Import lets practitioners bring existing resources under Terraform management without recreating them.

### Simple Import (PassthroughID)

When the import ID is the same as the resource's `id` attribute. Both `slack_conversation` and `slack_usergroup` use this, since each one's `id` is just the Slack-assigned channel or group ID:

```go
var _ resource.ResourceWithImportState = &conversationResource{}

func (r *conversationResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
    resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}
```

Usage: `terraform import slack_conversation.example C0123456789`

`usergroupResource.ImportState` is identical, just against `slack_usergroup`:

```go
func (r *usergroupResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
    resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}
```

Usage: `terraform import slack_usergroup.example S0123456789`

Neither implementation does anything beyond the passthrough call. After `ImportState` sets `id`, Terraform calls Read to fill in every other attribute (`name`, `topic`, `channels`, `users`, and so on) from the live Slack data.

### Compound Import (Custom Parsing)

When import needs multiple values, because the resource's `id` is built by combining more than one field, `ImportState` parses the raw import string instead of passing it straight through. Neither resource in this provider needs this: `slack_conversation.id` and `slack_usergroup.id` are both plain Slack-assigned IDs, not a composite of other attributes. The shape below is illustrative, for a hypothetical future resource whose `id` combined two values (for example `<conversation_id>/<user_id>` for a channel membership resource):

```go
type fooImportID struct {
    First  string
    Second string
}

func parseFooImportID(raw string) (fooImportID, error) {
    parts := strings.SplitN(raw, "/", 2)
    if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
        return fooImportID{}, fmt.Errorf("expected import ID in the format <first>/<second>, got: %q", raw)
    }
    return fooImportID{First: parts[0], Second: parts[1]}, nil
}

func (r *fooResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
    parsed, err := parseFooImportID(req.ID)
    if err != nil {
        resp.Diagnostics.AddError("Invalid Import ID", err.Error())
        return
    }
    resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
    resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("first"), parsed.First)...)
    resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("second"), parsed.Second)...)
}
```

## State Upgrade

When you change a resource schema in a breaking way, existing state in `.tfstate` files won't match the new schema. State upgraders transform old state to the new format transparently. Neither `slack_conversation` nor `slack_usergroup` has needed one yet: both schemas set no `Version` (so it defaults to `0`), and neither implements `resource.ResourceWithUpgradeState`.

### When to Use

- Changing a list block to SingleNestedBlock
- Renaming attributes
- Changing attribute types (e.g., string to int)
- Restructuring nested objects, e.g., if `channels` or `users` ever moved from a flat `Set` of IDs to a list of nested objects carrying more than just an ID

### Implementation

1. Increment `Version` in the schema:

```go
func (r *fooResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
    resp.Schema = schema.Schema{
        Version: 1, // Was 0, now 1
        // ... current schema ...
    }
}
```

2. Implement `resource.ResourceWithUpgradeState`:

```go
var _ resource.ResourceWithUpgradeState = &fooResource{}

func (r *fooResource) UpgradeState(_ context.Context) map[int64]resource.StateUpgrader {
    return map[int64]resource.StateUpgrader{
        0: {
            StateUpgrader: func(ctx context.Context, req resource.UpgradeStateRequest, resp *resource.UpgradeStateResponse) {
                // Parse raw JSON from old state format
                var raw map[string]json.RawMessage
                if err := json.Unmarshal(req.RawState.JSON, &raw); err != nil {
                    resp.Diagnostics.AddError("State Upgrade Error",
                        fmt.Sprintf("Unable to parse raw state: %s", err))
                    return
                }

                var id string
                _ = json.Unmarshal(raw["id"], &id)

                state := fooResourceModel{
                    Id: types.StringValue(id),
                }
                resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
            },
        },
    }
}
```

### Key Points

- The map key is the OLD schema version (upgrade FROM version X)
- `req.RawState.JSON` contains the raw JSON bytes of the old state
- Parse manually: the old state shape does not match your current model struct
- After upgrade, Terraform calls Read to refresh state with current API data
- Multiple upgraders can be chained (0->1, 1->2, etc.)

## Private State

Store provider-internal data that is not visible in plan output. Useful for:

- ETags or version tokens for optimistic concurrency
- Internal identifiers that shouldn't be user-visible
- Cached metadata to avoid extra API calls

Not used anywhere in this provider today: the Slack Web API responses the resources call (`conversations.*`, `usergroups.*`) carry no ETag or version token the provider would need to stash privately. The pattern, if it's ever needed:

```go
var _ resource.ResourceWithPrivateState = &fooResource{}

// In Create or Update:
resp.Private.SetKey(ctx, "etag", []byte(apiResponse.Etag))

// In Read or Update:
etagBytes, diags := req.Private.GetKey(ctx, "etag")
etag := string(etagBytes)
```

## Writing State

### Full Model Write

Most common: write the entire model struct to state:

```go
resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
```

Every CRUD method on both resources ends this way (except Delete, which relies on the framework removing the resource automatically).

### Individual Attribute Write

Set a single attribute by path:

```go
resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("some_attribute"), value)...)
```

This provider does not use `SetAttribute` anywhere today: both `ImportState` implementations rely entirely on `ImportStatePassthroughID`, which sets only `id` directly, and let the framework's automatic follow-up Read populate every other attribute. A resource with a compound ID or extra computed identifiers set during import, as shown in "Compound Import" above, would need it.

### Removing Resource from State

When Read discovers the resource no longer exists. Both `conversationResource.Read` and `usergroupResource.Read` do this when their respective `find*` helper reports the resource gone (see `references/guides/resource-lifecycle.md`):

```go
resp.State.RemoveResource(ctx)
```

For `conversationResource`, that means `findConversation` returned `nil, nil`. For `usergroupResource`, that means `findUsergroup` returned `nil, nil`, or returned a group whose `DateDelete` is non-zero (Slack's way of marking a user group disabled). This tells Terraform the resource was deleted (or, for a user group, disabled) externally and needs recreation.

## Related Framework References

| File                                           | Contents                          |
| --------------------------------------------------- | -------------------------------------- |
| `framework/resources/import.mdx`               | Import state documentation        |
| `framework/resources/state-upgrade.mdx`        | State upgrade details             |
| `framework/resources/private-state.mdx`        | Private state storage             |
| `framework/resources/state-move.mdx`           | State move between resource types |
| `framework/handling-data/writing-state.mdx`    | Writing to response state         |
| `framework/handling-data/accessing-values.mdx` | Reading from state/plan/config    |
