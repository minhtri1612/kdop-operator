# Remote state — fill bucket/table after one-time bootstrap (see README).
# Local: terraform init -backend-config=backend.hcl
# CI:    uses the same backend.hcl committed as backend.hcl.ci OR secrets

terraform {
  backend "s3" {
    # Set via backend.hcl / -backend-config (do not hardcode secrets here)
    # bucket         = "kdop-tfstate-<account-id>"
    # key            = "control-plane/terraform.tfstate"
    # region         = "ap-southeast-2"
    # dynamodb_table = "kdop-tfstate-lock"
    # encrypt        = true
  }
}
