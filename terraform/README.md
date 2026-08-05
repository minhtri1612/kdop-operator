# Terraform — kdop control plane on AWS EC2 (Spot)

## Spec

| Item | Value |
|------|--------|
| Region | `ap-southeast-2` (Sydney) |
| Instance | `t3.medium` Spot |
| Disk | 30 GB gp3 |
| AMI | Ubuntu 22.04 |
| SG | 22 + 6443 (admin IP), 30000 (gateway) |

## Prereq

```bash
# AWS CLI configured for the same region
export AWS_DEFAULT_REGION=ap-southeast-2
aws sts get-caller-identity

# Key pair MUST exist in ap-southeast-2 (EC2 → Key pairs)
cp terraform.tfvars.example terraform.tfvars
# edit key_name / ssh_public_key
```

## Apply

```bash
cd terraform
terraform init
terraform plan
terraform apply
terraform output
```

## After apply

```bash
ssh ubuntu@$(terraform output -raw public_ip)

# wait until cloud-init done
ls /opt/kdop/READY

git clone https://github.com/minhtri1612/kdop-operator.git
cd kdop-operator
sudo bash /opt/kdop/bootstrap.sh

make docker-build
kind load docker-image kdop-operator:$(cat VERSION) --name kdop
kind load docker-image kdop-operator:latest --name kdop
# use your working install.yaml generator, then:
kubectl apply -f install/install.yaml
kubectl apply -f install/kind-setup.yaml
kubectl -n system apply -f config/rbac/leader_election_role.yaml
kubectl -n system apply -f config/rbac/leader_election_role_binding.yaml
```

Tunnel from remote Docker hosts:

```text
ws://<public_ip>:30000/ws?...
```

## Destroy (save money)

```bash
terraform destroy
```

Spot can be reclaimed anytime — OK for staging; use `use_spot = false` for demos.

## GitHub Actions (recommended)

Do **not** rely on laptop `terraform apply` for day-to-day. See [GITHUB_ACTIONS.md](./GITHUB_ACTIONS.md):

- PR → `terraform plan`
- merge `main` → `terraform apply` via OIDC
- State in S3 + DynamoDB lock
