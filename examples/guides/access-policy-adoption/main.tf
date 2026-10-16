variable "everything_policy_id" {
  description = "Existing Everything policy ID, discovered through obot_mcp_access_rules."
  type        = string
  default     = "acr1-everything"
}

variable "everything_policy_display_name" {
  description = "Copy the existing policy display name."
  type        = string
  default     = "Everything"
}

variable "everything_policy_subjects" {
  description = "Copy actual subjects from the live policy; do not infer everyone from its name."
  type        = set(object({ type = string, id = string }))
}

variable "everything_policy_resources" {
  description = "Copy actual resource selectors from the live policy."
  type        = set(object({ type = string, id = string }))
}

resource "obot_mcp_access_rule" "everything" {
  display_name = var.everything_policy_display_name
  subjects     = var.everything_policy_subjects
  resources    = var.everything_policy_resources
}

import {
  to = obot_mcp_access_rule.everything
  id = var.everything_policy_id
}
