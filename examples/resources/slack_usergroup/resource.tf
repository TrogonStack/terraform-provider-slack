resource "slack_usergroup" "platform" {
  name        = "Platform"
  handle      = "platform"
  description = "Platform engineers"
  channels    = [slack_conversation.platform.id]
  users       = ["U0123456789", "U9876543210"]
}
