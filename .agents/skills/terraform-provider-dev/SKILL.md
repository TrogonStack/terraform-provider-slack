---
name: terraform-provider-dev
description: >
  Use this skill when developing terraform-provider-slack: adding
  resources or data sources, designing schemas, implementing CRUD operations,
  plan modification, state upgrades, import, validation, acceptance testing,
  debugging, or any Terraform Plugin Framework work in Go. Also use when the
  user asks about terraform provider patterns, attribute types, or how to
  structure tests. This is the primary development skill for this repository.
---

# Terraform Provider Development (Plugin Framework)

## Mental Model

- Provider = Go server implementing Terraform RPCs (GetProviderSchema, PlanResourceChange, ApplyResourceChange, ReadResource, etc.)
- Resource = struct implementing `resource.Resource` interface: Metadata, Schema, Configure, Create, Read, Update, Delete
- DataSource = struct implementing `datasource.DataSource` interface: Metadata, Schema, Configure, Read
- Schema defines the "shape" of config/plan/state: attributes (leaf values) and blocks (nested structures)
- Plan then Apply: Terraform calls PlanResourceChange (propose changes), then ApplyResourceChange (execute)
- State = Terraform's record of the real world; Plan = expected post-apply state
- Computed attributes: set by the provider from API responses (IDs, timestamps, server-generated values)
- Plugin Framework uses strong Go types: `types.String`, `types.Bool`, `types.Int64`, `types.List`, etc.
- Null vs Unknown: null means the user did not set it; unknown means the value will be known after apply (planned computed)

---

## This Provider: Conventions

- **Package**: `internal/provider` (single flat package, all resources here)
- **File naming**: `resource_<name>.go`, `resource_<name>_test.go`, `data_source_<name>.go`
- **Provider client**: `*apiClient` wraps `*slack.Client` (`github.com/slack-go/slack`), built with the retrying HTTP client from `retry.go`
- **Client injection**: Configure method casts `req.ProviderData.(*apiClient)`
- **ID helper**: `rsId()` returns a Computed StringAttribute with `UseStateForUnknown`
- **Simple import**: `resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)` (used by `slack_conversation` and `slack_usergroup`)
- **Registration**: add the constructor to `Resources()` in `provider.go`; `DataSources()` is currently empty
- **Error handling**: Slack reports a failure as HTTP 200 with `"ok": false` and an error code, which `slack-go` returns as a `slack.SlackErrorResponse`. Match codes with `hasSlackError(err, codes...)` and `isNotFound(err)` in `errors.go`, which unwrap with `errors.As` (a wrapped error would fail a direct type assertion, and `err.Error()` strings are not a contract)
  - **Read**: call `resp.State.RemoveResource(ctx)` and return (resource was deleted externally)
  - **Delete**: return without error (idempotent)
- **No get-by-ID for user groups**: Slack has no `usergroups.info`. `findUsergroup` lists with `include_disabled` and `include_users` and filters by ID; a non-zero `DateDelete` means the group is disabled and counts as gone
- **Destroy semantics**: Slack cannot delete channels or user groups. Delete archives a `slack_conversation` and disables a `slack_usergroup`
- **Context**: always call the `...Context` variant of a `slack-go` method with the CRUD method's `ctx`
- **Retry**: `retry.go` wraps the client's HTTP transport with automatic retry on 429 and 5xx except 501, honoring `Retry-After`. No configuration attribute
- **Testing**: `fakeSlack` in `fake_slack_test.go` is an `http.Handler` that serves the `conversations.*` and `usergroups.*` methods with Slack's own error codes; `setupTestServer` + `setupTestClient` wire it up, no real API calls. `live_test.go` holds `TestLive_*` against a real workspace, skipped unless `TF_ACC` and `SLACK_TOKEN` are set

---

## Adding a New Resource

1. Create `internal/provider/resource_<name>.go`
2. Define model struct(s) with `tfsdk` tags
3. Implement the resource:

```go
var (
    _ resource.Resource                = &fooResource{}
    _ resource.ResourceWithImportState = &fooResource{}
)

func newFoo() resource.Resource { return &fooResource{} }

type fooResource struct {
    client *apiClient
}

type fooResourceModel struct {
    Id   types.String `tfsdk:"id"`
    Name types.String `tfsdk:"name"`
}

func (r *fooResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
    resp.TypeName = req.ProviderTypeName + "_foo"
}

func (r *fooResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
    resp.Schema = schema.Schema{
        Attributes: map[string]schema.Attribute{
            "id":   rsId(),
            "name": schema.StringAttribute{Required: true},
        },
    }
}

func (r *fooResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
    if req.ProviderData == nil {
        return
    }
    client, ok := req.ProviderData.(*apiClient)
    if !ok {
        resp.Diagnostics.AddError("Unexpected Resource Configure Type", fmt.Sprintf("Expected *apiClient, got: %T", req.ProviderData))
        return
    }
    r.client = client
}
```

