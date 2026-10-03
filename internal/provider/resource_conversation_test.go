package provider

import (
	"fmt"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

func checkConversationArchived(fake *fakeSlack) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		fake.mu.Lock()
		defer fake.mu.Unlock()
		for id, c := range fake.conversations {
			if !c.IsArchived {
				return fmt.Errorf("expected conversation %s to be archived on destroy", id)
			}
		}
		return nil
	}
}

func TestAccConversation_Basic(t *testing.T) {
	fake := newFakeSlack()
	server := setupTestServer(t, fake)
	setupTestClient(t, server)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             checkConversationArchived(fake),
		Steps: []resource.TestStep{
			{
				Config: testProviderConfig + `
resource "slack_conversation" "test" {
  name    = "eng-platform"
  topic   = "Platform engineering"
  purpose = "Talk about the platform"
}
`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("slack_conversation.test", "id"),
					resource.TestCheckResourceAttr("slack_conversation.test", "name", "eng-platform"),
					resource.TestCheckResourceAttr("slack_conversation.test", "is_private", "false"),
					resource.TestCheckResourceAttr("slack_conversation.test", "topic", "Platform engineering"),
					resource.TestCheckResourceAttr("slack_conversation.test", "purpose", "Talk about the platform"),
					resource.TestCheckResourceAttr("slack_conversation.test", "is_archived", "false"),
					resource.TestCheckResourceAttr("slack_conversation.test", "created", "2023-11-14T22:13:20Z"),
				),
			},
			{
				ResourceName:      "slack_conversation.test",
				ImportState:       true,
				ImportStateVerify: true,
			},
			{
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction("slack_conversation.test", plancheck.ResourceActionUpdate),
					},
				},
				Config: testProviderConfig + `
resource "slack_conversation" "test" {
  name    = "eng-platform-renamed"
  topic   = "New topic"
  purpose = "Talk about the platform"
}
`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("slack_conversation.test", "name", "eng-platform-renamed"),
					resource.TestCheckResourceAttr("slack_conversation.test", "topic", "New topic"),
				),
			},
			{
				Config: testProviderConfig + `
resource "slack_conversation" "test" {
  name        = "eng-platform-renamed"
  topic       = "New topic"
  purpose     = "Talk about the platform"
  is_archived = true
}
`,
				Check: resource.TestCheckResourceAttr("slack_conversation.test", "is_archived", "true"),
			},
			{
				Config: testProviderConfig + `
resource "slack_conversation" "test" {
  name    = "eng-platform-renamed"
  topic   = "Back again"
  purpose = "Talk about the platform"
}
`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("slack_conversation.test", "is_archived", "false"),
					resource.TestCheckResourceAttr("slack_conversation.test", "topic", "Back again"),
				),
			},
		},
	})
}

func TestAccConversation_Private(t *testing.T) {
	fake := newFakeSlack()
	server := setupTestServer(t, fake)
	setupTestClient(t, server)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             checkConversationArchived(fake),
		Steps: []resource.TestStep{
			{
				Config: testProviderConfig + `
resource "slack_conversation" "test" {
  name       = "secret-project"
  is_private = true
}
`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("slack_conversation.test", "is_private", "true"),
					resource.TestCheckResourceAttr("slack_conversation.test", "topic", ""),
					resource.TestCheckResourceAttr("slack_conversation.test", "purpose", ""),
				),
			},
			{
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction("slack_conversation.test", plancheck.ResourceActionReplace),
					},
				},
				Config: testProviderConfig + `
resource "slack_conversation" "test" {
  name       = "public-project"
  is_private = false
}
`,
				Check: resource.TestCheckResourceAttr("slack_conversation.test", "is_private", "false"),
			},
		},
	})
}

func TestAccConversation_UnmanagedTopicIsKept(t *testing.T) {
	fake := newFakeSlack()
	server := setupTestServer(t, fake)
	setupTestClient(t, server)

	config := testProviderConfig + `
resource "slack_conversation" "test" {
  name = "random"
}
`

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
					for _, c := range fake.conversations {
						c.Topic.Value = "set in Slack"
					}
				},
				Config: config,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectEmptyPlan(),
					},
				},
				Check: resource.TestCheckResourceAttr("slack_conversation.test", "topic", "set in Slack"),
			},
		},
	})
}

func TestAccConversation_ArchivedExternally(t *testing.T) {
	fake := newFakeSlack()
	server := setupTestServer(t, fake)
	setupTestClient(t, server)

	config := testProviderConfig + `
resource "slack_conversation" "test" {
  name = "general-chat"
}
`

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
					for _, c := range fake.conversations {
						c.IsArchived = true
					}
				},
				Config: config,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction("slack_conversation.test", plancheck.ResourceActionUpdate),
					},
				},
				Check: resource.TestCheckResourceAttr("slack_conversation.test", "is_archived", "false"),
			},
		},
	})
}

func TestAccConversation_DeletedExternally(t *testing.T) {
	fake := newFakeSlack()
	server := setupTestServer(t, fake)
	setupTestClient(t, server)

	config := testProviderConfig + `
resource "slack_conversation" "test" {
  name = "ephemeral"
}
`

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
					for id := range fake.conversations {
						delete(fake.conversations, id)
					}
				},
				Config: config,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction("slack_conversation.test", plancheck.ResourceActionCreate),
					},
				},
				Check: resource.TestCheckResourceAttrSet("slack_conversation.test", "id"),
			},
		},
	})
}
