# Validation

## Overview

Validation catches configuration errors before apply, giving practitioners fast, actionable feedback instead of a failed API call mid-apply. Terraform Plugin Framework offers several validation layers.

## Attribute Validators

Attached directly to a schema attribute, run during plan.

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

This is the real `slack_usergroup.users` attribute in `resource_usergroup.go`, and it is the only validator used anywhere in this provider today. Slack's `usergroups.users.update` endpoint rejects an empty user list with `invalid_users`, so `SizeAtLeast(1)` catches an empty `users` set at plan time instead of letting the practitioner hit an API error on apply. `channels` has no equivalent validator: an empty set of default channels is a valid configuration, since `usergroups.update` accepts an empty channel list.

`fakeSlack` (`fake_slack_test.go`) mirrors the real API's rejection for coverage of the Create/Update path itself, and `TestAccUsergroup_EmptyUsersRejected` (`resource_usergroup_test.go`) asserts the plan-time validator error, using `ExpectError` against a config with `users = []`.

## Common Validators by Type

### String Validators (`stringvalidator`)

| Validator                       | Purpose                     |
| ---------------------------------- | ------------------------------ |
| `LengthAtLeast(n)`              | Minimum string length        |
| `LengthAtMost(n)`               | Maximum string length        |
| `LengthBetween(min, max)`       | Length range                |
| `OneOf(values...)`              | Enum-style constraint        |
| `NoneOf(values...)`             | Exclusion constraint         |
| `RegexMatches(regex, message)`  | Pattern match                |
| `UTF8LengthAtLeast(n)`          | Minimum UTF-8 length         |

None of these are used anywhere in this provider. Neither `slack_conversation` nor `slack_usergroup` constrains string length, enum values, or pattern shape at the schema level; Slack's own API validates names, handles, and IDs and returns an error code the resource surfaces through `hasSlackError` (see `references/guides/resource-lifecycle.md`).

### Collection Validators (`setvalidator`, `listvalidator`, `mapvalidator`)

| Validator          | Purpose              |
| --------------------- | ----------------------- |
| `SizeAtLeast(n)`    | Minimum element count |
| `SizeAtMost(n)`     | Maximum element count |
| `SizeBetween(min, max)` | Element count range |
| `ValueStringsAre(...)` | Per-element string validators |

`setvalidator.SizeAtLeast(1)` on `slack_usergroup.users` is the only one of these in active use. `SizeAtMost`, `SizeBetween`, and `ValueStringsAre` are unused.

### Numeric Validators (`int64validator`, `float64validator`)

| Validator           | Purpose          |
| --------------------- | ------------------ |
| `AtLeast(n)`        | Minimum value     |
| `AtMost(n)`         | Maximum value     |
| `Between(min, max)` | Value range       |

Not used anywhere in this provider: neither resource has an `Int64Attribute` or `Float64Attribute` (see `references/guides/schema-design.md`).

## Conflict and Dependency Validators

Cross-attribute validators, usually attached at the schema level via `Validators` on the whole `schema.Schema`, or passed to `schemavalidator`:

```go
import "github.com/hashicorp/terraform-plugin-framework-validators/schemavalidator"

schemavalidator.ConflictsWith(path.MatchRoot("field_a"), path.MatchRoot("field_b"))
schemavalidator.AtLeastOneOf(path.MatchRoot("field_a"), path.MatchRoot("field_b"))
schemavalidator.ExactlyOneOf(path.MatchRoot("field_a"), path.MatchRoot("field_b"))
schemavalidator.RequiredTogether(path.MatchRoot("field_a"), path.MatchRoot("field_b"))
```

Not used anywhere in this provider. `slack_conversation` and `slack_usergroup` have no mutually exclusive or jointly required attribute pairs: every attribute on both resources can be set independently of every other one.

## Custom Validators

