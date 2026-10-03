package provider

import (
	"context"
	"fmt"
	"os"
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
