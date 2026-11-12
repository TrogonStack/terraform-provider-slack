package provider

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/acctest"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	"github.com/slack-go/slack"
)

const liveProviderConfig = `
provider "slack" {}
`

func requireLiveCredentials(t *testing.T) {
	t.Helper()
	if os.Getenv("TF_ACC") == "" {
		t.Skip("set TF_ACC=1 to run live acceptance tests against a real Slack workspace")
	}
	if os.Getenv("SLACK_TOKEN") == "" {
		t.Skip("set SLACK_TOKEN to run live acceptance tests against a real Slack workspace")
	}
}

func requireLiveAppCredentials(t *testing.T) {
	t.Helper()
	if os.Getenv("TF_ACC") == "" {
		t.Skip("set TF_ACC=1 to run live acceptance tests against a real Slack workspace")
	}
	if os.Getenv("SLACK_APP_CONFIGURATION_TOKEN") == "" {
		t.Skip("set SLACK_APP_CONFIGURATION_TOKEN to run live acceptance tests that manage Slack apps")
	}
}

func liveSlackClient() *slack.Client {
	return slack.New(os.Getenv("SLACK_TOKEN"))
}

func TestLive_Conversation(t *testing.T) {
	requireLiveCredentials(t)

	client := liveSlackClient()
	name := "tf-live-" + acctest.RandString(8)
	var conversationId string

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy: func(_ *terraform.State) error {
			return checkLiveConversationArchived(client, conversationId)
		},
		Steps: []resource.TestStep{
			{
				Config: liveProviderConfig + fmt.Sprintf(`
resource "slack_conversation" "test" {
  name    = %q
  topic   = "Terraform live test"
  purpose = "Created by the terraform-provider-slack live tests"
}
`, name),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("slack_conversation.test", "id"),
					resource.TestCheckResourceAttr("slack_conversation.test", "name", name),
					resource.TestCheckResourceAttr("slack_conversation.test", "topic", "Terraform live test"),
					resource.TestCheckResourceAttrSet("slack_conversation.test", "created"),
					func(s *terraform.State) error {
						conversationId = s.RootModule().Resources["slack_conversation.test"].Primary.ID
						return nil
					},
				),
			},
			{
				ResourceName:      "slack_conversation.test",
				ImportState:       true,
				ImportStateVerify: true,
			},
			{
				Config: liveProviderConfig + fmt.Sprintf(`
resource "slack_conversation" "test" {
  name    = "%s-renamed"
  topic   = "Terraform live test, renamed"
  purpose = "Created by the terraform-provider-slack live tests"
}
`, name),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("slack_conversation.test", "name", name+"-renamed"),
					resource.TestCheckResourceAttr("slack_conversation.test", "topic", "Terraform live test, renamed"),
				),
			},
		},
	})
}

func TestLive_Usergroup(t *testing.T) {
	requireLiveCredentials(t)

	client := liveSlackClient()
	suffix := acctest.RandString(8)
	var usergroupId string

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy: func(_ *terraform.State) error {
			return checkLiveUsergroupDisabled(client, usergroupId)
		},
		Steps: []resource.TestStep{
			{
				Config: liveProviderConfig + fmt.Sprintf(`
resource "slack_usergroup" "test" {
  name        = "TF Live %[1]s"
  handle      = "tf-live-%[1]s"
  description = "Created by the terraform-provider-slack live tests"
}
`, suffix),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("slack_usergroup.test", "id"),
					resource.TestCheckResourceAttr("slack_usergroup.test", "handle", "tf-live-"+suffix),
					func(s *terraform.State) error {
						usergroupId = s.RootModule().Resources["slack_usergroup.test"].Primary.ID
						return nil
					},
				),
			},
			{
				ResourceName:      "slack_usergroup.test",
				ImportState:       true,
				ImportStateVerify: true,
			},
			{
				Config: liveProviderConfig + fmt.Sprintf(`
resource "slack_usergroup" "test" {
  name        = "TF Live %[1]s renamed"
  handle      = "tf-live-%[1]s"
  description = "Updated by the terraform-provider-slack live tests"
}
`, suffix),
				Check: resource.TestCheckResourceAttr("slack_usergroup.test", "name", "TF Live "+suffix+" renamed"),
			},
		},
	})
}

