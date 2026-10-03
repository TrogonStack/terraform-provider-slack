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
	Token                 types.String `tfsdk:"token"`
	AppConfigurationToken types.String `tfsdk:"app_configuration_token"`
}

func (p *slackProvider) Metadata(ctx context.Context, req provider.MetadataRequest, resp *provider.MetadataResponse) {
	resp.TypeName = "slack"
	resp.Version = p.version
}

func (p *slackProvider) Schema(ctx context.Context, req provider.SchemaRequest, resp *provider.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: `Manage Slack apps, conversations and user groups with Terraform.

The provider covers the Slack Web API: creating apps from their manifest,
creating, renaming, archiving and unarchiving channels, and managing user
groups with their default channels and members.

## Authentication

The provider takes two credentials. Each resource needs only one of them, so
set the one the configuration uses.

` + "`token`" + ` is a Slack bot token (` + "`xoxb-`" + `), used by ` + "`slack_conversation`" + ` and
` + "`slack_usergroup`" + `. It comes from installing a Slack app with the bot token
scopes below. ` + "`slack_app`" + ` can create that app from a manifest, and a workspace
admin then installs it once through its ` + "`oauth_authorize_url`" + `.

| Scope              | Needed for                                  |
| ------------------ | ------------------------------------------- |
| ` + "`channels:manage`" + `  | creating and changing public channels       |
| ` + "`channels:read`" + `    | reading public channels                     |
| ` + "`groups:write`" + `     | creating and changing private channels      |
| ` + "`groups:read`" + `      | reading private channels                    |
| ` + "`usergroups:write`" + ` | creating and changing user groups           |
| ` + "`usergroups:read`" + `  | reading user groups and their members       |

` + "`app_configuration_token`" + ` is an app configuration token, used by
` + "`slack_app`" + `. Slack issues it per workspace at https://api.slack.com/apps, and it
expires after 12 hours. The provider never rotates it. Rotate it with
` + "`tooling.tokens.rotate`" + ` in the pipeline before ` + "`terraform plan`" + `, and store the new
refresh token there. Each rotation returns a new refresh token, and Terraform
has nowhere safe to keep it: a refresh during plan is never saved.

## Environment variables

| Attribute                 | Environment variable            |
| ------------------------- | ------------------------------- |
| ` + "`token`" + `                   | ` + "`SLACK_TOKEN`" + `                   |
| ` + "`app_configuration_token`" + ` | ` + "`SLACK_APP_CONFIGURATION_TOKEN`" + ` |`,
		Attributes: map[string]schema.Attribute{
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
		resp.Diagnostics.AddError("Configuration Error", "token or app_configuration_token must be set, either in the provider configuration or the SLACK_TOKEN and SLACK_APP_CONFIGURATION_TOKEN environment variables")
		return
	}

	resp.DataSourceData = client
	resp.ResourceData = client
}

func (p *slackProvider) Resources(ctx context.Context) []func() resource.Resource {
	return []func() resource.Resource{
		newApp,
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
