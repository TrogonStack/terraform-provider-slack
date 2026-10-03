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

Two attributes, `token` and `app_configuration_token`, both Optional and Sensitive. Each resource needs only one of them:

```go
"token": schema.StringAttribute{
    Optional:            true,
    Sensitive:           true,
    MarkdownDescription: "The Slack bot token, e.g. `xoxb-...`. Needed by `slack_conversation` and `slack_usergroup`.",
},
"app_configuration_token": schema.StringAttribute{
    Optional:            true,
    Sensitive:           true,
    MarkdownDescription: "The Slack app configuration token. Needed by `slack_app`. It expires after 12 hours and the provider never rotates it.",
},
```

The provider-level `MarkdownDescription` carries the scope table and the rotation guidance: the pipeline calls `tooling.tokens.rotate` before `terraform plan`, because each rotation returns a new refresh token and a refresh during plan is never saved.

Unlike a provider configuring a self-hosted service, there is no `url`/`endpoint`/`host` attribute: the Slack Web API has one fixed base URL (`slack.APIURL`).

### Provider Model

```go
type slackProviderModel struct {
    Token                 types.String `tfsdk:"token"`
    AppConfigurationToken types.String `tfsdk:"app_configuration_token"`
}
```

### Configure

```go
client := &apiClient{}

token := data.Token.ValueString()
if token == "" {
    token = os.Getenv("SLACK_TOKEN")
}
if token != "" {
    client.slack = slack.New(token, slack.OptionHTTPClient(newRetryableClient()))
}

appConfigurationToken := data.AppConfigurationToken.ValueString()
if appConfigurationToken == "" {
    appConfigurationToken = os.Getenv("SLACK_APP_CONFIGURATION_TOKEN")
}
if appConfigurationToken != "" {
    client.apps = newAppsClient(appConfigurationToken, newRetryableClient(), slack.APIURL)
}

if client.slack == nil && client.apps == nil {
    resp.Diagnostics.AddError("Configuration Error", "token or app_configuration_token must be set, ...")
    return
}
```

A `testAPIClient != nil` branch runs before this and hands the injected client straight to resources (see `references/guides/testing.md`).

### Client Structure

```go
type apiClient struct {
    slack *slack.Client
    apps  *appsClient
}
```

`slack` serves `slack_conversation` and `slack_usergroup`. `apps` (`apps_client.go`) serves `slack_app`: it posts `apps.manifest.*` itself with the manifest as raw JSON, because `slack-go` decodes manifests into a fixed struct that drops fields it does not model, and its create response has no `app_id` or credentials. Its `ok: false` errors come back as `slack.SlackErrorResponse`, so `hasSlackError` works on both clients.

Either field is nil when its token is unset. Each resource checks the one it needs in `Configure`:

```go
client.requireAppConfigurationToken(&resp.Diagnostics, "slack_app")
r.client = client
```

`requireBotToken` is the same check for `slack_conversation` and `slack_usergroup`.

### Client Data Flow

```
provider "slack" { token = "...", app_configuration_token = "..." }
          |
          v
   Configure()
          |
          v
   apiClient{ slack: *slack.Client, apps: *appsClient }
          |
    (DataSourceData / ResourceData)
          |
          v
  appResource.Configure()          -> requireAppConfigurationToken
  conversationResource.Configure() -> requireBotToken
  usergroupResource.Configure()    -> requireBotToken
          |
          v
  r.client.apps.createManifest(ctx, ...)
  r.client.slack.CreateConversationContext(ctx, ...)
```

### Resource and Data Source Registration

```go
func (p *slackProvider) Resources(ctx context.Context) []func() resource.Resource {
    return []func() resource.Resource{
        newApp,
        newConversation,
        newUsergroup,
    }
}
```

`DataSources` returns an empty slice today; see `references/guides/data-source-lifecycle.md` for the illustrative shape a future data source would follow.

## Resource/Data Source Configure Pattern

Every resource receives the client through its own `Configure` method, and checks the credential it needs before keeping it:

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
    client.requireBotToken(&resp.Diagnostics, "slack_conversation")
    r.client = client
}
```

`req.ProviderData == nil` happens during the provider's own schema/metadata validation passes, before `Configure` has run; returning early (rather than erroring) is correct here, since those passes don't call Create/Read/Update/Delete.

## Environment Variable Fallbacks

| Config Attribute          | Environment Variable            | Attribute Type             |
| ------------------------- | ------------------------------- | -------------------------- |
| `token`                   | `SLACK_TOKEN`                   | Sensitive string, Optional |
| `app_configuration_token` | `SLACK_APP_CONFIGURATION_TOKEN` | Sensitive string, Optional |

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
