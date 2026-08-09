#!/usr/bin/env bash
# Steps 2–5: IAM policy+role (Terraform) + GitHub secrets + aws-prod environment.
# Prerequisite: OIDC provider token.actions.githubusercontent.com already exists in AWS.
# Run from laptop/WSL with AWS CLI + gh authenticated.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
GITHUB_REPO="${GITHUB_REPO:-minhtri1612/kdop-operator}"
TF_ADMIN_CIDR="${TF_ADMIN_CIDR:-}"
AWS_ACCOUNT_ID="$(aws sts get-caller-identity --query Account --output text)"
OIDC_PROVIDER_ARN="arn:aws:iam::${AWS_ACCOUNT_ID}:oidc-provider/token.actions.githubusercontent.com"

echo "==> Ensuring OIDC provider audience includes sts.amazonaws.com..."
CLIENT_IDS="$(aws iam get-open-id-connect-provider \
  --open-id-connect-provider-arn "${OIDC_PROVIDER_ARN}" \
  --query 'ClientIDList' --output text 2>/dev/null || true)"
if [[ " ${CLIENT_IDS} " != *" sts.amazonaws.com "* ]]; then
  echo "Adding client ID sts.amazonaws.com to OIDC provider..."
  aws iam add-client-id-to-open-id-connect-provider \
    --open-id-connect-provider-arn "${OIDC_PROVIDER_ARN}" \
    --client-id sts.amazonaws.com
else
  echo "OIDC ClientIDList already has sts.amazonaws.com"
fi

echo "==> Applying terraform/github-oidc (IAM policy + role)..."
cd "${ROOT}/terraform/github-oidc"
terraform init -input=false
terraform apply -input=false -auto-approve \
  -var="github_repository=${GITHUB_REPO}"

ROLE_ARN="$(terraform output -raw role_arn)"
BUCKET="$(terraform output -json github_secrets | python3 -c "import json,sys; print(json.load(sys.stdin)['TF_STATE_BUCKET'])")"
LOCK_TABLE="$(terraform output -json github_secrets | python3 -c "import json,sys; print(json.load(sys.stdin)['TF_STATE_LOCK_TABLE'])")"

echo "==> Setting GitHub repository secrets..."
gh secret set AWS_ROLE_ARN --repo "${GITHUB_REPO}" --body "${ROLE_ARN}"
gh secret set TF_STATE_BUCKET --repo "${GITHUB_REPO}" --body "${BUCKET}"
gh secret set TF_STATE_LOCK_TABLE --repo "${GITHUB_REPO}" --body "${LOCK_TABLE}"

if [ -n "${TF_ADMIN_CIDR}" ]; then
  gh secret set TF_ADMIN_CIDR --repo "${GITHUB_REPO}" --body "${TF_ADMIN_CIDR}"
  echo "Set TF_ADMIN_CIDR=${TF_ADMIN_CIDR}"
else
  echo "Skip TF_ADMIN_CIDR (set env TF_ADMIN_CIDR=your.ip/32 to pin SG for SSH/6443)"
fi

echo "==> Creating GitHub environment aws-prod..."
gh api --method PUT -H "Accept: application/vnd.github+json" \
  "/repos/${GITHUB_REPO}/environments/aws-prod" >/dev/null

cat <<EOF

Done.

  Role ARN:    ${ROLE_ARN}
  State bucket: ${BUCKET}
  Lock table:   ${LOCK_TABLE}

Next: Actions → Terraform → Run workflow → plan

EOF