4. Implement Create, Read, Update, Delete (see guide: `references/guides/resource-lifecycle.md`)
5. Implement ImportState
6. Register in `provider.go`: add `newFoo` to `Resources()` return slice
7. Create `internal/provider/resource_foo_test.go` (see guide: `references/guides/testing.md`)

---

## Adding a Data Source

The provider does not define any data sources yet (`DataSources()` returns an empty slice). The shape below is illustrative, following the same lookup pattern `findUsergroup` already uses with `GetUserGroupsContext`:

```go
var _ datasource.DataSource = &usergroupDataSource{}

func newUsergroupDataSource() datasource.DataSource { return &usergroupDataSource{} }

type usergroupDataSource struct {
    client *apiClient
}

type usergroupDataSourceModel struct {
    Id     types.String `tfsdk:"id"`
    Handle types.String `tfsdk:"handle"`
}

func (d *usergroupDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
    resp.TypeName = req.ProviderTypeName + "_usergroup"
}

func (d *usergroupDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
    resp.Schema = schema.Schema{
        Attributes: map[string]schema.Attribute{
            "id":     schema.StringAttribute{Computed: true},
            "handle": schema.StringAttribute{Required: true},
        },
    }
}

func (d *usergroupDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
    if req.ProviderData == nil {
        return
    }
    client, ok := req.ProviderData.(*apiClient)
    if !ok {
        resp.Diagnostics.AddError("Unexpected DataSource Configure Type", fmt.Sprintf("Expected *apiClient, got: %T", req.ProviderData))
        return
    }
    d.client = client
}

func (d *usergroupDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
    var data usergroupDataSourceModel
    resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
    if resp.Diagnostics.HasError() {
        return
    }
    // API call, populate data fields...
    resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}
```

Register: add `newUsergroupDataSource` to `DataSources()` in `provider.go`.

---

## Schema Design Quick-Reference

| Schema Type                                                                                    | Go Model Type            | When to Use                    |
| ---------------------------------------------------------------------------------------------- | ------------------------ | ------------------------------- |
| `schema.StringAttribute{Required: true}`                                                       | `types.String`           | User must provide              |
| `schema.StringAttribute{Optional: true}`                                                       | `types.String`           | User may provide               |
| `schema.StringAttribute{Computed: true}`                                                       | `types.String`           | Server-generated only          |
| `schema.StringAttribute{Optional: true, Computed: true}`                                       | `types.String`           | User provides OR server fills  |
| `schema.SetAttribute{Optional: true, Computed: true, ElementType: types.StringType}`            | `types.Set`              | Set the server fills when unset (`slack_usergroup.users`) |
| `schema.BoolAttribute{Optional: true, Computed: true, Default: booldefault.StaticBool(false)}`    | `types.Bool`          | Attribute with a known default (`slack_conversation.is_private`) |

### Plan Modifiers

| Modifier                                  | Use Case                                   |
| ------------------------------------------ | ------------------------------------------ |
| `stringplanmodifier.UseStateForUnknown()` | Computed value stable across updates (`rsId()`, `created`, unset `topic`) |
| `boolplanmodifier.RequiresReplace()`      | Changing this forces resource recreation (`is_private` on `slack_conversation`) |
| `setplanmodifier.UseStateForUnknown()`    | Same idea, for a Set attribute (`channels`, `users`) |

Full details: `references/guides/schema-design.md`

---

## Testing Patterns

### Test Infrastructure

```go
var testAccProtoV6ProviderFactories = map[string]func() (tfprotov6.ProviderServer, error){
    "slack": providerserver.NewProtocol6WithError(New("test")()),
}

const testProviderConfig = `
provider "slack" {
  token = "xoxb-unused"
}
`
```

### Test Structure

```go
func TestAccConversation_Basic(t *testing.T) {
    fake := newFakeSlack()
    server := setupTestServer(t, fake)
    setupTestClient(t, server)

    resource.Test(t, resource.TestCase{
        ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
        Steps: []resource.TestStep{
            {
                Config: testProviderConfig + `
