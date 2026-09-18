output "instance_id" {
  description = "Identifiant de l'instance (a utiliser avec aws ssm start-session)."
  value       = aws_instance.app.id
}

output "private_ip" {
  description = "IP privee de l'instance."
  value       = aws_instance.app.private_ip
}

output "public_ip" {
  description = "IP publique (EIP si allouee)."
  value       = var.allocate_eip ? aws_eip.app[0].public_ip : aws_instance.app.public_ip
}

output "iam_role_name" {
  description = "Nom du role IAM de l'instance."
  value       = aws_iam_role.instance.name
}
