terraform {
  required_providers {
    obot = {
      source  = "obot-platform/obot"
      version = "~> 0.1"
    }
  }
}

# Reads OBOT_BASE_URL and OBOT_TOKEN when base_url and token are not set.
provider "obot" {}
