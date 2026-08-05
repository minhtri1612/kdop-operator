variable "aws_region" {
  type    = string
  default = "ap-southeast-2"
}

variable "github_repository" {
  description = "GitHub repo allowed to assume the role (org/name)"
  type        = string
  default     = "minhtri1612/kdop-operator"
}

variable "role_name" {
  type    = string
  default = "github-actions-kdop-terraform"
}

variable "policy_name" {
  type    = string
  default = "kdop-terraform-github-actions"
}

variable "tf_state_bucket" {
  description = "S3 bucket from bootstrap-state output"
  type        = string
  default     = "kdop-tfstate-629954427603"
}

variable "tf_state_lock_table" {
  type    = string
  default = "kdop-tfstate-lock"
}
