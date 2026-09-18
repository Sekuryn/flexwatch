output "repository_arn" {
  description = "ARN du depot."
  value       = aws_ecr_repository.this.arn
}

output "repository_url" {
  description = "URL du depot (a utiliser dans docker tag/push)."
  value       = aws_ecr_repository.this.repository_url
}
