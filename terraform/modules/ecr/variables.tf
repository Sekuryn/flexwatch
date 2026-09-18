variable "name" {
  description = "Nom du depot ECR."
  type        = string
}

variable "kms_key_arn" {
  description = "Cle KMS de chiffrement (null = chiffrement AES256 gere par AWS)."
  type        = string
  default     = null
}

variable "allowed_pull_principals" {
  description = "ARN des principals autorises a pull (role de l'instance)."
  type        = list(string)
  default     = []
}

variable "tags" {
  description = "Tags communs."
  type        = map(string)
  default     = {}
}
