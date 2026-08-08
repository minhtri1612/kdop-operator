#!/usr/bin/env bash
# Fix GitHub Actions OIDC trust: TagSession + correct thumbprints/audience.
set -euo pipefail

ACCOUNT="$(aws sts get-caller-identity --query Account --output text)"
ROLE_NAME="${ROLE_NAME:-github-actions-kdop-terraform}"
REPO="${GITHUB_REPO:-minhtri1612/kdop-operator}"
PROVIDER_ARN="arn:aws:iam::${ACCOUNT}:oidc-provider/token.actions.githubusercontent.com"
ROLE_ARN="arn:aws:iam::${ACCOUNT}:role/${ROLE_NAME}"

THUMBPRINTS=(
  "6938fd4d98bab03faadb97b34396831e3780aea1"
  "1c58a3a8518e8759bf075b76b750d4f2df264fcd"
)

echo "==> Account ${ACCOUNT}"
aws iam update-open-id-connect-provider-thumbprint \
  --open-id-connect-provider-arn "${PROVIDER_ARN}" \
  --thumbprint-list "${THUMBPRINTS[@]}" || true

CLIENT_IDS="$(aws iam get-open-id-connect-provider \
  --open-id-connect-provider-arn "${PROVIDER_ARN}" \
  --query 'ClientIDList' --output text)"
if [[ " ${CLIENT_IDS} " != *" sts.amazonaws.com "* ]]; then
  aws iam add-client-id-to-open-id-connect-provider \
    --open-id-connect-provider-arn "${PROVIDER_ARN}" \
    --client-id sts.amazonaws.com
fi

echo "==> Updating trust policy (AssumeRoleWithWebIdentity + TagSession)..."
TRUST_FILE="$(mktemp)"
cat >"${TRUST_FILE}" <<EOF
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Effect": "Allow",
      "Principal": {
        "Federated": "${PROVIDER_ARN}"
      },
      "Action": [
        "sts:AssumeRoleWithWebIdentity",
        "sts:TagSession"
      ],
      "Condition": {
        "StringEquals": {
          "token.actions.githubusercontent.com:aud": "sts.amazonaws.com"
        },
        "StringLike": {
          "token.actions.githubusercontent.com:sub": "repo:${REPO}:*"
        }
      }
    }
  ]
}
EOF
aws iam update-assume-role-policy \
  --role-name "${ROLE_NAME}" \
  --policy-document "file://${TRUST_FILE}"
rm -f "${TRUST_FILE}"

aws iam get-role --role-name "${ROLE_NAME}" \
  --query 'Role.AssumeRolePolicyDocument' --output json

gh secret set AWS_ROLE_ARN -R "${REPO}" --body "${ROLE_ARN}"

echo
echo "Done. Re-run:"
echo "  gh workflow run terraform.yml -R ${REPO} -f action=plan"
