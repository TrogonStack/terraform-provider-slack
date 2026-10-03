package provider

import (
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/slack-go/slack"
)

type apiClient struct {
	slack *slack.Client
	apps  *appsClient
}

func (c *apiClient) requireBotToken(diags *diag.Diagnostics, resourceType string) {
	if c.slack == nil {
		diags.AddError("Missing Credentials", resourceType+" needs token, set either in the provider configuration or the SLACK_TOKEN environment variable")
	}
}

func (c *apiClient) requireAppConfigurationToken(diags *diag.Diagnostics, resourceType string) {
	if c.apps == nil {
		diags.AddError("Missing Credentials", resourceType+" needs app_configuration_token, set either in the provider configuration or the SLACK_APP_CONFIGURATION_TOKEN environment variable")
	}
}
