# Contributing

## Prerequisites

- [mise](https://mise.jdx.dev), which pins the Go, `golangci-lint`, GoReleaser, and `tfplugindocs` versions used by CI

Install the toolchain with `mise install`. Every command below runs through `mise` so local runs match CI.

## Development

```bash
mise run build   # full CI pipeline: download, lint, test, tidy, docs, diff
mise run test    # go test -count=1 -cover ./...
mise run lint    # golangci-lint run --fix ./...
mise run docs    # regenerate docs/ from schema descriptions
```

`mise run build` is what CI runs on every pull request, including the `git diff --exit-code` check, so run it before pushing.

## Testing

Tests run against an in-memory fake of the Slack Web API and never reach Slack. `fakeSlack` in `fake_slack_test.go` is an `http.Handler` that answers the `apps.manifest.*`, `conversations.*` and `usergroups.*` methods the provider calls, with Slack's own error codes, and rejects any request without the matching test token. `setupTestServer` serves it from an `httptest.Server`; `setupTestClient` points a real `slack.Client` (with `slack.OptionAPIURL`) and an `appsClient` at that server and injects them as `testAPIClient`, bypassing provider configuration entirely.

```bash
mise exec -- go test ./internal/provider/ -v -run TestAccConversation
```

`live_test.go` runs the same resources against a real workspace. Each test skips unless `TF_ACC` and the token it needs are set. The channel and user group tests need `SLACK_TOKEN`, and they leave channels and user groups archived or disabled, not deleted. The app test needs `SLACK_APP_CONFIGURATION_TOKEN`, and it deletes the app it creates. Use a sandbox workspace.

```bash
SLACK_TOKEN=xoxb-... SLACK_APP_CONFIGURATION_TOKEN=... mise run test:live
```

## Code layout

All resources live in the flat `internal/provider/` package, named `resource_<name>.go` with tests alongside as `<file>_test.go`. New resources must be registered in the `Resources()` or `DataSources()` method in `provider.go`, or the provider will not expose them.

These things are easy to get wrong here:

- Slack reports errors as HTTP 200 with `"ok": false`, which `slack-go` returns as a `slack.SlackErrorResponse`. Match error codes with the shared `hasSlackError(err, codes...)` and `isNotFound(err)` helpers, which unwrap with `errors.As`, rather than comparing `err.Error()` strings.
- `slack-go` cannot round-trip an app manifest, so `appsClient` in `apps_client.go` calls `apps.manifest.*` with raw JSON. Its errors are still `slack.SlackErrorResponse`, and `describeSlackError` adds Slack's per-field manifest errors to a diagnostic.
- Slack has no `usergroups.info`. `findUsergroup` lists with `include_disabled` and `include_users` and filters by ID, and a group with a non-zero `date_delete` counts as gone.

On a not-found error, `Read` should call `resp.State.RemoveResource(ctx)` and return; `Delete` should return without an error.

## Commits

Commits follow [Conventional Commits](https://www.conventionalcommits.org) and require a [DCO](https://developercertificate.org) sign-off:

```bash
git commit -s -m "fix(usergroup): keep unmanaged members on update"
```

The commit type determines the next version, so it is worth getting right.

## Releases

[release-please](https://github.com/googleapis/release-please) reads the conventional commits merged into `main` and maintains an open release pull request with the computed version bump and changelog entries. Merging that pull request tags the release and publishes the provider archives, plus a GPG-signed checksum file, via [GoReleaser](https://goreleaser.com). No release happens without that pull request being merged.

Each release carries the assets the provider registry protocol expects: one zip per platform, a `SHA256SUMS` file, a detached GPG signature over it, and `terraform-provider-slack_<version>_manifest.json` built from `terraform-registry-manifest.json` at the repository root. That manifest declares plugin protocol 6, which `providerserver.Serve` uses because `main.go` leaves `ProtocolVersion` unset. Registries assume protocol 5.0 when the manifest is missing, so a release without it installs and then fails to load.
