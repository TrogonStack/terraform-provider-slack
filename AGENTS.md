# terraform-provider-slack

Always use `/claude-md-improver` when updating this file.

Terraform provider for managing Slack channels and user groups through the Slack Web API.

- **Module**: `github.com/TrogonStack/terraform-provider-slack`
- **Package**: `internal/provider/` (single flat package, all resources here)

## Commands

```bash
mise run test          # go test -count=1 -cover ./...
mise run lint          # golangci-lint run --fix ./...
mise run build         # full CI pipeline (download, tidy, lint, test, docs, diff)
mise run docs          # regenerate docs/ from schema descriptions
mise run test:live     # live acceptance tests, needs SLACK_TOKEN and a sandbox workspace
```

Single test:

```bash
go test ./internal/provider/ -v -run TestAccConversation
```

Runtime credentials the provider itself needs (not required to run the test suite, which never calls a real API):

- `SLACK_TOKEN`: a Slack bot token (`xoxb-`) with the scopes listed in `provider.go`

## Skills

Always load `terraform-provider-dev` when working on a resource or data source.

## Architecture

- File naming: `resource_<name>.go`, `resource_<name>_test.go`, `data_source_<name>.go`
- Provider client: `*apiClient` wraps `*slack.Client` (`github.com/slack-go/slack`), built with a retrying HTTP client from `retry.go`
- Auth: a single bot token, sent by `slack-go` as the `token` form value on every call
- New resources must be registered in `provider.go` `Resources()` / `DataSources()`

## Conventions

### Resource naming

Use the same terminology as the Slack Web API. `slack_conversation` maps to `conversations.*` and `slack.Channel`, `slack_usergroup` maps to `usergroups.*` and `slack.UserGroup`. Never abbreviate to jargon that doesn't appear in the API surface.

### Not-found handling

Slack reports a failure as HTTP 200 with `"ok": false` and an error code, which `slack-go` returns as a `slack.SlackErrorResponse`. Detect codes with the shared helpers in `errors.go`, which unwrap with `errors.As` rather than a type assertion or string comparison:

- `isNotFound(err)`: `channel_not_found`
- `hasSlackError(err, codes...)`: any listed code, e.g. `already_archived`, `name_already_exists`

Slack has no `usergroups.info`, so `findUsergroup` lists with `include_disabled` and `include_users` and filters by ID. A missing group, or one with a non-zero `DateDelete`, counts as not found.

- **Read**: call `resp.State.RemoveResource(ctx)` and return (resource was deleted externally)
- **Delete**: return without error (idempotent)

### Destroy semantics

Slack has no API to delete a channel or a user group:

- `slack_conversation` Delete archives the channel and ignores `already_archived`
- `slack_usergroup` Delete disables the group and skips a group that is missing or already disabled
- `slack_usergroup` Create retries a `name_already_exists` / `handle_already_exists` by re-enabling a disabled group with the same name or handle; an enabled duplicate is an error that tells the user to import it

### Testing

Tests use an in-memory fake of the Slack Web API, never real API calls:

- `fake_slack_test.go`: `fakeSlack` is an `http.Handler` serving the `conversations.*` and `usergroups.*` methods the provider calls, backed by mutex-guarded maps and returning Slack's own error codes; any other method, or a request without `testToken`, returns an error
- `setupTestServer()`: serves the fake from an `httptest.Server`
- `setupTestClient()`: points a real `slack.Client` at that server (via `slack.OptionAPIURL`) and injects it as `testAPIClient`, bypassing provider configuration entirely
- `live_test.go`: `TestLive_*` run against a real workspace, skipped unless `TF_ACC` and `SLACK_TOKEN` are set; `mise run test` skips them

### Context propagation

Always call the `...Context` variant of a `slack-go` method (`CreateConversationContext`, `GetUserGroupsContext`, ...) and pass the resource CRUD method's own `ctx`, so Terraform cancellation (user Ctrl+C, timeouts) reaches in-flight requests. The non-`Context` variants use `context.Background()`.

### Unmanaged attributes

`topic`, `purpose`, `handle`, `description`, `channels`, and `users` are `Optional` + `Computed` with `UseStateForUnknown`. Leaving one unset means the provider reads it but never changes it, so values edited in the Slack client are kept.

### Retry

Automatic retry on 429 and 5xx except 501. No configuration attribute: the transport in `retry.go` is fixed, and `go-retryablehttp`'s default backoff already honors Slack's `Retry-After` header on 429.

## Benchmarking

When designing resources or solving implementation questions, reference [`slack-go/slack`](https://github.com/slack-go/slack) for method signatures and response types, particularly `conversation.go` and `usergroups.go`, and the archived [`pablovarela/terraform-provider-slack`](https://github.com/pablovarela/terraform-provider-slack) for prior art on which behaviors Terraform users relied on, such as archive on destroy and re-enabling disabled user groups. Check the official method pages at [docs.slack.dev](https://docs.slack.dev/reference/methods) for the error codes each method can return before matching on one.

## CI

- PR: lint + test + build (GitHub Actions)
- Release: release-please + goreleaser on push to main
