package provider

import (
	"fmt"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

func checkUsergroupsDisabled(fake *fakeSlack) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		fake.mu.Lock()
		defer fake.mu.Unlock()
		for id, g := range fake.usergroups {
			if g.DateDelete == 0 {
				return fmt.Errorf("expected user group %s to be disabled on destroy", id)
			}
		}
		return nil
	}
}

const usergroupConfig = `
resource "slack_usergroup" "test" {
  name        = "Platform"
  handle      = "platform"
  description = "Platform engineers"
  channels    = ["C0000000001"]
  users       = ["U0000000002", "U0000000001"]
}
`

func TestAccUsergroup_Basic(t *testing.T) {
	fake := newFakeSlack()
	server := setupTestServer(t, fake)
	setupTestClient(t, server)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             checkUsergroupsDisabled(fake),
		Steps: []resource.TestStep{
			{
				Config: testProviderConfig + usergroupConfig,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("slack_usergroup.test", "id"),
					resource.TestCheckResourceAttr("slack_usergroup.test", "name", "Platform"),
					resource.TestCheckResourceAttr("slack_usergroup.test", "handle", "platform"),
					resource.TestCheckResourceAttr("slack_usergroup.test", "description", "Platform engineers"),
					resource.TestCheckResourceAttr("slack_usergroup.test", "channels.#", "1"),
					resource.TestCheckTypeSetElemAttr("slack_usergroup.test", "channels.*", "C0000000001"),
					resource.TestCheckResourceAttr("slack_usergroup.test", "users.#", "2"),
					resource.TestCheckTypeSetElemAttr("slack_usergroup.test", "users.*", "U0000000001"),
					resource.TestCheckTypeSetElemAttr("slack_usergroup.test", "users.*", "U0000000002"),
				),
			},
			{
				ResourceName:      "slack_usergroup.test",
				ImportState:       true,
				ImportStateVerify: true,
			},
			{
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction("slack_usergroup.test", plancheck.ResourceActionUpdate),
					},
				},
				Config: testProviderConfig + `
resource "slack_usergroup" "test" {
  name        = "Platform Team"
  handle      = "platform-team"
  description = ""
  channels    = []
  users       = ["U0000000003"]
}
`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("slack_usergroup.test", "name", "Platform Team"),
					resource.TestCheckResourceAttr("slack_usergroup.test", "handle", "platform-team"),
					resource.TestCheckResourceAttr("slack_usergroup.test", "description", ""),
					resource.TestCheckResourceAttr("slack_usergroup.test", "channels.#", "0"),
					resource.TestCheckResourceAttr("slack_usergroup.test", "users.#", "1"),
					resource.TestCheckTypeSetElemAttr("slack_usergroup.test", "users.*", "U0000000003"),
				),
			},
		},
	})
}

func TestAccUsergroup_RecreateReenablesDisabled(t *testing.T) {
	fake := newFakeSlack()
	server := setupTestServer(t, fake)
	setupTestClient(t, server)

	var firstID string

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             checkUsergroupsDisabled(fake),
		Steps: []resource.TestStep{
			{
				Config: testProviderConfig + usergroupConfig,
				Check: func(s *terraform.State) error {
					firstID = s.RootModule().Resources["slack_usergroup.test"].Primary.ID
					return nil
				},
			},
			{
				Config: testProviderConfig,
				Check:  checkUsergroupsDisabled(fake),
			},
			{
				Config: testProviderConfig + usergroupConfig,
				Check: resource.ComposeAggregateTestCheckFunc(
					func(s *terraform.State) error {
						id := s.RootModule().Resources["slack_usergroup.test"].Primary.ID
						if id != firstID {
							return fmt.Errorf("expected the disabled user group %s to be enabled again, got %s", firstID, id)
						}
						fake.mu.Lock()
						defer fake.mu.Unlock()
						if fake.usergroups[id].DateDelete != 0 {
							return fmt.Errorf("expected user group %s to be enabled", id)
						}
						return nil
					},
					resource.TestCheckResourceAttr("slack_usergroup.test", "users.#", "2"),
				),
			},
		},
	})
}

func TestAccUsergroup_EnabledDuplicateFails(t *testing.T) {
	fake := newFakeSlack()
	server := setupTestServer(t, fake)
	setupTestClient(t, server)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: testProviderConfig + `
resource "slack_usergroup" "first" {
  name = "Oncall"
}

resource "slack_usergroup" "second" {
  name       = "Oncall"
  depends_on = [slack_usergroup.first]
}
`,
				ExpectError: regexp.MustCompile(`import it instead`),
			},
		},
	})
}

func TestAccUsergroup_DisabledExternally(t *testing.T) {
	fake := newFakeSlack()
	server := setupTestServer(t, fake)
	setupTestClient(t, server)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: testProviderConfig + usergroupConfig,
			},
			{
				PreConfig: func() {
					fake.mu.Lock()
					defer fake.mu.Unlock()
					for _, g := range fake.usergroups {
						g.DateDelete = fakeCreatedAt
					}
				},
				Config: testProviderConfig + usergroupConfig,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction("slack_usergroup.test", plancheck.ResourceActionCreate),
					},
				},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("slack_usergroup.test", "name", "Platform"),
					func(s *terraform.State) error {
						fake.mu.Lock()
						defer fake.mu.Unlock()
						if len(fake.usergroups) != 1 {
							return fmt.Errorf("expected the disabled user group to be reused, got %d user groups", len(fake.usergroups))
						}
						return nil
					},
				),
			},
		},
	})
}

func TestAccUsergroup_UnmanagedMembers(t *testing.T) {
	fake := newFakeSlack()
	server := setupTestServer(t, fake)
	setupTestClient(t, server)

	config := testProviderConfig + `
resource "slack_usergroup" "test" {
  name = "Readers"
}
`

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: config,
				Check:  resource.TestCheckResourceAttr("slack_usergroup.test", "users.#", "0"),
			},
			{
				PreConfig: func() {
					fake.mu.Lock()
					defer fake.mu.Unlock()
					for _, g := range fake.usergroups {
						g.Users = []string{"U0000000009"}
					}
				},
				Config: config,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectEmptyPlan(),
					},
				},
				Check: resource.TestCheckResourceAttr("slack_usergroup.test", "users.#", "1"),
			},
		},
	})
}

func TestAccUsergroup_EmptyUsersRejected(t *testing.T) {
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: testProviderConfig + `
resource "slack_usergroup" "test" {
  name  = "Nobody"
  users = []
}
`,
				PlanOnly:    true,
				ExpectError: regexp.MustCompile(`at least 1`),
			},
		},
	})
}
