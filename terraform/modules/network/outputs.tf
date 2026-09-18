output "vpc_id" {
  description = "Identifiant du VPC."
  value       = aws_vpc.this.id
}

output "public_subnet_id" {
  description = "Identifiant du subnet public."
  value       = aws_subnet.public.id
}

output "security_group_id" {
  description = "Security group de l'application (aucune entree)."
  value       = aws_security_group.app.id
}
