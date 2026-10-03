# Provider-Defined Functions

## Overview

Provider-defined functions let practitioners call provider logic directly in HCL expressions, outside any resource or data source, since Terraform 1.8. This provider defines none today: `slackProvider` does not implement `provider.ProviderWithFunctions`, and there is no `Functions()` method anywhere in `provider.go`. Everything below is illustrative, built around a real documented fact about this provider's schema, to show the shape a function would take if one were added.

## Function Interface

```go
type Function interface {
    Metadata(context.Context, MetadataRequest, *MetadataResponse)
    Definition(context.Context, DefinitionRequest, *DefinitionResponse)
    Run(context.Context, RunRequest, *RunResponse)
}
```

## Registration

Would require adding `provider.ProviderWithFunctions` to `slackProvider` and a `Functions` method:

```go
func (p *slackProvider) Functions(_ context.Context) []func() function.Function {
    return []func() function.Function{
        newStripHandlePrefixFunction,
    }
}
```

## Illustrative Example: `strip_handle_prefix`

`slack_usergroup.handle`'s real `MarkdownDescription` documents that Slack stores a user group's handle without the leading `@` practitioners type when mentioning a group (`@team-a` mentions the group whose stored `handle` is `team-a`). A practitioner migrating handles from another system that does store the `@` might want a pure string-transform function to strip it before passing the value into `slack_usergroup.handle`. This is a hypothetical use of that one real, documented fact; this provider has no such function today.

### Metadata

```go
func (f *stripHandlePrefixFunction) Metadata(_ context.Context, req function.MetadataRequest, resp *function.MetadataResponse) {
    resp.Name = "strip_handle_prefix"
}
```

### Definition

```go
func (f *stripHandlePrefixFunction) Definition(_ context.Context, _ function.DefinitionRequest, resp *function.DefinitionResponse) {
    resp.Definition = function.Definition{
        Summary:             "Strips a leading '@' from a handle",
        MarkdownDescription: "Removes a leading `@` from a string, matching how Slack stores a user group's `handle` without it.",
        Parameters: []function.Parameter{
            function.StringParameter{
                Name:                "input",
                MarkdownDescription: "The handle, with or without a leading `@`.",
            },
        },
        Return: function.StringReturn{},
    }
}
```

### Run

```go
func (f *stripHandlePrefixFunction) Run(ctx context.Context, req function.RunRequest, resp *function.RunResponse) {
    var input string
    resp.Error = function.ConcatFuncErrors(resp.Error, req.Arguments.Get(ctx, &input))
    if resp.Error != nil {
        return
    }

    output := strings.TrimPrefix(input, "@")

    resp.Error = function.ConcatFuncErrors(resp.Error, resp.Result.Set(ctx, output))
}
```

### Usage

```hcl
resource "slack_usergroup" "team_a" {
  name   = "Team A"
  handle = provider::slack::strip_handle_prefix("@team-a")
  users  = ["U0123456789"]
}
```

## Parameter Types

| Schema Parameter Type       | Go Type         |
| ------------------------------ | ---------------- |
| `function.StringParameter`   | `string`        |
| `function.BoolParameter`     | `bool`          |
| `function.Int64Parameter`    | `int64`         |
| `function.Float64Parameter`  | `float64`       |
| `function.ListParameter`     | `[]T`           |
| `function.MapParameter`      | `map[string]T`  |
| `function.ObjectParameter`   | struct          |
| `function.DynamicParameter`  | `any`           |

`strip_handle_prefix` above uses only `function.StringParameter`; none of the other parameter types has a use case anywhere in this provider's current schema.

## Variadic Parameters

```go
Parameters: []function.Parameter{
    function.StringParameter{Name: "separator"},
},
VariadicParameter: function.StringParameter{
    Name: "values",
},
```

Not applicable to `strip_handle_prefix`, which takes exactly one argument. Would matter for a hypothetical function joining a variable number of channel or user IDs.

## Return Types

Same type set as parameters, via `function.<Type>Return{}`. `strip_handle_prefix` returns `function.StringReturn{}`.

## Error Handling

```go
resp.Error = function.ConcatFuncErrors(resp.Error, function.NewArgumentFuncError(0, "input must not be empty"))
```

`function.NewArgumentFuncError(index, message)` ties an error to a specific argument position, which Terraform surfaces pointing at that argument in the calling expression.

## Testing Functions

```go
func TestStripHandlePrefixFunction(t *testing.T) {
    resource.UnitTest(t, resource.TestCase{
        ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
        Steps: []resource.TestStep{
            {
                Config: `
output "test" {
  value = provider::slack::strip_handle_prefix("@team-a")
}
`,
                Check: resource.ComposeAggregateTestCheckFunc(
                    resource.TestCheckOutput("test", "team-a"),
                ),
            },
        },
    })
}
```

No such test exists in this provider, since the function itself does not exist; this mirrors the real acceptance-test shape described in `references/guides/testing.md`, adapted to check a function output instead of a resource attribute.

## Related Framework References

| File                                     | Contents                   |
| ------------------------------------------- | ------------------------------- |
| `framework/functions/index.mdx`          | Function interface overview |
| `framework/functions/parameters.mdx`     | Parameter types              |
| `framework/functions/returns.mdx`        | Return types                 |
| `framework/functions/errors.mdx`         | Error handling                |
| `framework/functions/implementation.mdx` | Implementation guidance      |