resource "slack_conversation" "test" {
  name  = "eng-platform"
  topic = "Platform engineering"
}
`,
                Check: resource.ComposeAggregateTestCheckFunc(
                    resource.TestCheckResourceAttrSet("slack_conversation.test", "id"),
                    resource.TestCheckResourceAttr("slack_conversation.test", "is_private", "false"),
                ),
            },
        },
    })
}
```

### Running Tests

```bash
go test ./internal/provider/ -v -run TestAcc
go test ./internal/provider/ -v -run TestAccConversation
```

Full details: `references/guides/testing.md`

---

## State Upgrade

Neither resource has needed one yet (`Schema` sets no `Version`, so it defaults to 0, and no resource implements `resource.ResourceWithUpgradeState`). If a future breaking schema change requires one (e.g., changing `users` from a Set to a nested block):

1. Increment `Version` in the schema
2. Implement `resource.ResourceWithUpgradeState`
3. Parse raw JSON state and write to current model

```go
func (r *fooResource) UpgradeState(_ context.Context) map[int64]resource.StateUpgrader {
    return map[int64]resource.StateUpgrader{
        0: {
            StateUpgrader: func(ctx context.Context, req resource.UpgradeStateRequest, resp *resource.UpgradeStateResponse) {
                var raw map[string]json.RawMessage
                if err := json.Unmarshal(req.RawState.JSON, &raw); err != nil {
                    resp.Diagnostics.AddError("State Upgrade Error", fmt.Sprintf("Unable to parse raw state: %s", err))
                    return
                }
                // Parse old format, build new model, set state
                resp.Diagnostics.Append(resp.State.Set(ctx, &newModel)...)
            },
        },
    }
}
```

Full details: `references/guides/state-management.md`

---

## Reference Docs

### Topic Guides (synthesized, task-oriented)

| Guide                                         | Contents                                              |
| ---------------------------------------------- | ------------------------------------------------------ |
| `references/guides/resource-lifecycle.md`     | CRUD methods, interface contracts, registration       |
| `references/guides/data-source-lifecycle.md`  | Data source pattern, Read method                      |
| `references/guides/schema-design.md`          | Attributes, blocks, types, nested models              |
| `references/guides/plan-modification.md`      | UseStateForUnknown, RequiresReplace, custom modifiers |
| `references/guides/state-management.md`       | Import, state upgrade, private state                  |
| `references/guides/validation.md`             | Attribute validators, resource-level validation       |
| `references/guides/testing.md`                | Acceptance tests, fake Slack server, test steps  |
| `references/guides/provider-configuration.md` | Provider setup, client injection, servers             |
| `references/guides/functions.md`              | Provider-defined functions (Terraform 1.8+)           |

### Framework Reference (verbatim, upstream HashiCorp docs)

Key entry points in `references/framework/`:

| File                                 | Contents                         |
| ------------------------------------- | --------------------------------- |
| `resources/index.mdx`                | Resource interface, registration |
| `resources/create.mdx`               | Create method contract           |
| `resources/read.mdx`                 | Read method, refresh state       |
| `resources/update.mdx`               | Update method, in-place changes  |
| `resources/delete.mdx`               | Delete method                    |
| `resources/configure.mdx`            | Client injection into resources  |
| `resources/import.mdx`               | Import state support             |
| `resources/plan-modification.mdx`    | Plan modifiers                   |
| `resources/state-upgrade.mdx`        | State upgrade for schema changes |
| `data-sources/index.mdx`             | Data source interface            |
| `handling-data/schemas.mdx`          | Schema definition                |
| `handling-data/accessing-values.mdx` | Reading config/plan/state        |
| `handling-data/writing-state.mdx`    | Writing to response state        |
| `handling-data/attributes/index.mdx` | All attribute types              |
| `handling-data/blocks/index.mdx`     | All block types                  |
| `handling-data/types/index.mdx`      | Type system (Go value types)     |
| `validation.mdx`                     | Validation patterns              |
| `diagnostics.mdx`                    | Error/warning diagnostics        |
| `acctests.mdx`                       | Acceptance testing setup         |
| `debugging.mdx`                      | Debugging providers              |
| `providers/index.mdx`                | Provider interface               |
| `provider-servers.mdx`               | Provider server (main.go)        |
| `functions/implementation.mdx`       | Provider functions               |
| `migrating/index.mdx`                | SDKv2 migration overview         |
