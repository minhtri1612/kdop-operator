output "role_arn" {
  value = aws_iam_role.github_actions.arn
}

output "role_name" {
  value = aws_iam_role.github_actions.name
}

output "policy_arn" {
  value = aws_iam_policy.terraform.arn
}

output "github_secrets" {
  description = "Values to set in GitHub Actions secrets"
  value = {
    AWS_ROLE_ARN        = aws_iam_role.github_actions.arn
    TF_STATE_BUCKET     = var.tf_state_bucket
    TF_STATE_LOCK_TABLE = var.tf_state_lock_table
  }
}
