data "obot_git_credentials" "all" {}

# Display names need not be unique, so list each credential with its ID.
output "git_credentials" {
  value = [
    for credential in data.obot_git_credentials.all.credentials : {
      id           = credential.id
      display_name = credential.display_name
      host         = credential.host
    }
  ]
}
