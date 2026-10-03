# Schema Design

## Overview

Schemas define the shape of configuration, plan, and state data. Each attribute or block maps to a Go struct field via `tfsdk` tags.

```go
type conversationResourceModel struct {
    Id         types.String `tfsdk:"id"`
    Name       types.String `tfsdk:"name"`
    IsPrivate  types.Bool   `tfsdk:"is_private"`
    Topic      types.String `tfsdk:"topic"`
    Purpose    types.String `tfsdk:"purpose"`
    IsArchived types.Bool   `tfsdk:"is_archived"`
    Created    types.String `tfsdk:"created"`
}
```

## Attribute Types

### Primitives

| Schema Type               | Go Type         | Notes               |
| -------------------------- | ---------------- | --------------------- |
| `schema.StringAttribute`  | `types.String`  | UTF-8 string        |
| `schema.BoolAttribute`    | `types.Bool`    | true/false          |
| `schema.Int64Attribute`   | `types.Int64`   | 64-bit integer      |
| `schema.Int32Attribute`   | `types.Int32`   | 32-bit integer      |
| `schema.Float64Attribute` | `types.Float64` | 64-bit float        |
| `schema.Float32Attribute` | `types.Float32` | 32-bit float        |
| `schema.NumberAttribute`  | `types.Number`  | Arbitrary precision |

This provider only uses the first two rows. `slack_conversation.is_private` and `slack_conversation.is_archived` are `schema.BoolAttribute`; every other attribute on either resource is a `schema.StringAttribute` or a `schema.SetAttribute` of strings. No `Int64Attribute`, `Float64Attribute`, or `NumberAttribute` appears anywhere in this provider.

### Collections

| Schema Type            | Go Type      | Requires      |
| ------------------------ | -------------- | --------------- |
| `schema.ListAttribute` | `types.List` | `ElementType` |
| `schema.MapAttribute`  | `types.Map`  | `ElementType` |
| `schema.SetAttribute`  | `types.Set`  | `ElementType` |

`slack_usergroup.channels` and `slack_usergroup.users` are each a `schema.SetAttribute` of strings, since channel and user membership is unordered:

```go
"channels": schema.SetAttribute{
    Optional:    true,
    Computed:    true,
    ElementType: types.StringType,
    MarkdownDescription: "IDs of the default channels for the user group. Leave unset to leave them unmanaged.",
    PlanModifiers: []planmodifier.Set{
        setplanmodifier.UseStateForUnknown(),
    },
}
```

Neither `schema.ListAttribute` nor `schema.MapAttribute` is used anywhere in this provider.

### Nested Attributes (Protocol v6 only)

| Schema Type                    | Go Type                  | Use Case                |
| --------------------------------- | -------------------------- | -------------------------- |
| `schema.SingleNestedAttribute` | `*nestedModel`           | Single object           |
| `schema.ListNestedAttribute`   | `[]nestedModel`          | Ordered list of objects |
| `schema.MapNestedAttribute`    | `map[string]nestedModel` | Keyed objects           |
| `schema.SetNestedAttribute`    | `[]nestedModel`          | Unique set of objects   |

Neither resource in this provider uses nested attributes today; both `slack_conversation` and `slack_usergroup` are flat attribute bags. This table is here for when one is needed:

```go
schema.SingleNestedAttribute{
    Optional: true,
    Attributes: map[string]schema.Attribute{
        "key":   schema.StringAttribute{Required: true},
        "value": schema.StringAttribute{Required: true},
    },
}
```

## Blocks

Blocks are structural containers that appear as HCL blocks (with `{}` syntax). Use blocks for complex nested structures, especially when they can be optional or repeated.

| Schema Type                | Go Type                               | HCL Syntax                              |
| ----------------------------- | ---------------------------------------- | ------------------------------------------ |
| `schema.SingleNestedBlock` | `*nestedModel` (pointer for optional) | `block_name { ... }`                    |
| `schema.ListNestedBlock`   | `[]nestedModel`                       | `block_name { ... }` (repeated)         |
| `schema.SetNestedBlock`    | `[]nestedModel`                       | `block_name { ... }` (unique, repeated) |

This provider does not currently define any blocks; both `slack_conversation` and `slack_usergroup` are flat attribute bags. Reach for a block only if a future resource needs an optional, HCL-block-shaped nested structure.

### Blocks vs Nested Attributes

| Use Blocks When                                      | Use Nested Attributes When             |
| -------------------------------------------------------- | ------------------------------------------- |
| Optional complex object (pointer nil = not provided) | Always-present object structure        |
| Matching existing Terraform provider conventions     | New providers (preferred direction)    |
| HCL block syntax feels natural for the structure     | Programmatic, data-oriented structures |

## Attribute Behaviors

### Required, Optional, Computed

| Combination                                    | Meaning                                    |
| ------------------------------------------------- | ---------------------------------------------- |
| `Required: true`                               | User must provide; error if missing        |
| `Optional: true`                               | User may provide; null if omitted          |
| `Computed: true`                               | Provider sets the value; user cannot       |
| `Optional: true, Computed: true`               | User may provide OR provider fills         |
| `Optional: true, Computed: true, Default: ...` | User may provide; known default if omitted |

