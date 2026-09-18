variable "name" {
  description = "Prefixe de nommage."
  type        = string
}

variable "instance_type" {
  description = "Type d'instance (ARM/Graviton attendu)."
  type        = string
  default     = "t4g.small"

  validation {
    condition     = startswith(var.instance_type, "t4g.") || startswith(var.instance_type, "m7g.") || startswith(var.instance_type, "c7g.")
    error_message = "Ce module cible Graviton (arm64) : l'image conteneur est construite pour linux/arm64."
  }
}

variable "subnet_id" {
  description = "Subnet d'accueil."
  type        = string
}

variable "security_group_id" {
  description = "Security group a attacher."
  type        = string
}

variable "root_volume_size" {
  description = "Taille du volume racine, en Gio."
  type        = number
  default     = 20

  validation {
    # k3s + images + Prometheus/Loki : sous 20 Gio on sature vite, au-dela de
    # 50 la facture n'est plus celle d'un projet perso.
    condition     = var.root_volume_size >= 20 && var.root_volume_size <= 50
    error_message = "Prevoir entre 20 et 50 Gio."
  }
}

variable "ecr_repository_arn" {
  description = "ARN du depot ECR a autoriser en lecture (null = pas d'acces ECR)."
  type        = string
  default     = null
}

variable "secret_arns" {
  description = "ARN des secrets Secrets Manager lisibles par l'instance."
  type        = list(string)
  default     = []
}

variable "user_data" {
  description = "Script de bootstrap (ne doit contenir aucun secret)."
  type        = string
  default     = null

  validation {
    # Garde-fou grossier mais utile : le user_data est lisible via l'IMDS par
    # tout process de l'instance, y compris un conteneur compromis.
    condition     = var.user_data == null || !can(regex("(?i)(TELEGRAM_TOKEN|AWS_SECRET_ACCESS_KEY|PRIVATE KEY)", var.user_data))
    error_message = "Le user_data semble contenir un secret : le user_data est lisible via l'IMDS, utiliser Secrets Manager."
  }
}

variable "associate_public_ip" {
  description = "Attribue une IP publique a l'instance (requis sans NAT Gateway)."
  type        = bool
  default     = true
}

variable "allocate_eip" {
  description = "Attache une IP elastique (adresse stable entre les reboots)."
  type        = bool
  default     = true
}

variable "tags" {
  description = "Tags communs."
  type        = map(string)
  default     = {}
}
