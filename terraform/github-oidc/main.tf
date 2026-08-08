resource "aws_iam_policy" "terraform" {
  name        = var.policy_name
  description = "GitHub Actions OIDC: Terraform state + kdop control-plane infra"

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Sid    = "TfState"
        Effect = "Allow"
        Action = [
          "s3:ListBucket",
          "s3:GetObject",
          "s3:PutObject",
          "s3:DeleteObject",
        ]
        Resource = [
          "arn:aws:s3:::${var.tf_state_bucket}",
          "arn:aws:s3:::${var.tf_state_bucket}/*",
        ]
      },
      {
        Sid    = "TfLock"
        Effect = "Allow"
        Action = [
          "dynamodb:GetItem",
          "dynamodb:PutItem",
          "dynamodb:DeleteItem",
          "dynamodb:DescribeTable",
        ]
        Resource = "arn:aws:dynamodb:${var.aws_region}:${data.aws_caller_identity.current.account_id}:table/${var.tf_state_lock_table}"
      },
      {
        Sid    = "TfInfra"
        Effect = "Allow"
        Action = [
          "ec2:*",
          "iam:CreateRole",
          "iam:DeleteRole",
          "iam:GetRole",
          "iam:PassRole",
          "iam:AttachRolePolicy",
          "iam:DetachRolePolicy",
          "iam:CreateInstanceProfile",
          "iam:DeleteInstanceProfile",
          "iam:AddRoleToInstanceProfile",
          "iam:RemoveRoleFromInstanceProfile",
          "iam:GetInstanceProfile",
          "iam:ListInstanceProfilesForRole",
          "iam:TagRole",
          "iam:TagInstanceProfile",
        ]
        Resource = "*"
      },
    ]
  })
}

resource "aws_iam_role" "github_actions" {
  name = var.role_name

  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect = "Allow"
      Principal = {
        Federated = data.aws_iam_openid_connect_provider.github.arn
      }
      Action = [
        "sts:AssumeRoleWithWebIdentity",
        "sts:TagSession",
      ]
      Condition = {
        StringEquals = {
          "token.actions.githubusercontent.com:aud" = "sts.amazonaws.com"
        }
        StringLike = {
          "token.actions.githubusercontent.com:sub" = "repo:${var.github_repository}:*"
        }
      }
    }]
  })
}

resource "aws_iam_role_policy_attachment" "terraform" {
  role       = aws_iam_role.github_actions.name
  policy_arn = aws_iam_policy.terraform.arn
}
