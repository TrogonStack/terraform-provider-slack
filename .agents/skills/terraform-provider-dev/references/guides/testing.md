# Testing

## Overview

This provider tests almost entirely against an in-memory fake of the Slack Web API, never against real Slack credentials in the normal test suite. A small, separate set of live acceptance tests exists for pre-release sanity checks, gated behind `TF_ACC` and a real `SLACK_TOKEN` or `SLACK_APP_CONFIGURATION_TOKEN`.

## Test Infrastructure

### The Fake Slack

`fake_slack_test.go` defines `fakeSlack`, an `http.Handler` that dispatches on request path, covering the `apps.manifest.*`, `conversations.*` and `usergroups.*` endpoints the resources call:

```go
type fakeSlack struct {
    mu            sync.Mutex
    conversations map[string]*slack.Channel
    usergroups    map[string]*slack.UserGroup
    nextID        int
}

func (f *fakeSlack) ServeHTTP(w http.ResponseWriter, r *http.Request) {
    switch r.URL.Path {
    case "/conversations.create":
        f.createConversation(w, r)
    case "/usergroups.create":
        f.createUsergroup(w, r)
    case "/usergroups.list":
        f.listUsergroups(w, r)
    // ... other conversations.* and usergroups.* endpoints
    }
}
```

Unlike a fake built around quirk-simulating boolean flags, `fakeSlack` has none: there is no `failNextCreate`-style field anywhere in it. Error conditions (`name_taken`, `invalid_users`, `channel_not_found`, and so on) come from the actual state of the fake's in-memory maps, the same way the real Slack API's errors come from the actual state of a workspace.

### Provider Test Harness

`provider_test.go` wires the fake into a real provider instance:

```go
var testAccProtoV6ProviderFactories = map[string]func() (tfprotov6.ProviderServer, error){
    "slack": providerserver.NewProtocol6WithError(New("test")()),
}

const testProviderConfig = `
provider "slack" {
  token = "xoxb-unused"
}
`

func setupTestServer(t *testing.T) *fakeSlack {
    t.Helper()
    fs := &fakeSlack{
        conversations: map[string]*slack.Channel{},
        usergroups:    map[string]*slack.UserGroup{},
    }
    server := httptest.NewServer(fs)
    t.Cleanup(server.Close)
    testAPIClient = &apiClient{
        slack: slack.New("xoxb-unused", slack.OptionAPIURL(server.URL+"/"), slack.OptionHTTPClient(server.Client())),
    }
    t.Cleanup(func() { testAPIClient = nil })
    return fs
}

func setupTestClient(t *testing.T) (*fakeSlack, *slack.Client) {
    t.Helper()
    fs := setupTestServer(t)
    return fs, testAPIClient.slack
}
```

`testAPIClient` is a package-level variable that `slackProvider.Configure` checks first, before falling back to `SLACK_TOKEN` and the real `slack.New(...)` call (see `references/guides/provider-configuration.md`). Setting it in `setupTestServer` is how acceptance tests run the full provider lifecycle (Create/Read/Update/Delete through real Terraform plans) against the fake instead of real Slack, with no HTTP traffic leaving the test process.

## Basic Test Structure

```go
func TestAccConversation_Basic(t *testing.T) {
    fs := setupTestServer(t)
    resource.Test(t, resource.TestCase{
        ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
        Steps: []resource.TestStep{
            {
                Config: testProviderConfig + `
resource "slack_conversation" "test" {
  name = "test-channel"
}
`,
                Check: resource.ComposeAggregateTestCheckFunc(
                    resource.TestCheckResourceAttr("slack_conversation.test", "name", "test-channel"),
                    resource.TestCheckResourceAttrSet("slack_conversation.test", "id"),
                ),
            },
        },
    })
    _ = fs
}
```

This is the real shape of `TestAccConversation_Basic` in `resource_conversation_test.go`: one `TestStep` creating a `slack_conversation`, checking `name` and a non-empty `id`.

## Multi-Step Tests (Update Behavior)

```go
func TestAccConversation_Private(t *testing.T) {
    setupTestServer(t)
    resource.Test(t, resource.TestCase{
        ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
        Steps: []resource.TestStep{
            {
                Config: testProviderConfig + `
resource "slack_conversation" "test" {
  name       = "test-channel"
  is_private = false
}
`,
            },
            {
                Config: testProviderConfig + `
