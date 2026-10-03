resource "slack_conversation" "platform" {
  name    = "eng-platform"
  topic   = "Platform engineering"
  purpose = "Questions and announcements for the platform team"
}
