resource "slack_app" "workspace" {
  manifest = jsonencode({
    display_information = {
      name        = "Workspace Terraform"
      description = "Manages channels and user groups from Terraform"
    }
    features = {
      bot_user = {
        display_name  = "workspace-terraform"
        always_online = false
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
    settings = {
      org_deploy_enabled     = false
      socket_mode_enabled    = false
      token_rotation_enabled = false
    }
  })
}

output "install_url" {
  value = slack_app.workspace.oauth_authorize_url
}
