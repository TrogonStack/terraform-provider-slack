terraform {
  required_providers {
    slack = {
      source = "trogonstack/slack"
    }
  }
}

provider "slack" {
  token                   = var.slack_token
  app_configuration_token = var.slack_app_configuration_token
}

variable "slack_token" {
  type      = string
  sensitive = true
}

variable "slack_app_configuration_token" {
  type      = string
  sensitive = true
}
