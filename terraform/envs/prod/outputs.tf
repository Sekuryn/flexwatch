output "instance_id" {
  description = "Instance a joindre via : aws ssm start-session --target <id>"
  value       = module.compute.instance_id
}

output "public_ip" {
  description = "IP publique de l'instance."
  value       = module.compute.public_ip
}

output "ecr_repository_url" {
  description = "URL du depot ECR pour docker tag/push."
  value       = module.ecr.repository_url
}

output "telegram_secret_arn" {
  description = "ARN du secret Telegram (valeur a renseigner hors Terraform)."
  value       = aws_secretsmanager_secret.telegram.arn
}

output "next_steps" {
  description = "Rappel des etapes manuelles qui suivent l'apply."
  value       = <<-EOT
    1. Renseigner le secret (la valeur n'est jamais dans Terraform) :
       aws secretsmanager put-secret-value --secret-id ${aws_secretsmanager_secret.telegram.arn} \
         --secret-string '{"token":"...","chat_id":"..."}'
    2. Ouvrir une session : aws ssm start-session --target ${module.compute.instance_id}
    3. Verifier k3s : sudo k3s kubectl get nodes
    4. Suite du runbook : PLAN.md phase 4.
  EOT
}
