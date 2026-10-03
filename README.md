# terraform-provider-slack

**A Terraform provider that manages Slack channels and user groups as code.** It covers the Slack Web API behind a single provider configuration.

**The provider turns workspace structure into declarative resources.** A channel's name, privacy, topic, purpose, and archive state, and a user group's handle, description, default channels, and members all become Terraform resources with full create, read, update, delete, and import support. Authentication runs through a single Slack bot token, so no separate credential store is involved.

**It exists because workspace structure otherwise drifts between the Slack client and whoever clicked last.** Channels get renamed by hand, user groups gain and lose members ad hoc, and neither leaves the kind of record that a code review or a rollout pipeline can rely on. Expressing channels and user groups as Terraform configuration puts those changes under review, makes them reproducible across workspaces, and lets Slack live alongside the rest of your infrastructure.

**It is useful to teams already running Terraform** who want team channels and on-call user groups created by the same pipeline that creates the teams' other infrastructure, without hand-editing them in the Slack client.

## Provider configuration

```hcl
provider "slack" {
  token = "xoxb-xxxxxxxxxxxx-xxxxxxxxxxxxx-xxxxxxxxxxxxxxxxxxxxxxxx"
}
```

| Attribute | Environment variable | Description                                   |
| --------- | -------------------- | --------------------------------------------- |
| `token`   | `SLACK_TOKEN`        | Slack bot token (`xoxb-`). Required, sensitive. |

`token` falls back to `SLACK_TOKEN` when unset in configuration, and the provider fails with a diagnostic if the value is missing from both places.

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

## Resources

| Type                 | API                 |
| -------------------- | ------------------- |
| `slack_conversation` | `conversations.*`   |
| `slack_usergroup`    | `usergroups.*`      |

## Example

```hcl
terraform {
  required_providers {
    slack = {
      source = "trogonstack/slack"
    }
  }
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

Slack has no API to delete a channel or a user group, so destroying `slack_conversation` archives the channel and destroying `slack_usergroup` disables the group. Creating a user group whose name or handle matches a disabled group re-enables that group instead of failing.

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md) for the development setup, test workflow, and release process.
