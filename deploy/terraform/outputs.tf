output "public_ip" {
  description = "Static public (Elastic) IP of the rendezvous node."
  value       = aws_eip.node.public_ip
}

output "instance_id" {
  description = "EC2 instance ID (useful for SSM Session Manager)."
  value       = aws_instance.node.id
}

output "access" {
  description = "How to get a shell on the node."
  value = var.ssh_key_name != "" && var.ssh_cidr != "" ? join("\n", [
    "ssh ec2-user@${aws_eip.node.public_ip}",
    "  # or: aws ssm start-session --target ${aws_instance.node.id} --region ${var.aws_region}",
    ]) : join("\n", [
    "aws ssm start-session --target ${aws_instance.node.id} --region ${var.aws_region}",
    "  # (key-based SSH is disabled; set ssh_key_name + ssh_cidr to enable it)",
  ])
}

output "next_steps" {
  description = "What to do after apply."
  value       = <<-EOT
    Wait ~1 minute for the node to finish installing, then read the
    client-ready bootstrap address:

      aws ssm start-session --target ${aws_instance.node.id} --region ${var.aws_region}
      sudo journalctl -u gochat-rendezvous -n 40 --no-pager

    Look for the line starting with `client: gochat -bootstrap "..."`.
  EOT
}
