# GitHub Actions + Terraform (professional flow)

Local `terraform apply` chỉ dùng để **bootstrap state** lần đầu. Sau đó infra đổi qua PR → Plan → merge `main` → Apply.

```
PR (terraform/**)  →  terraform plan  (+ comment)
merge main         →  terraform apply  (GitHub Environment: aws-prod)
```

Auth: **OIDC** (không nhét AWS access key dài hạn vào repo).

---

## One-time setup

### 1) Bootstrap remote state (laptop, 1 lần)

```bash
cd terraform/bootstrap-state
terraform init
terraform apply
terraform output backend_hcl
# copy vào terraform/backend.hcl (gitignored) for local use
```

### 2) Migrate control-plane stack to S3

```bash
cd terraform
cp backend.hcl.example backend.hcl
# paste bucket/table from bootstrap output
terraform init -migrate-state -backend-config=backend.hcl
```

### 3–5) IAM role + GitHub secrets (automated)

OIDC provider `token.actions.githubusercontent.com` must already exist (one per AWS account).

```bash
# AWS credentials on laptop + gh auth login
export TF_ADMIN_CIDR="123.21.136.125/32"   # optional but recommended (your home IP)
bash scripts/setup-github-actions.sh
```

This applies `terraform/github-oidc/` (policy + role) and sets secrets:

| Secret | Meaning |
|--------|---------|
| `AWS_ROLE_ARN` | IAM role ARN for OIDC |
| `TF_STATE_BUCKET` | S3 bucket name |
| `TF_STATE_LOCK_TABLE` | DynamoDB table |
| `TF_ADMIN_CIDR` | Pin SG for SSH/6443 (optional) |

Also creates GitHub Environment **`aws-prod`** (used by apply job).

### 5) Push workflow

```bash
git add .github/workflows/terraform.yml terraform/
git commit -m "Add Terraform CI with OIDC and S3 state"
git push
```

Open a PR touching `terraform/` → see Plan. Merge → Apply.

---

## Day-to-day

1. Branch → edit `terraform/*.tf`
2. PR → review plan in Actions
3. Merge `main` → apply (or Actions → Terraform → `apply`)

Destroy still careful: prefer `workflow_dispatch` with a dedicated destroy job later, or local with same OIDC/role.