Implement the relevant `validator.<Type>` interface for provider-specific logic. This provider defines none today; the shape below is illustrative, using the real constraint (documented in the real schema's `MarkdownDescription`) that a user group's `handle` excludes the leading `@` that practitioners type when mentioning a group in Slack:

```go
type handleValidator struct{}

func (v handleValidator) Description(_ context.Context) string {
    return "handle must not include a leading '@'"
}

func (v handleValidator) MarkdownDescription(_ context.Context) string {
    return "`handle` must not include a leading `@`"
}

func (v handleValidator) ValidateString(ctx context.Context, req validator.StringRequest, resp *validator.StringResponse) {
    if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
        return
    }
    if strings.HasPrefix(req.ConfigValue.ValueString(), "@") {
        resp.Diagnostics.AddAttributeError(
            req.Path,
            "Invalid Handle",
            "handle must not include a leading '@'; Slack stores handles without it",
        )
    }
}
```

`slack_usergroup.handle` does not actually register this validator today: Slack's own `usergroups.create`/`usergroups.update` silently accept (and store) a handle without stripping a leading `@`, so this would be a provider-side convenience check, not a documented API rejection. Treat it as a hypothetical, not a real part of this provider.

## Resource-Level Validation (ValidateConfig)

For validation spanning multiple attributes, at the whole-resource level rather than a single schema node:

```go
var _ resource.ResourceWithValidateConfig = &fooResource{}

func (r *fooResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
    var config fooResourceModel
    resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
    if resp.Diagnostics.HasError() {
        return
    }

    if isConfigured(config.Name) && isConfigured(config.Handle) && config.Name.ValueString() == config.Handle.ValueString() {
        resp.Diagnostics.AddError(
            "Invalid Configuration",
            "name and handle must not be identical",
        )
    }
}
```

Neither `conversationResource` nor `usergroupResource` implements `resource.ResourceWithValidateConfig`. The example above is hypothetical, built from the real `usergroupResourceModel` fields (`Name`, `Handle`) and the real `isConfigured` helper, to illustrate the shape; it is not a rule this provider actually enforces, and Slack's API itself places no such constraint on a user group's name versus its handle.

## Diagnostics

All validation reports through `diag.Diagnostics`:

```go
resp.Diagnostics.AddError("Summary", "Detail message")
resp.Diagnostics.AddAttributeError(path.Root("field"), "Summary", "Detail message")
resp.Diagnostics.AddWarning("Summary", "Detail message")
```

Guidelines:

- **Summary**: short, no punctuation, title case
- **Detail**: full sentence(s), actionable
- Attribute-level errors (`AddAttributeError`) point practitioners to the specific line in configuration
- Multiple diagnostics can accumulate before returning

The real pattern in this provider is simpler: both resources surface Slack API failures with `resp.Diagnostics.AddError("API Error", fmt.Sprintf("Unable to ...: %s", err))` from Create, Read, Update, and Delete, after checking `hasSlackError`/`isNotFound` (see `references/guides/resource-lifecycle.md`). There is no provider-defined "Invalid Configuration" error anywhere in the current code; the `ValidateConfig` example above would be the first one if it were ever added.

## Validation Timing

```
Config → ValidateConfig (resource, provider, data source) → Attribute Validators → Plan
```

Validators run during `terraform plan` (and `terraform validate`), before any API calls. This is why `setvalidator.SizeAtLeast(1)` on `slack_usergroup.users` catches an empty set before `usergroups.users.update` would ever be called, and why `TestAccUsergroup_EmptyUsersRejected` can assert the failure without the fake backend being involved at all.

## Related Framework References

| File                                               | Contents                    |
| ------------------------------------------------------- | -------------------------------- |
| `framework/handling-data/validation/index.mdx`     | Validation overview         |
| `framework/handling-data/attributes/string.mdx`    | String attribute validators |
| `framework/resources/validate-configuration.mdx`   | Resource ValidateConfig     |
| `framework/providers/validate-configuration.mdx`   | Provider ValidateConfig     |
| `framework/diagnostics.mdx`                        | Diagnostics API              |
