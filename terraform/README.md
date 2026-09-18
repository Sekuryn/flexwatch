# Terraform — infrastructure flexwatch (référence)

> **Rien n'est appliqué depuis ce dépôt par une automatisation.** Ces fichiers
> sont la référence du runbook `PLAN.md` (phases 4 et 5) : c'est toi qui lances
> `init`, `plan`, `conftest`, puis `apply`, à la main, dans cet ordre.

## Arborescence

```
terraform/
├── envs/
│   └── prod/              # la seule racine que l'on applique
│       ├── backend.tf     # état distant S3 + verrou
│       ├── main.tf        # câblage des modules
│       ├── variables.tf
│       ├── outputs.tf
│       └── terraform.tfvars.example
└── modules/
    ├── network/           # VPC, subnet public, IGW, security group  (module d'exemple complet)
    ├── compute/           # EC2 ARM t4g.small + IAM least-privilege
    └── ecr/               # registre d'images privé + lifecycle policy
```

Un seul environnement (`prod`) : pour un projet portfolio, un `staging` qui ne
sert jamais est une complexité gratuite. La structure permet d'en ajouter un
sans rien réécrire.

## Ordre des commandes (à taper soi-même)

```bash
cd terraform/envs/prod
cp terraform.tfvars.example terraform.tfvars   # puis remplir
terraform init
terraform fmt -check -recursive ../..
terraform validate
terraform plan -out=tfplan

# Contrôle de conformité AVANT tout apply (cf. policy/terraform)
terraform show -json tfplan > tfplan.json
conftest test --policy ../../../policy/terraform tfplan.json

terraform apply tfplan
```

`tfplan.json` contient l'état cible en clair : il est dans `.gitignore`, et il
doit être supprimé après usage (`rm tfplan tfplan.json`).

## Pourquoi l'état est distant

L'état Terraform contient des valeurs sensibles (ARN, identifiants de
ressources, parfois des secrets référencés). En local, il finit par être perdu
ou committé. Dans S3 chiffré + versionné, il est sauvegardé, auditable, et
verrouillé pendant un `apply` — deux `apply` concurrents ne peuvent pas se
marcher dessus.