`slack_conversation.id` and `slack_conversation.created` are `Computed: true` only. `slack_conversation.name` is `Required: true`. `slack_conversation.is_private` and `slack_conversation.is_archived` are `Optional: true, Computed: true` with a `Default`. `slack_conversation.topic` and `slack_conversation.purpose` are `Optional: true, Computed: true` with no default: leaving either unset means the provider reads whatever value Slack already has but never pushes a change (see "Unmanaged Attributes" below). `slack_usergroup.handle`, `description`, `channels`, and `users` follow the identical `Optional: true, Computed: true` shape, for the same reason.

### Sensitive

```go
schema.StringAttribute{
    Required:  true,
    Sensitive: true, // Value hidden in plan/state output
}
```

The provider's own `token` attribute (in `provider.go`) is `Sensitive: true`. Neither `slack_conversation` nor `slack_usergroup` has a `Sensitive` attribute: channel and user group IDs are not secret values.

### Deprecation

```go
schema.StringAttribute{
    Optional:           true,
    DeprecationMessage: "Use 'new_field' instead.",
}
```

Not used anywhere in this provider yet.

## Defaults

Set a known value when the user does not provide one. Requires `Optional: true, Computed: true`.

```go
import "github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"

schema.BoolAttribute{
    Optional: true,
    Computed: true,
    Default:  booldefault.StaticBool(false),
}
```

This is the actual `is_private` and `is_archived` attributes on `slack_conversation` (`resource_conversation.go`): both default to `false` when the practitioner leaves them out of the configuration.

## Plan Modifiers

Control how attribute values change during planning.

```go
import "github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
import "github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"

schema.StringAttribute{
    Computed: true,
    PlanModifiers: []planmodifier.String{
        stringplanmodifier.UseStateForUnknown(), // ID: stable after creation
    },
}

schema.BoolAttribute{
    Optional: true,
    Computed: true,
    Default:  booldefault.StaticBool(false),
    PlanModifiers: []planmodifier.Bool{
        boolplanmodifier.RequiresReplace(), // Immutable: forces recreation
    },
}
```

`slack_conversation.is_private` uses `boolplanmodifier.RequiresReplace()`, since the Slack Web API has no way to convert a public channel to private or back. Full details: `references/guides/plan-modification.md`.

## Validators

Constrain acceptable values at plan time.

```go
import "github.com/hashicorp/terraform-plugin-framework-validators/setvalidator"
import "github.com/hashicorp/terraform-plugin-framework/schema/validator"

schema.SetAttribute{
    Optional:    true,
    Computed:    true,
    ElementType: types.StringType,
    Validators: []validator.Set{
        setvalidator.SizeAtLeast(1),
    },
}
```

This is `slack_usergroup.users` (`resource_usergroup.go`), the only validator currently used anywhere in this provider: `usergroups.users.update` requires at least one user, so an empty `users` set is rejected at plan time. `channels` has no such constraint; an empty set of default channels is a valid configuration. Full details: `references/guides/validation.md`.

## The `rsId()` Helper

This provider's standard ID attribute pattern, defined in `helpers.go`:

```go
func rsId() schema.StringAttribute {
    return schema.StringAttribute{
        Computed:            true,
        MarkdownDescription: "The unique ID of this resource.",
        PlanModifiers: []planmodifier.String{
            stringplanmodifier.UseStateForUnknown(),
        },
    }
}
```

Use `"id": rsId()` in every resource schema. `slack_conversation.id` is the channel ID Slack assigns (for example `C0123456789`); `slack_usergroup.id` is the user group ID (for example `S0123456789`). Unlike a provider with a compound ID, neither value is built by concatenating fields: both are plain Slack-assigned IDs, and both resources import with a simple ID passthrough (see `references/guides/state-management.md`).

## Accessing Values from Models

```go
// Read plan/config into model
var plan conversationResourceModel
resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)

// Access primitive values
id := plan.Id.ValueString()

// Check whether an Optional+Computed string was actually set by the practitioner
func isConfigured(v types.String) bool {
    return !v.IsNull() && !v.IsUnknown()
}

if isConfigured(plan.Topic) {
    // the practitioner set a topic; push it to Slack
}

// Access set elements, sorted for stable comparisons
func setStrings(ctx context.Context, v types.Set) ([]string, diag.Diagnostics) {
    if v.IsNull() || v.IsUnknown() {
        return nil, nil
    }
    out := []string{}
    diags := v.ElementsAs(ctx, &out, false)
    sort.Strings(out)
    return out, diags
}

// Set values
plan.Id = types.StringValue("C0123456789")
```

`isConfigured` and `setStrings` are both real helpers in `helpers.go`, used throughout `resource_conversation.go` and `resource_usergroup.go` instead of inline `IsNull()`/`IsUnknown()` checks.

## Related Framework References

| File                                                   | Contents                              |
| ---------------------------------------------------------- | ------------------------------------------ |
| `framework/handling-data/schemas.mdx`                  | Schema definition fundamentals        |
| `framework/handling-data/attributes/index.mdx`         | All attribute types overview          |
| `framework/handling-data/attributes/string.mdx`        | String attribute details              |
| `framework/handling-data/attributes/list-nested.mdx`   | List nested attribute                 |
| `framework/handling-data/attributes/single-nested.mdx` | Single nested attribute               |
| `framework/handling-data/blocks/index.mdx`             | Block types overview                  |
| `framework/handling-data/blocks/single-nested.mdx`     | SingleNestedBlock details             |
| `framework/handling-data/types/index.mdx`              | Go value types                        |
| `framework/handling-data/accessing-values.mdx`         | Reading values from state/plan/config |
| `framework/handling-data/writing-state.mdx`            | Writing values to state               |
| `framework/resources/default.mdx`                      | Default values                        |
