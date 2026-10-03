package provider

import (
	"encoding/json"
	"fmt"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	"github.com/slack-go/slack"
)

func checkAppsDeleted(fake *fakeSlack) resource.TestCheckFunc {
	return func(_ *terraform.State) error {
		fake.mu.Lock()
		defer fake.mu.Unlock()
		for id := range fake.apps {
			return fmt.Errorf("expected app %s to be deleted on destroy", id)
		}
		return nil
	}
}

func appConfig(name, description string) string {
	return testProviderConfig + fmt.Sprintf(`
resource "slack_app" "test" {
  manifest = jsonencode({
    display_information = {
      name        = %q
      description = %q
    }
    features = {
      bot_user = {
        display_name = "terraform"
      }
    }
    oauth_config = {
      scopes = {
        bot = ["channels:manage", "channels:read"]
      }
    }
    settings = {
      socket_mode_enabled = false
    }
  })
}
`, name, description)
}

func TestAccApp_Basic(t *testing.T) {
	fake := newFakeSlack()
	server := setupTestServer(t, fake)
	setupTestClient(t, server)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             checkAppsDeleted(fake),
		Steps: []resource.TestStep{
			{
				Config: appConfig("Workspace Bot", "Manages channels"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("slack_app.test", "id", "A0000000001"),
					resource.TestCheckResourceAttr("slack_app.test", "client_id", "1111.A0000000001"),
					resource.TestCheckResourceAttr("slack_app.test", "client_secret", "secret-A0000000001"),
					resource.TestCheckResourceAttr("slack_app.test", "signing_secret", "signing-A0000000001"),
					resource.TestCheckResourceAttr("slack_app.test", "verification_token", "verify-A0000000001"),
					resource.TestCheckResourceAttr("slack_app.test", "oauth_authorize_url", "https://slack.com/oauth/v2/authorize?client_id=1111.A0000000001"),
				),
			},
			{
				ResourceName:      "slack_app.test",
				ImportState:       true,
				ImportStateVerify: true,
				ImportStateVerifyIgnore: []string{
					"manifest",
					"client_id",
					"client_secret",
					"signing_secret",
					"verification_token",
					"oauth_authorize_url",
				},
			},
			{
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction("slack_app.test", plancheck.ResourceActionUpdate),
					},
				},
				Config: appConfig("Workspace Bot", "Manages channels and user groups"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("slack_app.test", "id", "A0000000001"),
					resource.TestCheckResourceAttr("slack_app.test", "client_secret", "secret-A0000000001"),
					func(_ *terraform.State) error {
						fake.mu.Lock()
						defer fake.mu.Unlock()
						info := fake.apps["A0000000001"]["display_information"].(map[string]any)
						if info["description"] != "Manages channels and user groups" {
							return fmt.Errorf("expected Slack to hold the updated description, got %v", info["description"])
						}
						return nil
					},
				),
			},
		},
	})
}

func TestAccApp_DriftIsDetected(t *testing.T) {
	fake := newFakeSlack()
	server := setupTestServer(t, fake)
	setupTestClient(t, server)

	config := appConfig("Workspace Bot", "Manages channels")

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: config,
			},
			{
				PreConfig: func() {
					fake.mu.Lock()
					defer fake.mu.Unlock()
					for _, manifest := range fake.apps {
						manifest["display_information"].(map[string]any)["description"] = "Changed in the Slack UI"
					}
				},
				Config: config,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction("slack_app.test", plancheck.ResourceActionUpdate),
					},
				},
			},
		},
	})
}

func TestAccApp_DeletedExternally(t *testing.T) {
	fake := newFakeSlack()
	server := setupTestServer(t, fake)
	setupTestClient(t, server)

	config := appConfig("Workspace Bot", "Manages channels")

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: config,
			},
			{
				PreConfig: func() {
					fake.mu.Lock()
					defer fake.mu.Unlock()
					clear(fake.apps)
				},
				Config: config,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction("slack_app.test", plancheck.ResourceActionCreate),
					},
				},
				Check: resource.TestCheckResourceAttr("slack_app.test", "id", "A0000000002"),
			},
		},
	})
}

