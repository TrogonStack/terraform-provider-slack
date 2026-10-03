# Data Source Lifecycle

The provider does not define any data sources today (`DataSources()` in `provider.go` returns an empty slice). Everything below is illustrative: the pattern to follow when one is added, built from the same client and lookup helpers the two resources already use.

## Interface

A data source must implement `datasource.DataSource`:

```go
type DataSource interface {
    Metadata(context.Context, MetadataRequest, *MetadataResponse)
    Schema(context.Context, SchemaRequest, *SchemaResponse)
    Read(context.Context, ReadRequest, *ReadResponse)
}
```

Optional interfaces:

- `datasource.DataSourceWithConfigure`: receive provider client
- `datasource.DataSourceWithValidateConfig`: configuration validation

## Registration

```go
func newUsergroupDataSource() datasource.DataSource { return &usergroupDataSource{} }

// In provider.go:
func (p *slackProvider) DataSources(ctx context.Context) []func() datasource.DataSource {
    return []func() datasource.DataSource{
        newUsergroupDataSource,
    }
}
```

## Metadata

```go
func (d *usergroupDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
    resp.TypeName = req.ProviderTypeName + "_usergroup"
}
```

## Schema

Data source schemas use the `datasource/schema` package (not `resource/schema`):

```go
import "github.com/hashicorp/terraform-plugin-framework/datasource/schema"

func (d *usergroupDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
    resp.Schema = schema.Schema{
        Attributes: map[string]schema.Attribute{
            "id":     schema.StringAttribute{Computed: true},
            "handle": schema.StringAttribute{Required: true},
            "name":   schema.StringAttribute{Computed: true},
        },
    }
}
```

Key differences from resource schemas:

- No plan modifiers (no plan phase for data sources)
- No defaults (no apply phase)
- Attributes are either Required (lookup key) or Computed (returned value)
- Optional attributes serve as optional filter criteria

## Configure

Same pattern as resources:

```go
func (d *usergroupDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
    if req.ProviderData == nil {
        return
    }
    client, ok := req.ProviderData.(*apiClient)
    if !ok {
        resp.Diagnostics.AddError("Unexpected DataSource Configure Type",
            fmt.Sprintf("Expected *apiClient, got: %T", req.ProviderData))
        return
    }
    d.client = client
}
```

## Read

Contract:

- Read configuration from `req.Config` (the user-provided lookup criteria)
- Perform the API call to find the data
- If not found: add an error diagnostic (data sources must find their target)
- Set all attribute values in `resp.State`

The Slack Web API has no single-item get RPC for a user group, exactly as `usergroupResource.findUsergroup` already works around in `resource_usergroup.go` (see `references/guides/resource-lifecycle.md`). A data source would follow the same list-and-filter shape, but filter by `handle` instead of `id`, since practitioners who want to look up an existing group usually know its mention handle rather than its internal ID, and would treat "not found" as an error instead of a state removal:

```go
func (d *usergroupDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
    var data usergroupDataSourceModel
    resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
    if resp.Diagnostics.HasError() {
        return
    }

    handle := data.Handle.ValueString()
    groups, err := d.client.slack.GetUserGroupsContext(ctx, slack.GetUserGroupsOptionIncludeUsers(true))
    if err != nil {
        resp.Diagnostics.AddError("API Error", fmt.Sprintf("Unable to list user groups: %s", err))
        return
    }

    var found *slack.UserGroup
    for i := range groups {
        if groups[i].Handle == handle {
            found = &groups[i]
        }
    }
    if found == nil {
        resp.Diagnostics.AddError("Not Found", fmt.Sprintf("User group with handle %q not found", handle))
        return
    }

    data.Id = types.StringValue(found.ID)
    data.Name = types.StringValue(found.Name)
    resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}
```

`GetUserGroupsContext` without `GetUserGroupsOptionIncludeDisabled(true)` already excludes disabled groups by default, which is the right behavior for a lookup: a data source should not resolve to a group that destroying the matching `slack_usergroup` resource would have disabled.

## Data Sources vs Resources

| Aspect           | Resource                     | Data Source          |
| ------------------ | ------------------------------- | ----------------------- |
| Purpose          | Manage lifecycle (CRUD)      | Read-only lookup     |
| Methods          | Create, Read, Update, Delete | Read only            |
| Import           | Supported                    | N/A                  |
| Plan modifiers   | Yes                          | No                   |
| Defaults         | Yes                          | No                   |
| State management | Full lifecycle               | Refreshed every plan |
| Not found        | RemoveResource (drift)       | Error diagnostic     |

## Related Framework References

| File                                                | Contents                            |
| ------------------------------------------------------ | -------------------------------------- |
| `framework/data-sources/index.mdx`                  | Data source interface, registration |
| `framework/data-sources/configure.mdx`              | Configure method                    |
| `framework/data-sources/validate-configuration.mdx` | Validation                          |
| `framework/data-sources/timeouts.mdx`               | Timeout support                     |
