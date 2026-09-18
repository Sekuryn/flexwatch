variable "name" {
  description = "Prefixe de nommage des ressources."
  type        = string

  validation {
    condition     = can(regex("^[a-z][a-z0-9-]{1,30}$", var.name))
    error_message = "name doit etre en minuscules, chiffres et tirets (2 a 31 caracteres)."
  }
}

variable "vpc_cidr" {
  description = "CIDR du VPC."
  type        = string
  default     = "10.42.0.0/16"
}

variable "public_subnet_cidr" {
  description = "CIDR du subnet public."
  type        = string
  default     = "10.42.1.0/24"
}

variable "availability_zone" {
  description = "AZ du subnet (ex: ca-central-1a)."
  type        = string
}

variable "enable_flow_logs" {
  description = "Active les VPC Flow Logs vers CloudWatch."
  type        = bool
  default     = true
}

variable "flow_logs_retention_days" {
  description = "Rétention des flow logs, en jours."
  type        = number
  default     = 14

  validation {
    # Une rétention infinie sur un projet perso, c'est une facture qui monte
    # sans que personne ne regarde les logs.
    condition     = var.flow_logs_retention_days >= 1 && var.flow_logs_retention_days <= 90
    error_message = "Garder la retention entre 1 et 90 jours pour ce projet."
  }
}

variable "tags" {
  description = "Tags communs."
  type        = map(string)
  default     = {}
}
