# Plan Modification

## Overview

After validation and before apply, Terraform generates a plan describing expected values. Plan modifiers let you:

- Provide known values for computed attributes (reduce "known after apply" noise)
- Mark resources for replacement when in-place update is impossible
- Return diagnostics on planned changes

## Plan Modification Process

1. Null config values get their default value applied
2. If plan differs from state, computed attributes with null config become unknown
3. Attribute plan modifiers run (in schema order)
4. Resource-level plan modifiers run (`ModifyPlan`)

After apply, all state values MUST match planned values or Terraform produces a "Provider produced inconsistent result" error.

## Built-in Attribute Plan Modifiers

Available in `resource/schema/<type>planmodifier` packages:

### UseStateForUnknown

Copies the prior state value into the plan. Use for computed values that don't change after creation (IDs, creation timestamps), and for Optional+Computed values the practitioner may be leaving unmanaged.

```go
import "github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"

schema.StringAttribute{
    Computed: true,
    PlanModifiers: []planmodifier.String{
        stringplanmodifier.UseStateForUnknown(),
    },
}
```

This provider's `rsId()` helper wraps this pattern for `id` on both resources. `slack_conversation.topic`, `purpose`, and `created`, and `slack_usergroup.handle` and `description`, all use the same modifier directly.

### RequiresReplace

Forces resource destruction and recreation when the attribute value changes. Use for immutable API fields.

```go
schema.BoolAttribute{
    Optional: true,
    Computed: true,
    Default:  booldefault.StaticBool(false),
    PlanModifiers: []planmodifier.Bool{
        boolplanmodifier.RequiresReplace(),
    },
}
```

`slack_conversation.is_private` uses this: the Slack Web API has no operation to convert a public channel to private or back, so changing it means a different conversation, not an update to the current one. `TestAccConversation_Private` asserts the plan action is `plancheck.ResourceActionReplace` when `is_private` changes.

### RequiresReplaceIf

Conditional replacement based on provider-defined logic:

```go
stringplanmodifier.RequiresReplaceIf(
    func(ctx context.Context, req planmodifier.StringRequest, resp *stringplanmodifier.RequiresReplaceIfFuncResponse) {
        // Only replace if changing from non-empty to different non-empty
        resp.RequiresReplace = !req.StateValue.IsNull() && !req.PlanValue.IsNull()
    },
    "Replace when changing between non-null values",
    "Replace when changing between non-null values",
)
```

Not used anywhere in this provider yet.

### RequiresReplaceIfConfigured

Like RequiresReplace but only triggers if the practitioner explicitly configured the value (not null):

```go
stringplanmodifier.RequiresReplaceIfConfigured()
```

Not used anywhere in this provider yet.

## Available Modifier Packages

Each type has its own package:

| Type    | Package                               | Modifiers                                                                           |
| --------- | ---------------------------------------- | ---------------------------------------------------------------------------------------- |
| String  | `resource/schema/stringplanmodifier`  | UseStateForUnknown, RequiresReplace, RequiresReplaceIf, RequiresReplaceIfConfigured |
| Bool    | `resource/schema/boolplanmodifier`    | UseStateForUnknown, RequiresReplace, RequiresReplaceIf, RequiresReplaceIfConfigured |
| Int64   | `resource/schema/int64planmodifier`   | UseStateForUnknown, RequiresReplace, RequiresReplaceIf, RequiresReplaceIfConfigured |
| Float64 | `resource/schema/float64planmodifier` | UseStateForUnknown, RequiresReplace, RequiresReplaceIf, RequiresReplaceIfConfigured |
| List    | `resource/schema/listplanmodifier`    | UseStateForUnknown, RequiresReplace, RequiresReplaceIf, RequiresReplaceIfConfigured |
| Map     | `resource/schema/mapplanmodifier`     | UseStateForUnknown, RequiresReplace, RequiresReplaceIf, RequiresReplaceIfConfigured |
| Set     | `resource/schema/setplanmodifier`     | UseStateForUnknown, RequiresReplace, RequiresReplaceIf, RequiresReplaceIfConfigured |
| Object  | `resource/schema/objectplanmodifier`  | UseStateForUnknown, RequiresReplace, RequiresReplaceIf, RequiresReplaceIfConfigured |

