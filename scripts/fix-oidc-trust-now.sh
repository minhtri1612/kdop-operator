#!/usr/bin/env bash
# One-shot: fix live IAM trust for GitHub numeric OIDC sub, then re-run Terraform workflow.
set -euo pipefail

ROLE=github-actions-kdop-terraform
PROVIDER=arn:aws:iam::629954427603:oidc-provider/token.actions.githubusercontent.com
TRUST=$(mktemp)

cat >"$TRUST" <<'EOF'
{
  "Version": "2012-10-17",
  "Statement": [{
    "Effect": "Allow",
    "Principal": {
      "Federated": "arn:aws:iam::629954427603:oidc-provider/token.actions.githubusercontent.com"
    },
    "Action": ["sts:AssumeRoleWithWebIdentity", "sts:TagSession"],
    "Condition": {
      "StringEquals": {
        "token.actions.githubusercontent.com:aud": "sts.amazonaws.com"
      },
      "StringLike": {
        "token.actions.githubusercontent.com:sub": [
          "repo:minhtri1612/kdop-operator:*",
          "repo:minhtri1612@*/kdop-operator@*:*",
          "repo:minhtri1612@156641195/kdop-operator@1306688297:*"
        ]
      }
    }
  }]
}
EOF

# Ensure Federated ARN matches account (rewrite if template above drifts)
python3 - <<PY
import json
p = "$TRUST"
doc = json.load(open(p))
doc["Statement"][0]["Principal"]["Federated"] = "$PROVIDER"
json.dump(doc, open(p, "w"), indent=2)
PY

aws iam update-assume-role-policy --role-name "$ROLE" --policy-document "file://$TRUST"
rm -f "$TRUST"
aws iam get-role --role-name "$ROLE" --query "Role.AssumeRolePolicyDocument" --output json

echo "Re-running failed Terraform workflow..."
gh run rerun 31250412422 -R minhtri1612/kdop-operator --failed \
  || gh workflow run terraform.yml -R minhtri1612/kdop-operator -f action=plan

sleep 20
gh run list -R minhtri1612/kdop-operator --workflow=terraform.yml -L 3
echo "Done. Watch: https://github.com/minhtri1612/kdop-operator/actions"
