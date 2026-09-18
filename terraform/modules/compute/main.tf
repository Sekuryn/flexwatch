// Module compute — une instance ARM (Graviton) qui héberge k3s + flexwatch.
//
// Sécurité, points saillants :
//   - IMDSv2 OBLIGATOIRE (http_tokens = "required"). C'est la contre-mesure
//     directe au SSRF : sans elle, une requête HTTP forgée depuis le bot peut
//     lire les credentials de l'instance sur 169.254.169.254.
//   - volume racine chiffré, pas de clé SSH, administration par SSM.
//   - rôle IAM sans aucune wildcard : chaque action est portée à une ressource.

terraform {
  required_version = ">= 1.9"
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 5.60"
    }
  }
}

// L'AMI n'est pas codée en dur : un ID d'AMI est régional et devient obsolète
// à chaque patch. SSM donne toujours la dernière Amazon Linux 2023 arm64.
data "aws_ssm_parameter" "al2023_arm64" {
  name = "/aws/service/ami-amazon-linux-latest/al2023-ami-kernel-6.1-arm64"
}

resource "aws_iam_role" "instance" {
  name        = "${var.name}-instance"
  description = "Role de l'instance flexwatch (least privilege)"

  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect    = "Allow"
      Principal = { Service = "ec2.amazonaws.com" }
      Action    = "sts:AssumeRole"
    }]
  })

  tags = var.tags
}

// SSM Session Manager : l'accès shell sans port 22 ouvert, avec journalisation
// des sessions côté AWS. C'est la seule policy managée que l'on accepte ici.
resource "aws_iam_role_policy_attachment" "ssm" {
  role       = aws_iam_role.instance.name
  policy_arn = "arn:aws:iam::aws:policy/AmazonSSMManagedInstanceCore"
}

// Pull d'images depuis ECR. GetAuthorizationToken ne peut pas être portée à
// une ressource (limite de l'API IAM) ; tout le reste l'est strictement.
resource "aws_iam_role_policy" "ecr_pull" {
  count = var.ecr_repository_arn == null ? 0 : 1

  name = "ecr-pull-only"
  role = aws_iam_role.instance.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Sid      = "AuthToken"
        Effect   = "Allow"
        Action   = "ecr:GetAuthorizationToken"
        Resource = "*"
      },
      {
        Sid    = "PullOnly"
        Effect = "Allow"
        Action = [
          "ecr:BatchCheckLayerAvailability",
          "ecr:BatchGetImage",
          "ecr:GetDownloadUrlForLayer",
        ]
        // Aucune action d'écriture : une instance compromise ne peut pas
        // pousser une image piégée dans le registre.
        Resource = var.ecr_repository_arn
      },
    ]
  })
}

// Lecture du seul secret dont l'app a besoin.
resource "aws_iam_role_policy" "secrets_read" {
  count = length(var.secret_arns) == 0 ? 0 : 1

  name = "read-app-secrets"
  role = aws_iam_role.instance.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect   = "Allow"
      Action   = ["secretsmanager:GetSecretValue", "secretsmanager:DescribeSecret"]
      Resource = var.secret_arns
    }]
  })
}

resource "aws_iam_instance_profile" "instance" {
  name = "${var.name}-instance"
  role = aws_iam_role.instance.name
  tags = var.tags
}

resource "aws_instance" "app" {
  ami                    = data.aws_ssm_parameter.al2023_arm64.value
  instance_type          = var.instance_type
  subnet_id              = var.subnet_id
  vpc_security_group_ids = [var.security_group_id]
  iam_instance_profile   = aws_iam_instance_profile.instance.name

  // Pas de key_name : aucune clé SSH à protéger, à distribuer ou à révoquer.
  key_name = null

  metadata_options {
    http_endpoint = "enabled"
    // IMDSv2 uniquement.
    http_tokens = "required"
    // 1 saut : un conteneur ne peut pas atteindre l'IMDS à travers le bridge.
    http_put_response_hop_limit = 1
    instance_metadata_tags      = "disabled"
  }

  root_block_device {
    volume_type = "gp3"
    volume_size = var.root_volume_size
    encrypted   = true
    // Suppression avec l'instance : pas de volume orphelin qui traîne avec
    // des logs dessus (et qui coûte).
    delete_on_termination = true
  }

  // Le user_data ne contient AUCUN secret : il installe k3s, rien d'autre.
  // Les secrets arrivent par Secrets Manager au démarrage du pod.
  user_data                   = var.user_data
  user_data_replace_on_change = false

  monitoring = true

  tags = merge(var.tags, { Name = "${var.name}-app" })

  lifecycle {
    // L'AMI évolue à chaque patch Amazon ; un `plan` ne doit pas proposer de
    // recréer l'instance (et donc de perdre le cluster k3s) à chaque fois.
    // Les mises à jour de l'OS se font par `dnf upgrade`, la recréation est
    // une décision explicite (taint + apply).
    ignore_changes = [ami]
  }
}

resource "aws_eip" "app" {
  count = var.allocate_eip ? 1 : 0

  instance = aws_instance.app.id
  domain   = "vpc"
  tags     = merge(var.tags, { Name = "${var.name}-eip" })
}
