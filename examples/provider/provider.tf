terraform {
  required_providers {
    slack = {
      source = "trogonstack/slack"
    }
  }
}

provider "slack" {
  token = "xoxb-xxxxxxxxxxxx-xxxxxxxxxxxxx-xxxxxxxxxxxxxxxxxxxxxxxx"
}