resource "slack_conversation" "test" {
  name       = "test-channel"
  is_private = true
}
`,
                ConfigPlanChecks: resource.ConfigPlanChecks{
                    PreApply: []plancheck.PlanCheck{
                        plancheck.ExpectResourceAction("slack_conversation.test", plancheck.ResourceActionReplace),
                    },
                },
            },
        },
    })
}
```

This is the real shape of `TestAccConversation_Private`: step two changes `is_private`, and `plancheck.ExpectResourceAction(..., plancheck.ResourceActionReplace)` asserts the `RequiresReplace` plan modifier actually fires (see `references/guides/plan-modification.md`).

## Import Tests

```go
{
    ResourceName:      "slack_conversation.test",
    ImportState:       true,
    ImportStateVerify: true,
},
```

Append this step after a create step to verify `ImportStatePassthroughID` round-trips correctly (`references/guides/state-management.md`). `ImportStateVerify: true` re-imports and diffs every attribute against the post-create state.

## Testing Drift (Delete/Disable Outside Terraform)

```go
func TestAccConversation_DeletedExternally(t *testing.T) {
    fs := setupTestServer(t)
    resource.Test(t, resource.TestCase{
        ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
        Steps: []resource.TestStep{
            {
                Config: testProviderConfig + `
resource "slack_conversation" "test" {
  name = "test-channel"
}
`,
            },
            {
                PreConfig: func() {
                    fs.withActiveConversation(func(c *slack.Channel) { c.IsArchived = true })
                    // simulate full deletion/gone-ness as the real API would report it
                },
                RefreshState: true,
                ExpectNonEmptyPlan: true,
            },
        },
    })
}
```

The real test mutates the fake's backing map directly between steps (there is no "delete channel" Slack API to call), then asserts the next Read picks up the change via `findConversation`/`isNotFound` and either reflects it into state (`TestAccConversation_ArchivedExternally`) or removes the resource from state entirely (`TestAccConversation_DeletedExternally`), driving a non-empty plan on the next `terraform plan`. `usergroup_test.go`'s `TestAccUsergroup_DisabledExternally` follows the identical shape against `fs.withUsergroup`, checking `checkUsergroupsDisabled`.

## Testing Unmanaged Attributes

```go
func TestAccConversation_UnmanagedTopicIsKept(t *testing.T) {
    fs := setupTestServer(t)
    resource.Test(t, resource.TestCase{
        ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
        Steps: []resource.TestStep{
            {
                Config: testProviderConfig + `
resource "slack_conversation" "test" {
  name = "test-channel"
}
`,
            },
            {
                PreConfig: func() {
                    fs.withActiveConversation(func(c *slack.Channel) { c.Topic.Value = "set outside Terraform" })
                },
                Config: testProviderConfig + `
resource "slack_conversation" "test" {
  name = "test-channel"
}
`,
                ConfigPlanChecks: resource.ConfigPlanChecks{
                    PreApply: []plancheck.PlanCheck{
                        plancheck.ExpectEmptyPlan(),
                    },
                },
            },
        },
    })
}
```

This directly tests the "unmanaged attributes" pattern described in `references/guides/plan-modification.md`: a `topic` never set in configuration is never pushed by Update, and changing it directly on the fake backend produces an empty plan (not a diff Terraform tries to "fix"), because `UseStateForUnknown` carries the previous state forward and Read always adopts the live value. `TestAccUsergroup_UnmanagedMembers` is the equivalent test for `channels`/`users` on `slack_usergroup`.

## Testing User Group Re-enable on Name Conflict

```go
func TestAccUsergroup_RecreateReenablesDisabled(t *testing.T) {
    fs := setupTestServer(t)
    resource.Test(t, resource.TestCase{
        ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
        Steps: []resource.TestStep{
            {
                Config: testProviderConfig + usergroupConfig("team-a", "@team-a"),
            },
            {
                // destroy disables rather than deletes; a second apply with the
                // same name/handle should find and re-enable the disabled group
                Config: testProviderConfig + usergroupConfig("team-a", "@team-a"),
                Taint:  []string{"slack_usergroup.test"},
            },
        },
    })
}
```

