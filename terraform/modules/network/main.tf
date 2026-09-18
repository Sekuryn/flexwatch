// Module network — VPC minimal pour une instance unique.
//
// Choix assumés :
//   - un seul subnet PUBLIC, pas de NAT Gateway. Un NAT coûte ~35 USD/mois
//     pour un bot qui fait une requête HTTPS toutes les 20 s : le rapport
//     sécurité/prix ne le justifie pas ici. La contrepartie (IP publique sur
//     l'instance) est compensée par un security group SANS aucune règle
//     d'entrée et par l'accès via SSM Session Manager.
//   - aucun port d'entrée ouvert, y compris SSH (22). L'administration passe
//     par SSM, qui est sortant : rien à exposer, rien à brute-forcer.

terraform {
  required_version = ">= 1.9"
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 5.60"
    }
  }
}

resource "aws_vpc" "this" {
  cidr_block           = var.vpc_cidr
  enable_dns_support   = true
  enable_dns_hostnames = true

  tags = merge(var.tags, { Name = "${var.name}-vpc" })
}

resource "aws_internet_gateway" "this" {
  vpc_id = aws_vpc.this.id
  tags   = merge(var.tags, { Name = "${var.name}-igw" })
}

resource "aws_subnet" "public" {
  vpc_id                  = aws_vpc.this.id
  cidr_block              = var.public_subnet_cidr
  availability_zone       = var.availability_zone
  map_public_ip_on_launch = true

  tags = merge(var.tags, { Name = "${var.name}-public" })
}

resource "aws_route_table" "public" {
  vpc_id = aws_vpc.this.id

  route {
    cidr_block = "0.0.0.0/0"
    gateway_id = aws_internet_gateway.this.id
  }

  tags = merge(var.tags, { Name = "${var.name}-public-rt" })
}

resource "aws_route_table_association" "public" {
  subnet_id      = aws_subnet.public.id
  route_table_id = aws_route_table.public.id
}

// Security group : ZÉRO règle d'entrée.
//
// Un `aws_security_group` sans bloc ingress ne laisse rien entrer, ce qui est
// exactement le besoin : flexwatch n'est pas un serveur, c'est un worker
// sortant. /metrics reste joignable dans le cluster, et Grafana s'atteint via
// un tunnel SSM (`aws ssm start-session --document-name AWS-StartPortForwardingSession`).
resource "aws_security_group" "app" {
  name        = "${var.name}-app"
  description = "flexwatch: aucune entree, sorties HTTPS et DNS uniquement"
  vpc_id      = aws_vpc.this.id

  tags = merge(var.tags, { Name = "${var.name}-app" })

  lifecycle {
    create_before_destroy = true
  }
}

// Règles de sortie déclarées séparément : plus lisible dans un `plan` et
// modifiable sans recréer le groupe.
resource "aws_vpc_security_group_egress_rule" "https" {
  security_group_id = aws_security_group.app.id
  description       = "API Communauto, Telegram, ECR, SSM, mises a jour"
  cidr_ipv4         = "0.0.0.0/0"
  from_port         = 443
  to_port           = 443
  ip_protocol       = "tcp"
}

resource "aws_vpc_security_group_egress_rule" "dns_udp" {
  security_group_id = aws_security_group.app.id
  description       = "resolution DNS"
  cidr_ipv4         = "0.0.0.0/0"
  from_port         = 53
  to_port           = 53
  ip_protocol       = "udp"
}

resource "aws_vpc_security_group_egress_rule" "ntp" {
  security_group_id = aws_security_group.app.id
  description       = "NTP: une horloge decalee casse TLS et les signatures cosign"
  cidr_ipv4         = "0.0.0.0/0"
  from_port         = 123
  to_port           = 123
  ip_protocol       = "udp"
}

// Flow logs : sans eux, une exfiltration réseau ne laisse aucune trace. Ils
// répondent à la question « qu'est-ce qui est sorti de cette instance ? ».
resource "aws_flow_log" "vpc" {
  count = var.enable_flow_logs ? 1 : 0

  vpc_id               = aws_vpc.this.id
  traffic_type         = "ALL"
  log_destination_type = "cloud-watch-logs"
  log_destination      = aws_cloudwatch_log_group.flow[0].arn
  iam_role_arn         = aws_iam_role.flow[0].arn

  tags = merge(var.tags, { Name = "${var.name}-flow-logs" })
}

resource "aws_cloudwatch_log_group" "flow" {
  count = var.enable_flow_logs ? 1 : 0

  name              = "/aws/vpc/${var.name}/flow-logs"
  retention_in_days = var.flow_logs_retention_days
  tags              = var.tags
}

resource "aws_iam_role" "flow" {
  count = var.enable_flow_logs ? 1 : 0

  name = "${var.name}-flow-logs"
  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect    = "Allow"
      Principal = { Service = "vpc-flow-logs.amazonaws.com" }
      Action    = "sts:AssumeRole"
    }]
  })
  tags = var.tags
}

resource "aws_iam_role_policy" "flow" {
  count = var.enable_flow_logs ? 1 : 0

  name = "write-flow-logs"
  role = aws_iam_role.flow[0].id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect = "Allow"
      Action = [
        "logs:CreateLogStream",
        "logs:PutLogEvents",
        "logs:DescribeLogStreams",
      ]
      // Portée au seul log group de ce VPC : pas de "Resource": "*".
      Resource = "${aws_cloudwatch_log_group.flow[0].arn}:*"
    }]
  })
}
