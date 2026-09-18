// Module ecr — registre privé pour l'image flexwatch.
//
// Utile même si la CI pousse sur GHCR : depuis l'instance, ECR se pull avec le
// rôle IAM, sans stocker de credentials de registre sur la machine.

terraform {
  required_version = ">= 1.9"
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 5.60"
    }
  }
}

resource "aws_ecr_repository" "this" {
  name = var.name

  // IMMUTABLE : un tag poussé ne peut plus être réécrit. Sans ça, `:v1.0.0`
  // peut désigner une image différente demain, et la signature vérifiée au
  // déploiement ne prouve plus rien.
  image_tag_mutability = "IMMUTABLE"

  image_scanning_configuration {
    // Scan à chaque push : c'est le filet de sécurité si la CI est
    // contournée (push manuel depuis un poste, par exemple).
    scan_on_push = true
  }

  encryption_configuration {
    encryption_type = var.kms_key_arn == null ? "AES256" : "KMS"
    kms_key         = var.kms_key_arn
  }

  force_delete = false

  tags = var.tags
}

// Sans lifecycle policy, ECR accumule des centaines d'images à 0,10 USD/Go/mois.
resource "aws_ecr_lifecycle_policy" "this" {
  repository = aws_ecr_repository.this.name

  policy = jsonencode({
    rules = [
      {
        rulePriority = 1
        description  = "Garder les 10 dernieres images versionnees"
        selection = {
          tagStatus     = "tagged"
          tagPrefixList = ["v"]
          countType     = "imageCountMoreThan"
          countNumber   = 10
        }
        action = { type = "expire" }
      },
      {
        rulePriority = 2
        description  = "Purger les images sans tag apres 7 jours"
        selection = {
          tagStatus   = "untagged"
          countType   = "sinceImagePushed"
          countUnit   = "days"
          countNumber = 7
        }
        action = { type = "expire" }
      },
    ]
  })
}

// Politique de dépôt : seul le rôle de l'instance peut pull.
resource "aws_ecr_repository_policy" "pull_only" {
  count = length(var.allowed_pull_principals) == 0 ? 0 : 1

  repository = aws_ecr_repository.this.name

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Sid       = "AllowPullFromInstanceRole"
      Effect    = "Allow"
      Principal = { AWS = var.allowed_pull_principals }
      Action = [
        "ecr:BatchCheckLayerAvailability",
        "ecr:BatchGetImage",
        "ecr:GetDownloadUrlForLayer",
      ]
    }]
  })
}
