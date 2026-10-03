package provider

import (
	"context"
	"os"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/slack-go/slack"
)

var _ provider.Provider = &slackProvider{}

// testAPIClient is set by tests to bypass authentication and inject a mock client.
var testAPIClient *apiClient

type slackProvider struct {
	version string
}

type slackProviderModel struct {
	Token types.String `tfsdk:"token"`
}

func (p *slackProvider) Metadata(ctx context.Context, req provider.MetadataRequest, resp *provider.MetadataResponse) {
	resp.TypeName = "slack"
	resp.Version = p.version
}

func (p *slackProvider) Schema(ctx context.Context, req provider.SchemaRequest, resp *provider.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: `Manage Slack conversations and user groups with Terraform.

The provider covers the Slack Web API: creating, renaming, archiving and
unarchiving channels, and managing user groups with their default channels
and members.

## Authentication

Every request carries ` + "`token`" + `, a Slack bot token (` + "`xoxb-`" + `). Create a Slack app,
add the bot token scopes below under OAuth & Permissions, install the app to
the workspace, and copy the Bot User OAuth Token.

| Scope              | Needed for                                  |
| ------------------ | ------------------------------------------- |
| ` + "`channels:manage`" + `  | creating and changing public channels       |
| ` + "`channels:read`" + `    | reading public channels                     |
| ` + "`groups:write`" + `     | creating and changing private channels      |
| ` + "`groups:read`" + `      | reading private channels                    |
| ` + "`usergroups:write`" + ` | creating and changing user groups           |
| ` + "`usergroups:read`" + `  | reading user groups and their members       |

## Environment variables

| Attribute | Environment variable |
| --------- | -------------------- |
| ` + "`token`" + `   | ` + "`SLACK_TOKEN`" + `        |`,
		Attributes: map[string]schema.Attribute{
			"token": schema.StringAttribute{
				Optional:            true,
				Sensitive:           true,
				MarkdownDescription: "The Slack bot token, e.g. `xoxb-...`.",
			},
		},
	}
}

func (p *slackProvider) Configure(ctx context.Context, req provider.ConfigureRequest, resp *provider.ConfigureResponse) {
	var data slackProviderModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// In tests, skip authentication and use the injected mock client.
	if testAPIClient != nil {
		resp.DataSourceData = testAPIClient
		resp.ResourceData = testAPIClient
		return
	}

	token := data.Token.ValueString()
	if token == "" {
		token = os.Getenv("SLACK_TOKEN")
	}
	if token == "" {
		resp.Diagnostics.AddError("Configuration Error", "token must be set, either in the provider configuration or the SLACK_TOKEN environment variable")
		return
	}

	client := &apiClient{slack: slack.New(token, slack.OptionHTTPClient(newRetryableClient()))}
	resp.DataSourceData = client
	resp.ResourceData = client
}

func (p *slackProvider) Resources(ctx context.Context) []func() resource.Resource {
	return []func() resource.Resource{
		newConversation,
		newUsergroup,
	}
}

func (p *slackProvider) DataSources(ctx context.Context) []func() datasource.DataSource {
	return []func() datasource.DataSource{}
}

func New(version string) func() provider.Provider {
	return func() provider.Provider {
		return &slackProvider{
			version: version,
		}
	}
}
