resource "aws_vpc" "this" {
  cidr_block           = "10.42.0.0/16"
  enable_dns_hostnames = true
  enable_dns_support   = true

  tags = {
    Name = "${local.name}-vpc"
  }
}

resource "aws_internet_gateway" "this" {
  vpc_id = aws_vpc.this.id

  tags = {
    Name = "${local.name}-igw"
  }
}

resource "aws_subnet" "public" {
  vpc_id                  = aws_vpc.this.id
  cidr_block              = "10.42.1.0/24"
  availability_zone       = data.aws_availability_zones.available.names[0]
  map_public_ip_on_launch = true

  tags = {
    Name = "${local.name}-public"
  }
}

resource "aws_route_table" "public" {
  vpc_id = aws_vpc.this.id

  route {
    cidr_block = "0.0.0.0/0"
    gateway_id = aws_internet_gateway.this.id
  }

  tags = {
    Name = "${local.name}-public-rt"
  }
}

resource "aws_route_table_association" "public" {
  subnet_id      = aws_subnet.public.id
  route_table_id = aws_route_table.public.id
}

resource "aws_security_group" "control" {
  name        = "${local.name}-sg"
  description = "kdop control plane: SSM (no SSH by default), Kind API, Tunnel Gateway"
  vpc_id      = aws_vpc.this.id

  dynamic "ingress" {
    for_each = var.enable_ssh ? [1] : []
    content {
      description = "SSH from admin"
      from_port   = 22
      to_port     = 22
      protocol    = "tcp"
      cidr_blocks = [local.admin_cidr]
    }
  }

  # Kind / kube-apiserver (mapped on host; adjust if your kind-config differs)
  ingress {
    description = "Kubernetes API from admin"
    from_port   = 6443
    to_port     = 6443
    protocol    = "tcp"
    cidr_blocks = [local.admin_cidr]
  }

  # Tunnel Gateway NodePort
  ingress {
    description = "Tunnel gateway NodePort"
    from_port   = 30000
    to_port     = 30000
    protocol    = "tcp"
    cidr_blocks = var.gateway_cidrs
  }

  egress {
    from_port   = 0
    to_port     = 0
    protocol    = "-1"
    cidr_blocks = ["0.0.0.0/0"]
  }

  tags = {
    Name = "${local.name}-sg"
  }
}

resource "aws_iam_role" "ssm" {
  count = var.enable_ssm ? 1 : 0

  name = "${local.name}-ssm"

  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Action    = "sts:AssumeRole"
      Effect    = "Allow"
      Principal = { Service = "ec2.amazonaws.com" }
    }]
  })

  tags = {
    Name = "${local.name}-ssm"
  }
}

resource "aws_iam_role_policy_attachment" "ssm_core" {
  count = var.enable_ssm ? 1 : 0

  role       = aws_iam_role.ssm[0].name
  policy_arn = "arn:aws:iam::aws:policy/AmazonSSMManagedInstanceCore"
}

resource "aws_iam_instance_profile" "ssm" {
  count = var.enable_ssm ? 1 : 0

  name = "${local.name}-ssm"
  role = aws_iam_role.ssm[0].name
}

resource "aws_key_pair" "this" {
  count = var.create_key_pair ? 1 : 0

  key_name   = coalesce(var.key_name, local.name)
  public_key = var.ssh_public_key
}

locals {
  resolved_key_name = var.create_key_pair ? aws_key_pair.this[0].key_name : (
    var.key_name != null && var.key_name != "" ? var.key_name : null
  )
}

resource "aws_instance" "control" {
  ami                         = data.aws_ami.ubuntu.id
  instance_type               = var.instance_type
  subnet_id                   = aws_subnet.public.id
  vpc_security_group_ids      = [aws_security_group.control.id]
  key_name                    = local.resolved_key_name
  iam_instance_profile        = var.enable_ssm ? aws_iam_instance_profile.ssm[0].name : null
  associate_public_ip_address = true

  root_block_device {
    volume_type           = "gp3"
    volume_size           = var.root_volume_gb
    delete_on_termination = true
  }

  user_data = templatefile("${path.module}/user_data.sh", {
    hostname = local.name
  })

  dynamic "instance_market_options" {
    for_each = var.use_spot ? [1] : []
    content {
      market_type = "spot"
      spot_options {
        spot_instance_type             = "one-time"
        instance_interruption_behavior = "terminate"
        max_price                      = var.spot_max_price != "" ? var.spot_max_price : null
      }
    }
  }

  tags = {
    Name = local.name
    Role = "kdop-control-plane"
  }

  lifecycle {
    # ignore AMI drift only; allow user_data replace when bootstrap script changes
    ignore_changes = [ami]
  }

  depends_on = [aws_iam_role_policy_attachment.ssm_core]
}
