variable "region" {
  description = "Region AWS. ca-central-1 (Montreal) : les donnees restent la ou est le service observe."
  type        = string
  default     = "ca-central-1"
}

variable "availability_zone" {
  description = "AZ de l'instance."
  type        = string
  default     = "ca-central-1a"
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

variable "instance_type" {
  description = "Type d'instance ARM."
  type        = string
  default     = "t4g.small"
}

variable "owner" {
  description = "Tag Owner (email ou pseudo), pour savoir a qui appartient la facture."
  type        = string
}

variable "k3s_version" {
  description = "Version de k3s a installer (epinglee : pas de 'latest' en prod)."
  type        = string
  default     = "v1.31.4+k3s1"
}

variable "k3s_installer_sha256" {
  description = <<-EOT
    Empreinte SHA256 du script get.k3s.io, verifiee avant execution.

    A relever soi-meme, depuis un poste de confiance :
      curl -sfL https://get.k3s.io | sha256sum

    Sans cette verification, le bootstrap execute en root un script telecharge
    sur Internet : c'est precisement le vecteur de compromission de chaine
    d'approvisionnement que le reste du projet cherche a eviter.
  EOT
  type        = string

  validation {
    condition     = can(regex("^[a-f0-9]{64}$", var.k3s_installer_sha256))
    error_message = "Attendu : 64 caracteres hexadecimaux (sortie de sha256sum)."
  }
}
