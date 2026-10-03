# Provider Configuration

## Overview

The provider block configures shared state (API clients, credentials) passed to every resource and data source via `Configure`.

## Provider Interface

```go
type Provider interface {
    Metadata(context.Context, MetadataRequest, *MetadataResponse)
    Schema(context.Context, SchemaRequest, *SchemaResponse)
    Configure(context.Context, ConfigureRequest, *ConfigureResponse)
    Resources(context.Context) []func() resource.Resource
    DataSources(context.Context) []func() datasource.DataSource
}
```

## This Provider's Structure

### Metadata

```go
func (p *slackProvider) Metadata(_ context.Context, _ provider.MetadataRequest, resp *provider.MetadataResponse) {
    resp.TypeName = "slack"
    resp.Version = p.version
}
```

### Schema

A single attribute, `token`, Optional and Sensitive:

```go
func (p *slackProvider) Schema(_ context.Context, _ provider.SchemaRequest, resp *provider.SchemaResponse) {
    resp.Schema = schema.Schema{
        Attributes: map[string]schema.Attribute{
            "token": schema.StringAttribute{
                Optional:  true,
                Sensitive: true,
                MarkdownDescription: "Slack API token (bot or user token). Falls back to the " +
                    "`SLACK_TOKEN` environment variable. Required OAuth scopes: " +
                    "`channels:manage`, `channels:read`, `groups:write`, `groups:read`, " +
                    "`usergroups:write`, `usergroups:read`.",
            },
        },
    }
}
```

Unlike a provider configuring a self-hosted service, there is no `url`/`endpoint`/`host` attribute: the Slack Web API has one fixed base URL, and the `slack-go/slack` client defaults to it. The only thing this provider's config needs is which workspace/token to authenticate with.

### Provider Model

```go
type slackProviderModel struct {
    Token types.String `tfsdk:"token"`
}
```

### Configure

```go
func (p *slackProvider) Configure(ctx context.Context, req provider.ConfigureRequest, resp *provider.ConfigureResponse) {
    if testAPIClient != nil {
        resp.DataSourceData = testAPIClient
        resp.ResourceData = testAPIClient
        return
    }

    var config slackProviderModel
    resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
    if resp.Diagnostics.HasError() {
        return
    }

    token := config.Token.ValueString()
    if token == "" {
        token = os.Getenv("SLACK_TOKEN")
    }
    if token == "" {
        resp.Diagnostics.AddError(
            "Missing Slack Token",
            "The provider requires a token, set via the `token` attribute or the SLACK_TOKEN environment variable.",
        )
        return
    }

    client := &apiClient{
        slack: slack.New(token, slack.OptionHTTPClient(newRetryableClient())),
    }
    resp.DataSourceData = client
    resp.ResourceData = client
}
```

The `testAPIClient != nil` branch at the top is the package-level test bypass used throughout `references/guides/testing.md`'s `setupTestServer`: it lets acceptance tests point the whole provider at `fakeSlack` without going through environment variables or real credentials at all.

### Client Structure

```go
type apiClient struct {
    slack *slack.Client
}
```

Deliberately thin: one field, the `slack-go/slack` client itself. There is no custom HTTP wrapper type, no separate auth-token field stored alongside it (the token is already captured inside the `slack.Client`), and no additional per-environment configuration.

### Client Data Flow

```
provider "slack" { token = "..." }
          |
          v
slackProviderModel{ Token: "..." }
          |
          v
   Configure()
          |
          v
   apiClient{ slack: *slack.Client }
          |
    (DataSourceData / ResourceData)
          |
          v
  conversationResource.Configure()
  usergroupResource.Configure()
          |
          v
  r.client.slack.CreateConversationContext(ctx, ...)
  r.client.slack.CreateUserGroupContext(ctx, ...)
```

### Resource and Data Source Registration

```go
func (p *slackProvider) Resources(_ context.Context) []func() resource.Resource {
    return []func() resource.Resource{
        newConversationResource,
        newUsergroupResource,
    }
}

func (p *slackProvider) DataSources(_ context.Context) []func() datasource.DataSource {
    return []func() datasource.DataSource{}
}
```

`DataSources` returns an empty slice today; see `references/guides/data-source-lifecycle.md` for the illustrative shape a future data source would follow.

### Constructor

```go
func New(version string) func() provider.Provider {
    return func() provider.Provider {
        return &slackProvider{version: version}
    }
}
```

## Resource/Data Source Configure Pattern

Every resource and data source receives the client through its own `Configure` method:

```go
func (r *conversationResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

`req.ProviderData == nil` happens during the provider's own schema/metadata validation passes, before `Configure` has run; returning early (rather than erroring) is correct here, since those passes don't call Create/Read/Update/Delete.

## Environment Variable Fallbacks

| Config Attribute | Environment Variable | Attribute Type       |
| ------------------- | ------------------------ | ----------------------- |
| `token`           | `SLACK_TOKEN`         | Sensitive string, Optional |

Unlike a provider with several independently-overridable settings, this provider has exactly one configurable value, and exactly one fallback path for it.

## Provider Server (main.go)

```go
package main

import (
    "context"
    "log"

    "github.com/hashicorp/terraform-plugin-framework/providerserver"

    "github.com/TrogonStack/terraform-provider-slack/internal/provider"
)

var version = "dev"

func main() {
    err := providerserver.Serve(context.Background(), provider.New(version), providerserver.ServeOpts{
        Address: "registry.terraform.io/trogonstack/slack",
    })
    if err != nil {
        log.Fatal(err)
    }
}
```

`version` is overridden at build time via `-ldflags "-X main.version=..."` in release builds; `New(version)` threads it into `slackProviderModel`'s `resp.Version` in `Metadata`. The module path is `github.com/TrogonStack/terraform-provider-slack` (`go.mod`); the registry address is `registry.terraform.io/trogonstack/slack`.

## Related Framework References

| File                                        | Contents                          |
| ---------------------------------------------- | -------------------------------------- |
| `framework/providers/index.mdx`             | Provider interface                |
| `framework/providers/configure.mdx`         | Provider Configure method         |
| `framework/providers/validate-configuration.mdx` | Provider-level ValidateConfig |
| `framework/handling-data/schemas.mdx`       | Provider schema                   |
