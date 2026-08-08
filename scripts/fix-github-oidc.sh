#!/usr/bin/env bash
# OPEN_TRUST=1 → temporarily allow any GitHub OIDC token (debug only).
# Default → restore restricted trust for minhtri1612/kdop-operator.
set -euo pipefail

ACCOUNT="$(aws sts get-caller-identity --query Account --output text)"
ROLE_NAME="${ROLE_NAME:-github-actions-kdop-terraform}"
REPO="${GITHUB_REPO:-minhtri1612/kdop-operator}"
PROVIDER_ARN="arn:aws:iam::${ACCOUNT}:oidc-provider/token.actions.githubusercontent.com"
OPEN_TRUST="${OPEN_TRUST:-0}"

THUMBPRINTS=(
  "6938fd4d98bab03faadb97b34396831e3780aea1"
  "1c58a3a8518e8759bf075b76b750d4f2df264fcd"
)

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

TRUST_FILE="$(mktemp)"
if [[ "${OPEN_TRUST}" == "1" ]]; then
  echo "==> OPEN trust (DEBUG ONLY — no sub/aud conditions)"
  cat >"${TRUST_FILE}" <<EOF
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Effect": "Allow",
      "Principal": { "Federated": "${PROVIDER_ARN}" },
      "Action": ["sts:AssumeRoleWithWebIdentity", "sts:TagSession"]
    }
  ]
}
EOF
else
  echo "==> Restricted trust for repo:${REPO}:*"
  cat >"${TRUST_FILE}" <<EOF
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Effect": "Allow",
      "Principal": { "Federated": "${PROVIDER_ARN}" },
      "Action": ["sts:AssumeRoleWithWebIdentity", "sts:TagSession"],
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
fi

aws iam update-assume-role-policy \
  --role-name "${ROLE_NAME}" \
  --policy-document "file://${TRUST_FILE}"
rm -f "${TRUST_FILE}"

aws iam get-role --role-name "${ROLE_NAME}" \
  --query 'Role.AssumeRolePolicyDocument' --output json

echo
echo "Next: push workflow if needed, then:"
echo "  gh workflow run terraform.yml -R ${REPO} -f action=plan"
if [[ "${OPEN_TRUST}" == "1" ]]; then
  echo "IMPORTANT: after test, run without OPEN_TRUST=1 to lock trust again."
fi