`slack_usergroup.channels` and `slack_usergroup.users` both use `setplanmodifier.UseStateForUnknown()`, the Set-typed counterpart to the String one above.

## Custom Plan Modifiers

Implement the relevant `planmodifier.<Type>` interface:

```go
type myModifier struct{}

func (m myModifier) Description(_ context.Context) string {
    return "Description for practitioners"
}

func (m myModifier) MarkdownDescription(_ context.Context) string {
    return "Markdown description for practitioners"
}

func (m myModifier) PlanModifyString(_ context.Context, req planmodifier.StringRequest, resp *planmodifier.StringResponse) {
    // Access current state, config, plan values:
    //   req.StateValue  - prior state
    //   req.ConfigValue - configuration value
    //   req.PlanValue   - current plan value
    //
    // Modify plan:
    //   resp.PlanValue = types.StringValue("new-value")
    //   resp.RequiresReplace = true
}
```

Not used anywhere in this provider yet; both resources get by with the built-in modifiers above.

## Resource-Level Plan Modification

Implement `resource.ResourceWithModifyPlan` for cross-attribute logic:

```go
func (r *fooResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
    // Access full plan, state, config
    // Can add diagnostics, mark for replacement, modify plan values

    if req.Plan.Raw.IsNull() {
        // Resource is being destroyed
        return
    }

    var plan fooResourceModel
    resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
}
```

Not used anywhere in this provider yet.

## Common Patterns in This Provider

### ID attributes (stable after creation)

```go
"id": rsId() // Uses UseStateForUnknown internally
```

### Immutable fields (force recreation)

```go
"is_private": schema.BoolAttribute{
    Optional: true,
    Computed: true,
    Default:  booldefault.StaticBool(false),
    PlanModifiers: []planmodifier.Bool{
        boolplanmodifier.RequiresReplace(),
    },
}
```

### Unmanaged attributes

`slack_conversation.topic` and `purpose`, and `slack_usergroup.handle`, `description`, `channels`, and `users`, are all `Optional: true, Computed: true` with `UseStateForUnknown()` (or its Set-typed form). Leaving one of these unset in configuration means the provider manages it minimally: Create and Update both guard the API call behind `isConfigured(...)` (for strings) or a `nil` slice from `setStrings` (for sets), so an attribute the practitioner never set is never pushed to Slack:

```go
if isConfigured(plan.Topic) && plan.Topic.ValueString() != created.Topic.Value {
    if _, err := r.client.slack.SetTopicOfConversationContext(ctx, created.ID, plan.Topic.ValueString()); err != nil {
        // ...
    }
}
```

`UseStateForUnknown` is what keeps the plan quiet for these attributes: without it, an unset Optional+Computed attribute would show "(known after apply)" on every plan, even though nothing is actually going to change. With it, Terraform carries the prior state value forward into the plan instead.

On the following Read, `applyConversation` and `applyUsergroup` always write back whatever Slack currently reports, regardless of what was configured, so a value changed directly in the Slack client (outside Terraform) is adopted into state rather than overwritten on the next apply. `TestAccConversation_UnmanagedTopicIsKept` and `TestAccUsergroup_UnmanagedMembers` (`resource_conversation_test.go`, `resource_usergroup_test.go`) both set a value directly on the fake backend between steps and assert `plancheck.ExpectEmptyPlan()`, proving the unmanaged value is kept rather than reverted.

## Related Framework References

| File                                        | Contents                             |
| ---------------------------------------------- | ----------------------------------------- |
| `framework/resources/plan-modification.mdx` | Full plan modification documentation |
| `framework/resources/default.mdx`           | Default values (interact with plan)  |
| `framework/handling-data/schemas.mdx`       | Schema definition                    |
