terraform {
  required_version = ">= 1.10"

  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 5.60"
    }
  }

  // État distant : chiffré, versionné, verrouillé.
  //
  // Le bucket et sa politique sont créés UNE FOIS À LA MAIN (poule et oeuf :
  // Terraform ne peut pas gérer le backend qu'il utilise). Les commandes sont
  // dans PLAN.md phase 5.
  //
  // `use_lockfile` (Terraform >= 1.10) remplace la table DynamoDB : le verrou
  // est un objet S3. Une dépendance de moins, un coût de moins.
  backend "s3" {
    bucket       = "REMPLACER-flexwatch-tfstate"
    key          = "prod/terraform.tfstate"
    region       = "ca-central-1"
    encrypt      = true
    use_lockfile = true
  }
}

provider "aws" {
  region = var.region

  // Tags appliqués à TOUTE ressource supportant les tags : indispensable pour
  // retrouver ce qui appartient au projet le jour où l'on veut tout détruire,
  // et pour le suivi des coûts.
  default_tags {
    tags = {
      Project     = "flexwatch"
      Environment = "prod"
      ManagedBy   = "terraform"
      Repository  = "github.com/Sekuryn/flexwatch"
    }
  }
}
