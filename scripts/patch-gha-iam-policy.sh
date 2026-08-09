#!/usr/bin/env bash
# Patch live GitHub Actions terraform policy with IAM read/write needed for plan/apply.
set -euo pipefail

ROLE=github-actions-kdop-terraform
POLICY_ARN=$(aws iam list-attached-role-policies --role-name "$ROLE" \
  --query "AttachedPolicies[0].PolicyArn" --output text)
echo "Updating policy: $POLICY_ARN"

# Pull current default version, merge missing IAM actions into TfInfra, publish new default.
python3 - <<'PY'
import json, subprocess, sys

role = "github-actions-kdop-terraform"
arn = subprocess.check_output(
    ["aws", "iam", "list-attached-role-policies", "--role-name", role,
     "--query", "AttachedPolicies[0].PolicyArn", "--output", "text"],
    text=True,
).strip()
ver = subprocess.check_output(
    ["aws", "iam", "get-policy", "--policy-arn", arn,
     "--query", "Policy.DefaultVersionId", "--output", "text"],
    text=True,
).strip()
doc = json.loads(subprocess.check_output(
    ["aws", "iam", "get-policy-version", "--policy-arn", arn, "--version-id", ver,
     "--query", "PolicyVersion.Document", "--output", "json"],
    text=True,
))

needed = [
    "iam:UpdateAssumeRolePolicy",
    "iam:ListAttachedRolePolicies",
    "iam:ListRolePolicies",
    "iam:GetRolePolicy",
    "iam:PutRolePolicy",
    "iam:DeleteRolePolicy",
    "iam:UntagRole",
    "iam:UntagInstanceProfile",
]
for stmt in doc["Statement"]:
    if stmt.get("Sid") == "TfInfra" or (
        isinstance(stmt.get("Action"), list) and "ec2:*" in stmt["Action"]
    ):
        actions = set(stmt["Action"])
        actions.update(needed)
        stmt["Action"] = sorted(actions)
        break
else:
    sys.exit("TfInfra statement not found")

path = "/tmp/gha-tf-policy.json"
with open(path, "w") as f:
    json.dump(doc, f)
print(path)
print(arn)
PY

OUT=$(python3 - <<'PY'
import json, subprocess, sys
role = "github-actions-kdop-terraform"
arn = subprocess.check_output(
    ["aws", "iam", "list-attached-role-policies", "--role-name", role,
     "--query", "AttachedPolicies[0].PolicyArn", "--output", "text"],
    text=True,
).strip()
ver = subprocess.check_output(
    ["aws", "iam", "get-policy", "--policy-arn", arn,
     "--query", "Policy.DefaultVersionId", "--output", "text"],
    text=True,
).strip()
doc = json.loads(subprocess.check_output(
    ["aws", "iam", "get-policy-version", "--policy-arn", arn, "--version-id", ver,
     "--query", "PolicyVersion.Document", "--output", "json"],
    text=True,
))
needed = [
    "iam:UpdateAssumeRolePolicy",
    "iam:ListAttachedRolePolicies",
    "iam:ListRolePolicies",
    "iam:GetRolePolicy",
    "iam:PutRolePolicy",
    "iam:DeleteRolePolicy",
    "iam:UntagRole",
    "iam:UntagInstanceProfile",
]
for stmt in doc["Statement"]:
    if stmt.get("Sid") == "TfInfra" or (
        isinstance(stmt.get("Action"), list) and "ec2:*" in stmt["Action"]
    ):
        actions = set(stmt["Action"])
        actions.update(needed)
        stmt["Action"] = sorted(actions)
        break
else:
    sys.exit("TfInfra statement not found")
path = "/tmp/gha-tf-policy.json"
json.dump(doc, open(path, "w"))
print(f"{path}\n{arn}")
PY
)

POLICY_FILE=$(echo "$OUT" | head -1)
POLICY_ARN=$(echo "$OUT" | tail -1)

# IAM managed policies allow max 5 versions — delete oldest non-default if needed.
COUNT=$(aws iam list-policy-versions --policy-arn "$POLICY_ARN" --query 'length(Versions)' --output text)
if [ "$COUNT" -ge 5 ]; then
  OLD=$(aws iam list-policy-versions --policy-arn "$POLICY_ARN" \
    --query 'Versions[?IsDefaultVersion==`false`] | sort_by(@, &CreateDate)[0].VersionId' --output text)
  aws iam delete-policy-version --policy-arn "$POLICY_ARN" --version-id "$OLD"
fi

aws iam create-policy-version \
  --policy-arn "$POLICY_ARN" \
  --policy-document "file://${POLICY_FILE}" \
  --set-as-default

echo "Policy updated. Re-run plan:"
echo "  gh run rerun 31250505148 --failed -R minhtri1612/kdop-operator"