func TestLive_App(t *testing.T) {
	requireLiveAppCredentials(t)

	client := newAppsClient(os.Getenv("SLACK_APP_CONFIGURATION_TOKEN"), newRetryableClient(), slack.APIURL)
	name := "TF Live " + acctest.RandString(8)
	var appId string

	config := func(description string) string {
		return liveProviderConfig + fmt.Sprintf(`
resource "slack_app" "test" {
  manifest = jsonencode({
    display_information = {
      name        = %q
      description = %q
    }
    features = {
      bot_user = {
        display_name = "tf-live"
      }
    }
    oauth_config = {
      scopes = {
        bot = ["channels:read"]
      }
    }
  })
}
`, name, description)
	}

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy: func(_ *terraform.State) error {
			return checkLiveAppDeleted(client, appId)
		},
		Steps: []resource.TestStep{
			{
				Config: config("Created by the terraform-provider-slack live tests"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("slack_app.test", "id"),
					resource.TestCheckResourceAttrSet("slack_app.test", "client_id"),
					resource.TestCheckResourceAttrSet("slack_app.test", "signing_secret"),
					resource.TestCheckResourceAttrSet("slack_app.test", "oauth_authorize_url"),
					func(s *terraform.State) error {
						appId = s.RootModule().Resources["slack_app.test"].Primary.ID
						return nil
					},
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
				Config: config("Updated by the terraform-provider-slack live tests"),
				Check:  resource.TestCheckResourceAttrSet("slack_app.test", "client_secret"),
			},
		},
	})
}

// TestLive_AppIcon needs SLACK_LIVE_ICON_APP_ID, an app the token may change.
// Slack cannot read or remove an icon, so the test only proves the upload.
func TestLive_AppIcon(t *testing.T) {
	requireLiveAppCredentials(t)
	appID := os.Getenv("SLACK_LIVE_ICON_APP_ID")
	if appID == "" {
		t.Skip("set SLACK_LIVE_ICON_APP_ID to an app ID whose icon the live test may replace")
	}

	icon := filepath.Join(t.TempDir(), "icon.png")
	img := image.NewGray(image.Rect(0, 0, 512, 512))
	for i := range img.Pix {
		img.Pix[i] = 128
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(icon, buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: liveProviderConfig + fmt.Sprintf(`
resource "slack_app_icon" "test" {
  app_id = %q
  file   = %q
  sha256 = filesha256(%q)
}
`, appID, icon, icon),
				Check: resource.TestCheckResourceAttr("slack_app_icon.test", "id", appID),
			},
		},
	})
}

func checkLiveAppDeleted(client *appsClient, appId string) error {
	if appId == "" {
		return fmt.Errorf("no app ID was captured to verify destruction")
	}
	_, err := client.exportManifest(context.Background(), appId)
	if hasSlackError(err, errAppNotFound, errInvalidAppID) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("failed to export app while verifying destroy of %s: %w", appId, err)
	}
	return fmt.Errorf("expected app %s to be deleted, but it still exists", appId)
}

func checkLiveConversationArchived(client *slack.Client, conversationId string) error {
	if conversationId == "" {
		return fmt.Errorf("no conversation ID was captured to verify destruction")
	}
	info, err := client.GetConversationInfoContext(context.Background(), &slack.GetConversationInfoInput{ChannelID: conversationId})
	if err != nil {
		return fmt.Errorf("failed to read conversation while verifying destroy of %s: %w", conversationId, err)
	}
	if !info.IsArchived {
		return fmt.Errorf("expected conversation %s to be archived, but it is still active", conversationId)
	}
	return nil
}

func checkLiveUsergroupDisabled(client *slack.Client, usergroupId string) error {
	if usergroupId == "" {
		return fmt.Errorf("no user group ID was captured to verify destruction")
	}
	groups, err := client.GetUserGroupsContext(context.Background(), slack.GetUserGroupsOptionIncludeDisabled(true))
	if err != nil {
		return fmt.Errorf("failed to list user groups while verifying destroy of %s: %w", usergroupId, err)
	}
	for _, g := range groups {
		if g.ID == usergroupId && g.DateDelete == 0 {
			return fmt.Errorf("expected user group %s to be disabled, but it is still enabled", usergroupId)
		}
	}
	return nil
}
