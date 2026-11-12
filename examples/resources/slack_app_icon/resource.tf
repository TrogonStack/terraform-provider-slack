resource "slack_app_icon" "from_file" {
  app_id = slack_app.workspace.id
  file   = "${path.module}/icon.png"
  sha256 = filesha256("${path.module}/icon.png")
}

resource "slack_app_icon" "from_url" {
  app_id = "A0123456789"
  url    = "https://example.com/icon.png"
}
