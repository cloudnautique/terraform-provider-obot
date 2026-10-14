terraform {
  required_version = ">= 1.13.0"

  required_providers {
    obot = {
      source = "obot-platform/obot"
    }
  }
}

provider "obot" {}

data "obot_current_user" "current" {}

output "current_email" {
  value = data.obot_current_user.current.email
}
