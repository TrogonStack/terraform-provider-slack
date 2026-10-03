# terraform-provider-slack

Always use `/claude-md-improver` when updating this file.

Terraform provider for managing Slack apps, channels and user groups through the Slack Web API.

- **Module**: `github.com/TrogonStack/terraform-provider-slack`
- **Package**: `internal/provider/` (single flat package, all resources here)

## Commands

```bash
mise run test          # go test -count=1 -cover ./...
mise run lint          # golangci-lint run --fix ./...
mise run build         # full CI pipeline (download, tidy, lint, test, docs, diff)
mise run docs          # regenerate docs/ from schema descriptions
mise run test:live     # live acceptance tests, needs SLACK_TOKEN and/or SLACK_APP_CONFIGURATION_TOKEN and a sandbox workspace
```

Single test:

```bash
go test ./internal/provider/ -v -run TestAccConversation
```

Runtime credentials the provider itself needs (not required to run the test suite, which never calls a real API):

- `SLACK_TOKEN`: a Slack bot token (`xoxb-`) with the scopes listed in `provider.go`, used by `slack_conversation` and `slack_usergroup`
- `SLACK_APP_CONFIGURATION_TOKEN`: an app configuration token, used by `slack_app`; it expires after 12 hours and the provider never rotates it

## Skills

Always load `terraform-provider-dev` when working on a resource or data source.

## Architecture

- File naming: `resource_<name>.go`, `resource_<name>_test.go`, `data_source_<name>.go`
- Provider client: `*apiClient` holds `slack` (`*slack.Client` from `github.com/slack-go/slack`) and `apps` (`*appsClient` in `apps_client.go`), each built with a retrying HTTP client from `retry.go`
- Auth: the bot token builds `slack` and the app configuration token builds `apps`. Either is nil when its token is unset, and each resource's Configure calls `requireBotToken` or `requireAppConfigurationToken` to fail with a diagnostic
- `appsClient` calls `apps.manifest.*` itself with the manifest as raw JSON, because `slack-go` decodes manifests into a fixed struct that drops fields it does not model, and its create response omits `app_id` and the credentials
- New resources must be registered in `provider.go` `Resources()` / `DataSources()`

## Conventions

### Resource naming

Use the same terminology as the Slack Web API. `slack_app` maps to `apps.manifest.*`, `slack_conversation` maps to `conversations.*` and `slack.Channel`, `slack_usergroup` maps to `usergroups.*` and `slack.UserGroup`. Never abbreviate to jargon that doesn't appear in the API surface.

### Not-found handling

Slack reports a failure as HTTP 200 with `"ok": false` and an error code, which `slack-go` returns as a `slack.SlackErrorResponse`. Detect codes with the shared helpers in `errors.go`, which unwrap with `errors.As` rather than a type assertion or string comparison:

- `isNotFound(err)`: `channel_not_found`
- `describeSlackError(err)`: the error code plus each `pointer: message` that `apps.manifest.*` returns for an invalid manifest
- `hasSlackError(err, codes...)`: any listed code, e.g. `already_archived`, `name_already_exists`

`slack_app` Read treats only `app_not_found` as gone. Delete also ignores `invalid_app_id`.

Slack has no `usergroups.info`, so `findUsergroup` lists with `include_disabled` and `include_users` and filters by ID. A missing group, or one with a non-zero `DateDelete`, counts as not found.

- **Read**: call `resp.State.RemoveResource(ctx)` and return (resource was deleted externally)
- **Delete**: return without error (idempotent)

### Destroy semantics

Slack has no API to delete a channel or a user group:

- `slack_app` Delete deletes the app with `apps.manifest.delete`, the only resource Slack can delete
- `slack_conversation` Delete archives the channel and ignores `already_archived`
- `slack_usergroup` Delete disables the group and skips a group that is missing or already disabled
- `slack_usergroup` Create retries a `name_already_exists` / `handle_already_exists` by re-enabling a disabled group with the same name or handle; an enabled duplicate is an error that tells the user to import it

### App manifests

- `manifest` is a `jsontypes.Normalized` string, so whitespace and key order never show as drift
- Create and Update call `apps.manifest.validate` first and report Slack's per-field errors on `manifest`
- `apps.manifest.export` adds defaults the configuration never set. Read keeps the state manifest while `manifestCovers` finds every configured value in the export (lists match in any order, and a missing key matches a zero value), and writes the export otherwise
- Slack returns `client_id`, `client_secret`, `signing_secret`, `verification_token` and `oauth_authorize_url` only from `apps.manifest.create`. They use `UseStateForUnknown`, and an import cannot recover them

### Testing

Tests use an in-memory fake of the Slack Web API, never real API calls:

- `fake_slack_test.go`: `fakeSlack` is an `http.Handler` serving the `apps.manifest.*`, `conversations.*` and `usergroups.*` methods the provider calls, backed by mutex-guarded maps and returning Slack's own error codes; `apps.manifest.*` requires `testConfigToken`, every other method requires `testToken`, and an unknown method returns an error. Its export adds defaults, so tests exercise the drift compare
- `setupTestServer()`: serves the fake from an `httptest.Server`
- `setupTestClient()`: points a real `slack.Client` (via `slack.OptionAPIURL`) and an `appsClient` at that server and injects them as `testAPIClient`, bypassing provider configuration entirely. Set either field to nil after it to test the missing-credential diagnostics
- `live_test.go`: `TestLive_*` run against a real workspace, skipped unless `TF_ACC` and the token the resource needs (`SLACK_TOKEN` or `SLACK_APP_CONFIGURATION_TOKEN`) are set; `mise run test` skips them

### Context propagation

Always call the `...Context` variant of a `slack-go` method (`CreateConversationContext`, `GetUserGroupsContext`, ...) and pass the resource CRUD method's own `ctx`, so Terraform cancellation (user Ctrl+C, timeouts) reaches in-flight requests. The non-`Context` variants use `context.Background()`.

### Unmanaged attributes

`topic`, `purpose`, `handle`, `description`, `channels`, and `users` are `Optional` + `Computed` with `UseStateForUnknown`. Leaving one unset means the provider reads it but never changes it, so values edited in the Slack client are kept.

### Retry

Automatic retry on 429 and 5xx except 501. `apps.manifest.create`, `update` and `delete` are Tier 1, so a 429 there is common. No configuration attribute: the transport in `retry.go` is fixed, and `go-retryablehttp`'s default backoff already honors Slack's `Retry-After` header on 429.

## Benchmarking

When designing resources or solving implementation questions, reference [`slack-go/slack`](https://github.com/slack-go/slack) for method signatures and response types, particularly `conversation.go`, `usergroups.go` and `manifests.go`, and the archived [`pablovarela/terraform-provider-slack`](https://github.com/pablovarela/terraform-provider-slack) for prior art on which behaviors Terraform users relied on, such as archive on destroy and re-enabling disabled user groups. Check the official method pages at [docs.slack.dev](https://docs.slack.dev/reference/methods) for the error codes each method can return before matching on one.

## CI

- PR: lint + test + build (GitHub Actions)
- Release: release-please + goreleaser on push to main
