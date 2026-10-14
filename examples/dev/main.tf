terraform {
  required_version = ">= 1.13.0"

  required_providers {
    obot = {
      source = "obot-platform/obot"
    }
  }
}

provider "obot" {}
