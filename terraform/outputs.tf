output "region" {
  value = var.aws_region
}

output "instance_id" {
  value = aws_instance.control.id
}

output "public_ip" {
  value = aws_instance.control.public_ip
}

output "public_dns" {
  value = aws_instance.control.public_dns
}

output "ssm_command" {
  value = var.enable_ssm ? "aws ssm start-session --target ${aws_instance.control.id} --region ${var.aws_region}" : null
}

output "ssh_command" {
  value = local.resolved_key_name != null ? "ssh -i <your-key.pem> ubuntu@${aws_instance.control.public_ip}" : "(SSM-only — use ssm_command)"
}

output "admin_cidr" {
  value = local.admin_cidr
}

output "security_group_id" {
  value = aws_security_group.control.id
}

output "worker_instance_id" {
  value = var.worker_enabled ? aws_instance.worker[0].id : null
}

output "worker_public_ip" {
  value = var.worker_enabled ? aws_instance.worker[0].public_ip : null
}

output "worker_ssm_command" {
  value = var.worker_enabled && var.enable_ssm ? "aws ssm start-session --target ${aws_instance.worker[0].id} --region ${var.aws_region}" : null
}

output "next_steps" {
  value = <<-EOT
    1. Wait ~3-5 min for user_data (SSM agent + Docker + kind + kubectl).
    2. Connect:
         aws ssm start-session --target ${aws_instance.control.id} --region ${var.aws_region}
       (need Session Manager plugin: https://docs.aws.amazon.com/systems-manager/latest/userguide/session-manager-working-with-install-plugin.html)
    3. git clone https://github.com/minhtri1612/kdop-operator.git && cd kdop-operator
    4. sudo bash /opt/kdop/bootstrap.sh
    5. make docker-build && kind load ... && kubectl apply -f install/install.yaml
    Tunnel clients dial: ws://${aws_instance.control.public_ip}:30000/...
    Optional fake remote VPS:
      terraform output -raw worker_instance_id
      terraform output -raw worker_ssm_command
  EOT
}
