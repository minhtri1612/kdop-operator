variable "aws_region" {
  description = "AWS region (default ap-southeast-2 / Sydney)."
  type        = string
  default     = "ap-southeast-2"
}

variable "name_prefix" {
  description = "Name prefix for resources"
  type        = string
  default     = "kdop-control"
}

variable "instance_type" {
  description = "EC2 instance type"
  type        = string
  default     = "t3.medium"
}

variable "worker_enabled" {
  description = "Create a second EC2 instance as a fake remote Docker VPS"
  type        = bool
  default     = false
}

variable "worker_instance_type" {
  description = "EC2 instance type for the fake remote Docker VPS"
  type        = string
  default     = "t3.small"
}

variable "use_spot" {
  description = "Use Spot instance (cheaper, can be interrupted)"
  type        = bool
  default     = true
}

variable "spot_max_price" {
  description = "Max Spot price (empty = on-demand price cap)"
  type        = string
  default     = ""
}

variable "root_volume_gb" {
  description = "Root gp3 volume size (GB)"
  type        = number
  default     = 30
}

variable "worker_root_volume_gb" {
  description = "Root gp3 volume size (GB) for the fake remote Docker VPS"
  type        = number
  default     = 20
}

variable "admin_cidr" {
  description = "CIDR allowed for SSH + K8s API. Empty = auto-detect your public IP /32."
  type        = string
  default     = ""
}

variable "gateway_cidrs" {
  description = "CIDRs allowed to dial Tunnel Gateway NodePort 30000"
  type        = list(string)
  default     = ["0.0.0.0/0"]
}

variable "argocd_cidrs" {
  description = "CIDRs allowed to reach ArgoCD UI on host port 8080 (port-forward)"
  type        = list(string)
  default     = ["0.0.0.0/0"]
}

variable "enable_ssm" {
  description = "Attach IAM instance profile for AWS Systems Manager Session Manager (no SSH key needed)"
  type        = bool
  default     = true
}

variable "enable_ssh" {
  description = "Open TCP/22 from admin_cidr (needs key_name or create_key_pair)"
  type        = bool
  default     = false
}

variable "key_name" {
  description = "Existing EC2 key pair name for SSH. Null/empty = SSM-only (recommended)."
  type        = string
  default     = null
  nullable    = true
}

variable "ssh_public_key" {
  description = "If create_key_pair=true, create key pair from this public key"
  type        = string
  default     = ""
}

variable "create_key_pair" {
  description = "Create aws_key_pair from ssh_public_key"
  type        = bool
  default     = false
}
