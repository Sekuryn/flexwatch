// Racine prod — câblage des modules.
//
// À APPLIQUER À LA MAIN, dans l'ordre décrit dans PLAN.md phase 5 :
//   init -> fmt -> validate -> plan -> conftest -> apply

locals {
  name = "flexwatch"

  tags = {
    Owner = var.owner
  }
}

module "network" {
  source = "../../modules/network"

  name                     = local.name
  availability_zone        = var.availability_zone
  vpc_cidr                 = var.vpc_cidr
  public_subnet_cidr       = var.public_subnet_cidr
  enable_flow_logs         = true
  flow_logs_retention_days = 14

  tags = local.tags
}

module "ecr" {
  source = "../../modules/ecr"

  name = local.name

  // Volontairement vide : l'autorisation de pull est portée par la policy
  // IAM du rôle de l'instance (identity-based), ce qui suffit en intra-compte.
  // Croiser les deux (policy de dépôt <-> rôle) créerait un cycle entre les
  // modules, et une policy de dépôt ne sert vraiment qu'en cross-account.
  allowed_pull_principals = []

  tags = local.tags
}

// Secret du bot : la ressource est créée par Terraform, mais sa VALEUR est
// écrite à la main avec l'AWS CLI (cf. PLAN.md phase 6). Mettre le token dans
// une variable Terraform le ferait atterrir en clair dans l'état distant.
resource "aws_secretsmanager_secret" "telegram" {
  name        = "flexwatch/telegram"
  description = "Token du bot Telegram et chat id (valeur renseignee hors Terraform)"

  // 7 jours de fenêtre de récupération : une suppression accidentelle reste
  // réversible.
  recovery_window_in_days = 7

  tags = local.tags
}

module "compute" {
  source = "../../modules/compute"

  name              = local.name
  instance_type     = var.instance_type
  subnet_id         = module.network.public_subnet_id
  security_group_id = module.network.security_group_id
  root_volume_size  = 30

  ecr_repository_arn = module.ecr.repository_arn
  secret_arns        = [aws_secretsmanager_secret.telegram.arn]

  user_data = templatefile("${path.module}/user_data.sh", {
    k3s_version          = var.k3s_version
    k3s_installer_sha256 = var.k3s_installer_sha256
  })

  allocate_eip = true
  tags         = local.tags
}
