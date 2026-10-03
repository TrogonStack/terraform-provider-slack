# terraform-provider-slack

**A Terraform provider that manages Slack apps, channels and user groups as code.** It covers the Slack Web API behind a single provider configuration.

**The provider turns workspace structure into declarative resources.** An app's manifest, a channel's name, privacy, topic, purpose, and archive state, and a user group's handle, description, default channels, and members all become Terraform resources with full create, read, update, delete, and import support. A bot token manages channels and user groups, and an app configuration token manages apps.

**It exists because workspace structure otherwise drifts between the Slack client and whoever clicked last.** Channels get renamed by hand, user groups gain and lose members ad hoc, and neither leaves the kind of record that a code review or a rollout pipeline can rely on. Expressing channels and user groups as Terraform configuration puts those changes under review, makes them reproducible across workspaces, and lets Slack live alongside the rest of your infrastructure.

**It is useful to teams already running Terraform** who want team channels and on-call user groups created by the same pipeline that creates the teams' other infrastructure, without hand-editing them in the Slack client.

## Provider configuration

```hcl
provider "slack" {
  token                   = var.slack_token
  app_configuration_token = var.slack_app_configuration_token
}
```

| Attribute                 | Environment variable            | Description                                                                 |
| ------------------------- | ------------------------------- | --------------------------------------------------------------------------- |
| `token`                   | `SLACK_TOKEN`                   | Slack bot token (`xoxb-`). Needed by `slack_conversation` and `slack_usergroup`. Optional, sensitive. |
| `app_configuration_token` | `SLACK_APP_CONFIGURATION_TOKEN` | Slack app configuration token. Needed by `slack_app`. Optional, sensitive. |

Each attribute falls back to its environment variable when unset in configuration. The provider fails if both are missing, and each resource fails with a diagnostic that names the credential it needs.

The bot token needs these scopes, added under OAuth & Permissions in the Slack app settings:

| Scope              | Needed for                             |
| ------------------ | -------------------------------------- |
| `channels:manage`  | creating and changing public channels  |
| `channels:read`    | reading public channels                |
| `groups:write`     | creating and changing private channels |
| `groups:read`      | reading private channels               |
| `usergroups:write` | creating and changing user groups      |
| `usergroups:read`  | reading user groups and their members  |

User groups are only available on paid Slack plans.

### App configuration token

`slack_app` calls the [App Manifest API](https://docs.slack.dev/reference/methods/apps.manifest.create), which accepts only an app configuration token. Slack issues one per workspace from the [app management page](https://api.slack.com/apps), together with a refresh token.

The configuration token expires 12 hours after it is issued, and the provider never rotates it. Rotate it in the pipeline before `terraform plan`:

1. Call [`tooling.tokens.rotate`](https://docs.slack.dev/reference/methods/tooling.tokens.rotate) with the stored refresh token.
2. Store the new refresh token that the call returns.
3. Pass the new configuration token to Terraform as `SLACK_APP_CONFIGURATION_TOKEN`.

Terraform has no safe place to keep the new refresh token, because a refresh during plan is never saved. That is why rotation stays outside the provider.

### Bootstrapping the bot token

`slack_app` can create the app that issues the bot token, so nobody builds it by hand in the Slack UI. One step stays human: a workspace admin opens the app's `oauth_authorize_url` and installs it, which issues the `xoxb-` token. Store that token as `SLACK_TOKEN`.

The admin installs again only when the manifest changes the bot scopes. Any other manifest change applies without a new install.

## Resources

| Type                 | API                 | Credential                |
| -------------------- | ------------------- | ------------------------- |
| `slack_app`          | `apps.manifest.*`   | `app_configuration_token` |
| `slack_conversation` | `conversations.*`   | `token`                   |
| `slack_usergroup`    | `usergroups.*`      | `token`                   |

## Example

```hcl
terraform {
  required_providers {
    slack = {
      source = "trogonstack/slack"
    }
  }
}

resource "slack_app" "workspace" {
  manifest = jsonencode({
    display_information = {
      name = "Workspace Terraform"
    }
    features = {
      bot_user = {
        display_name = "workspace-terraform"
      }
    }
    oauth_config = {
      scopes = {
        bot = [
          "channels:manage",
          "channels:read",
          "groups:write",
          "groups:read",
          "usergroups:write",
          "usergroups:read",
        ]
      }
    }
  })
}

resource "slack_conversation" "platform" {
  name    = "eng-platform"
  topic   = "Platform engineering"
  purpose = "Questions and announcements for the platform team"
}

resource "slack_usergroup" "platform" {
  name        = "Platform"
  handle      = "platform"
  description = "Platform engineers"
  channels    = [slack_conversation.platform.id]
  users       = ["U0123456789", "U9876543210"]
}
```

Destroying `slack_app` deletes the app. Slack returns the app's credentials only when the app is created, so `client_secret`, `signing_secret` and the other credentials stay unknown for an imported app.

Slack has no API to delete a channel or a user group, so destroying `slack_conversation` archives the channel and destroying `slack_usergroup` disables the group. Creating a user group whose name or handle matches a disabled group re-enables that group instead of failing.

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md) for the development setup, test workflow, and release process.