This exercises `reenableDisabled` in `resource_usergroup.go`: `usergroups.create` returns `name_already_exists` or `handle_already_exists` against a disabled group of the same name/handle, and Create recovers by listing groups (including disabled ones) and re-enabling the match, instead of failing. `TestAccUsergroup_EnabledDuplicateFails` is the negative case: when the conflicting group is still enabled, `reenableDisabled` finds no disabled match and the error surfaces, telling the practitioner to import it instead.

## Testing Validators

```go
func TestAccUsergroup_EmptyUsersRejected(t *testing.T) {
    setupTestServer(t)
    resource.Test(t, resource.TestCase{
        ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
        Steps: []resource.TestStep{
            {
                Config: testProviderConfig + `
resource "slack_usergroup" "test" {
  name   = "team-a"
  handle = "team-a"
  users  = []
}
`,
                ExpectError: regexp.MustCompile(`(?i)at least 1`),
            },
        },
    })
}
```

The real `TestAccUsergroup_EmptyUsersRejected` asserts the `setvalidator.SizeAtLeast(1)` failure described in `references/guides/validation.md`, without the fake ever seeing a request: validators run at plan time.

## Unit Tests Without the Fake

Not every test needs an HTTP server. `errors_test.go` tests `isNotFound` and `hasSlackError` as plain table tests against constructed `error` values:

```go
func TestIsNotFound(t *testing.T) {
    tests := []struct {
        name string
        err  error
        want bool
    }{
        {"channel not found", &slack.SlackErrorResponse{Err: errChannelNotFound}, true},
        {"already archived", &slack.SlackErrorResponse{Err: errAlreadyArchived}, false},
        {"nil error", nil, false},
    }
    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            if got := isNotFound(tt.err); got != tt.want {
                t.Errorf("isNotFound() = %v, want %v", got, tt.want)
            }
        })
    }
}
```

## Testing the Retry Policy Directly

`retry_test.go` tests `retryPolicy` as a plain function, with constructed `*http.Response` values and no server or fake needed at all:

```go
func TestRetryPolicy_429_Retries(t *testing.T) {
    resp := &http.Response{StatusCode: http.StatusTooManyRequests}
    retry, err := retryPolicy(context.Background(), resp, nil)
    if !retry || err != nil {
        t.Errorf("expected retry=true, err=nil; got retry=%v, err=%v", retry, err)
    }
}
```

The real suite covers `TestRetryPolicy_429_Retries`, `TestRetryPolicy_500_Retries`, `TestRetryPolicy_502_Retries`, `TestRetryPolicy_501_DoesNotRetry` (501 is explicitly excluded, since it means "not implemented," not "try again"), `TestRetryPolicy_200_DoesNotRetry`, `TestRetryPolicy_404_DoesNotRetry`, `TestRetryPolicy_ConnectionError_Retries`, and `TestRetryPolicy_CancelledContext_DoesNotRetry`.

## Live Acceptance Tests

A small second suite in `live_test.go` runs against real Slack, gated behind both `TF_ACC` and `SLACK_TOKEN`:

```go
func requireLiveCredentials(t *testing.T) string {
    t.Helper()
    if os.Getenv("TF_ACC") == "" {
        t.Skip("set TF_ACC=1 to run live acceptance tests")
    }
    token := os.Getenv("SLACK_TOKEN")
    if token == "" {
        t.Skip("set SLACK_TOKEN to run live acceptance tests")
    }
    return token
}

func TestLive_Conversation(t *testing.T) {
    requireLiveCredentials(t)
    resource.Test(t, resource.TestCase{
        ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
        Steps: []resource.TestStep{
            {
                Config: liveProviderConfig + `
resource "slack_conversation" "live" {
  name = "tf-provider-live-test"
}
`,
                Check: checkLiveConversationArchived(false),
            },
        },
    })
}
```

`TestLive_App` uses its own guard, `requireLiveAppCredentials`, which needs `SLACK_APP_CONFIGURATION_TOKEN` instead of `SLACK_TOKEN`, and checks destroy by exporting the deleted app through an `appsClient`.

`TestLive_Conversation` and `TestLive_Usergroup` are the bot-token live tests, using `liveSlackClient()` (a bare `slack.New(token)`, no fake) inside `checkLiveConversationArchived`/`checkLiveUsergroupDisabled` to confirm destroy archived or disabled the real object in the connected workspace. These never run in ordinary CI; they require a human to export `TF_ACC=1` and a valid `SLACK_TOKEN` for a real (ideally disposable) workspace.