func TestAccApp_InvalidManifest(t *testing.T) {
	fake := newFakeSlack()
	server := setupTestServer(t, fake)
	setupTestClient(t, server)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             checkAppsDeleted(fake),
		Steps: []resource.TestStep{
			{
				Config:      appConfig("", "No name"),
				ExpectError: regexp.MustCompile(`/display_information: must have required property 'name'`),
			},
			{
				Config: appConfig("Workspace Bot", "Manages channels"),
			},
			{
				Config:      appConfig("", "Manages channels"),
				ExpectError: regexp.MustCompile(`/display_information: must have required property 'name'`),
			},
		},
	})
}

func TestAccApp_MissingAppConfigurationToken(t *testing.T) {
	server := setupTestServer(t, newFakeSlack())
	setupTestClient(t, server)
	testAPIClient.apps = nil

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:      appConfig("Workspace Bot", "Manages channels"),
				ExpectError: regexp.MustCompile(`slack_app needs app_configuration_token`),
			},
		},
	})
}

func TestAccConversation_MissingBotToken(t *testing.T) {
	server := setupTestServer(t, newFakeSlack())
	setupTestClient(t, server)
	testAPIClient.slack = nil

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: testProviderConfig + `
resource "slack_conversation" "test" {
  name = "eng-platform"
}
`,
				ExpectError: regexp.MustCompile(`slack_conversation needs token`),
			},
			{
				Config: testProviderConfig + `
resource "slack_usergroup" "test" {
  name = "Platform"
}
`,
				ExpectError: regexp.MustCompile(`slack_usergroup needs token`),
			},
		},
	})
}

func TestAppsClientRejectsBotToken(t *testing.T) {
	server := setupTestServer(t, newFakeSlack())
	client := newAppsClient(testToken, server.Client(), server.URL+"/")

	_, err := client.exportManifest(t.Context(), "A0000000001")
	if !hasSlackError(err, "invalid_auth") {
		t.Fatalf("expected apps.manifest.export with a bot token to fail with invalid_auth, got: %v", err)
	}
}

func TestDescribeSlackErrorListsManifestErrors(t *testing.T) {
	err := manifestSlackError(appsResponse{
		Error:  "invalid_manifest",
		Errors: []manifestError{{Message: "must be a string", Pointer: "/display_information/name"}},
	})
	if got, want := describeSlackError(err), "invalid_manifest\n/display_information/name: must be a string"; got != want {
		t.Fatalf("describeSlackError() = %q, want %q", got, want)
	}
	if got := describeSlackError(slack.SlackErrorResponse{Err: "app_not_found"}); got != "app_not_found" {
		t.Fatalf("describeSlackError() without messages = %q, want app_not_found", got)
	}
}

func TestManifestCovers(t *testing.T) {
	exported := `{
		"display_information": {"name": "Bot", "description": "d", "background_color": "#000000"},
		"oauth_config": {"scopes": {"bot": ["channels:read", "channels:manage"]}},
		"settings": {"org_deploy_enabled": false, "socket_mode_enabled": false}
	}`

	cases := []struct {
		name  string
		state string
		want  bool
	}{
		{"identical", exported, true},
		{"subset", `{"display_information": {"name": "Bot"}}`, true},
		{"scopes in another order", `{"oauth_config": {"scopes": {"bot": ["channels:manage", "channels:read"]}}}`, true},
		{"empty object", `{}`, true},
		{"zero value missing from export", `{"settings": {"token_rotation_enabled": false, "event_subscriptions": {}}, "features": {"shortcuts": []}}`, true},
		{"changed scalar", `{"display_information": {"name": "Other"}}`, false},
		{"missing scope", `{"oauth_config": {"scopes": {"bot": ["channels:read"]}}}`, false},
		{"extra scope", `{"oauth_config": {"scopes": {"bot": ["channels:read", "channels:manage", "groups:read"]}}}`, false},
		{"set value missing from export", `{"settings": {"token_rotation_enabled": true}}`, false},
		{"type mismatch", `{"display_information": "Bot"}`, false},
	}

	var got any
	if err := json.Unmarshal([]byte(exported), &got); err != nil {
		t.Fatal(err)
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var want any
			if err := json.Unmarshal([]byte(tc.state), &want); err != nil {
				t.Fatal(err)
			}
			if covered := manifestCovers(want, got); covered != tc.want {
				t.Fatalf("manifestCovers(%s) = %v, want %v", tc.state, covered, tc.want)
			}
		})
	}
}