## Check Functions Reference

| Function                            | Purpose                                      |
| -------------------------------------- | ------------------------------------------------ |
| `resource.TestCheckResourceAttr`     | Exact attribute value match                  |
| `resource.TestCheckResourceAttrSet`  | Attribute is non-empty                       |
| `resource.TestCheckNoResourceAttr`   | Attribute is absent/null                     |
| `resource.ComposeAggregateTestCheckFunc` | Combine checks, report all failures         |
| `plancheck.ExpectResourceAction`     | Assert plan action (replace, update, no-op)  |
| `plancheck.ExpectEmptyPlan`          | Assert no changes planned                    |
| Custom check funcs (e.g. `checkConversationArchived`, `checkUsergroupsDisabled`) | Query the fake/live backend directly and assert on provider-specific state the schema doesn't expose as an attribute |

## Running Tests

```bash
go test ./internal/provider/...               # Fake-backed tests only
TF_ACC=1 go test ./internal/provider/... -run TestAcc  # Fake-backed acceptance tests, verbose plan/apply cycle
TF_ACC=1 SLACK_TOKEN=xoxb-... go test ./internal/provider/... -run TestLive  # Live tests against real Slack
```

Both `TestAcc*` and `TestLive*` use `resource.Test`, which requires `TF_ACC=1` to actually run (otherwise it skips with a message); `TestLive*` additionally requires `SLACK_TOKEN` via `requireLiveCredentials`. Plain unit tests (`TestIsNotFound`, `TestHasSlackError`, `TestRetryPolicy_*`, `TestFakeRejectsMissingToken`, `TestConversationGoneErrorsAreNotFound`, `TestManifestCovers`, `TestDescribeSlackErrorListsManifestErrors`, `TestAppsClientRejectsBotToken`) run unconditionally with plain `go test`.

## Test Naming Convention

- `TestAcc<Resource>_<Scenario>`: acceptance tests against the fake (e.g. `TestAccConversation_Basic`, `TestAccConversation_Private`, `TestAccConversation_UnmanagedTopicIsKept`, `TestAccConversation_ArchivedExternally`, `TestAccConversation_DeletedExternally`, `TestAccUsergroup_Basic`, `TestAccUsergroup_RecreateReenablesDisabled`, `TestAccUsergroup_EnabledDuplicateFails`, `TestAccUsergroup_DisabledExternally`, `TestAccUsergroup_UnmanagedMembers`, `TestAccUsergroup_EmptyUsersRejected`)
- `TestLive_<Resource>`: live acceptance tests against real Slack (`TestLive_Conversation`, `TestLive_Usergroup`)
- `Test<Thing>_<Condition>`: plain unit tests (`TestRetryPolicy_429_Retries`, `TestIsNotFound`, `TestHasSlackError`)
- `TestFakeRejectsMissingToken`, `TestConversationGoneErrorsAreNotFound`: direct tests of `provider_test.go` helpers themselves, not run through `resource.Test`

## Related Framework References

| File                                             | Contents                        |
| ----------------------------------------------------- | ------------------------------------ |
| `framework/acctests/index.mdx`                   | Acceptance testing overview     |
| `framework/acctests/testing-patterns.mdx`        | Common testing patterns         |
| `framework/acctests/plan-checks.mdx`             | Plan check functions             |

## Testing `slack_app`

- `apps.manifest.*` on the fake requires `testConfigToken`, every other method requires `testToken`, so a resource that reaches for the wrong client fails with `invalid_auth`
- `setupTestClient` sets both `testAPIClient.slack` and `testAPIClient.apps`. Set one to nil afterwards to test the `Missing Credentials` diagnostic, as `TestAccApp_MissingAppConfigurationToken` and `TestAccConversation_MissingBotToken` do
- The fake's export adds `settings` defaults, so every `TestAccApp_*` step also proves the drift compare keeps a plan empty; `TestAccApp_DriftIsDetected` edits the stored manifest to prove a real change still plans an update
- A manifest without `display_information.name` returns `invalid_manifest` with a `pointer`/`message` entry, which `TestAccApp_InvalidManifest` matches in the diagnostic
- Import steps ignore `manifest` (the export holds Slack's defaults) and the credentials (Slack returns them only on create)
