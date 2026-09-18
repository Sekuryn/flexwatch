# Policies OPA/Rego appliquées au PLAN Terraform, avant tout apply.
#
#   terraform show -json tfplan > tfplan.json
#   conftest test --policy policy/terraform tfplan.json
#
# Pourquoi sur le plan et pas sur le code HCL : le plan est l'état cible réel,
# variables et modules résolus. Une policy qui lit le HCL se fait contourner
# par une variable ; une policy qui lit le plan voit ce qui va vraiment être
# créé.
#
# Syntaxe Rego v1 (OPA >= 1.0) : `if` obligatoire, `contains` pour les règles
# d'ensemble partielles.
package terraform.security

import rego.v1

# ----------------------------------------------------------------- helpers

# Seules les ressources créées ou modifiées nous intéressent : inutile de
# refuser un apply à cause d'une ressource en cours de suppression.
changed(rc) if {
	some action in rc.change.actions
	action in {"create", "update"}
}

resources(kind) := [rc |
	some rc in input.resource_changes
	rc.type == kind
	changed(rc)
]

# Tags exigés sur toute ressource taguable : sans eux, impossible d'attribuer
# un coût ni de faire le ménage.
required_tags := {"Project", "Environment", "ManagedBy"}

# `ecr:GetAuthorizationToken` n'accepte pas de restriction de ressource côté
# API IAM : c'est la seule wildcard tolérée, et elle est documentée.
allowed_wildcard_actions := {"ecr:GetAuthorizationToken"}

# ------------------------------------------------------- chiffrement au repos

deny contains msg if {
	some rc in resources("aws_instance")
	some device in rc.change.after.root_block_device
	device.encrypted != true
	msg := sprintf("%s: volume racine non chiffre. Un snapshot ou un disque recupere serait lisible en clair.", [rc.address])
}

deny contains msg if {
	some rc in resources("aws_ebs_volume")
	rc.change.after.encrypted != true
	msg := sprintf("%s: volume EBS non chiffre.", [rc.address])
}

# -------------------------------------------------------------------- IMDSv2

deny contains msg if {
	some rc in resources("aws_instance")
	some opts in rc.change.after.metadata_options
	opts.http_tokens != "required"
	msg := sprintf("%s: IMDSv2 non obligatoire (http_tokens != \"required\"). Une SSRF dans l'app pourrait lire les credentials de l'instance.", [rc.address])
}

deny contains msg if {
	some rc in resources("aws_instance")
	object.get(rc.change.after, "metadata_options", []) == []
	msg := sprintf("%s: metadata_options absent, IMDSv1 reste actif par defaut.", [rc.address])
}

warn contains msg if {
	some rc in resources("aws_instance")
	some opts in rc.change.after.metadata_options
	opts.http_put_response_hop_limit > 1
	msg := sprintf("%s: hop limit IMDS > 1, un conteneur peut atteindre l'IMDS a travers le bridge reseau.", [rc.address])
}

# ------------------------------------------------------------ exposition reseau

deny contains msg if {
	some rc in resources("aws_security_group")
	some rule in rc.change.after.ingress
	"0.0.0.0/0" in rule.cidr_blocks
	msg := sprintf("%s: regle d'entree depuis 0.0.0.0/0 sur le port %d. Administration attendue via SSM Session Manager, pas via un port ouvert.", [rc.address, rule.from_port])
}

deny contains msg if {
	some rc in resources("aws_vpc_security_group_ingress_rule")
	rc.change.after.cidr_ipv4 == "0.0.0.0/0"
	msg := sprintf("%s: regle d'entree depuis 0.0.0.0/0.", [rc.address])
}

# SSH ouvert, même restreint, est un choix à justifier explicitement.
deny contains msg if {
	some rc in resources("aws_vpc_security_group_ingress_rule")
	rc.change.after.from_port == 22
	msg := sprintf("%s: port 22 ouvert. Ce projet n'utilise pas SSH (ni cle a gerer, ni port a exposer).", [rc.address])
}

# --------------------------------------------------------------- IAM wildcards

deny contains msg if {
	some rc in resources("aws_iam_role_policy")
	doc := json.unmarshal(rc.change.after.policy)
	some stmt in doc.Statement
	stmt.Effect == "Allow"
	some action in actions_of(stmt)
	action == "*"
	msg := sprintf("%s: policy IAM avec Action \"*\".", [rc.address])
}

deny contains msg if {
	some rc in resources("aws_iam_role_policy")
	doc := json.unmarshal(rc.change.after.policy)
	some stmt in doc.Statement
	stmt.Effect == "Allow"
	resources_of(stmt)[_] == "*"

	# Une wildcard de ressource n'est acceptable que si TOUTES les actions du
	# statement figurent dans la liste blanche documentée.
	some action in actions_of(stmt)
	not action in allowed_wildcard_actions
	msg := sprintf("%s: Resource \"*\" pour l'action %q. Porter la policy a l'ARN concerne.", [rc.address, action])
}

# `as` serait un mot reserve (import ... as ...) : nommer la variable
# autrement n'est pas cosmetique, c'est une erreur de parsing en moins.
# (Et en Rego le commentaire est `#` : `//` est une division.)
actions_of(stmt) := list if {
	is_array(stmt.Action)
	list := stmt.Action
}

actions_of(stmt) := [stmt.Action] if is_string(stmt.Action)

resources_of(stmt) := rs if {
	is_array(stmt.Resource)
	rs := stmt.Resource
}

resources_of(stmt) := [stmt.Resource] if is_string(stmt.Resource)

# ------------------------------------------------------------------- registre

deny contains msg if {
	some rc in resources("aws_ecr_repository")
	rc.change.after.image_tag_mutability != "IMMUTABLE"
	msg := sprintf("%s: tags mutables. Un tag reecrit invalide la signature verifiee au deploiement.", [rc.address])
}

warn contains msg if {
	some rc in resources("aws_ecr_repository")
	some cfg in rc.change.after.image_scanning_configuration
	cfg.scan_on_push != true
	msg := sprintf("%s: scan_on_push desactive.", [rc.address])
}

# --------------------------------------------------------------------- secrets

# La règle la plus importante du lot : un secret passé à Terraform finit en
# clair dans l'état distant, lisible par quiconque accède au bucket.
deny contains msg if {
	some rc in resources("aws_secretsmanager_secret_version")
	rc.change.after.secret_string
	not startswith(rc.change.after.secret_string, "REMPLACER")
	msg := sprintf("%s: valeur de secret dans le plan Terraform. L'etat distant la stockerait en clair : renseigner le secret avec l'AWS CLI.", [rc.address])
}

deny contains msg if {
	some rc in resources("aws_ssm_parameter")
	rc.change.after.type != "SecureString"
	contains(lower(rc.change.after.name), "token")
	msg := sprintf("%s: parametre nomme comme un secret mais de type %q.", [rc.address, rc.change.after.type])
}

# ---------------------------------------------------------------------- logs

deny contains msg if {
	some rc in resources("aws_cloudwatch_log_group")
	not rc.change.after.retention_in_days
	msg := sprintf("%s: pas de retention definie, les logs sont conserves indefiniment (et factures).", [rc.address])
}

# ----------------------------------------------------------------------- tags

deny contains msg if {
	some rc in input.resource_changes
	changed(rc)
	taggable(rc.type)
	tags := object.get(rc.change.after, "tags_all", {})
	missing := required_tags - object.keys(tags)
	count(missing) > 0
	msg := sprintf("%s: tags manquants %v.", [rc.address, missing])
}

taggable(kind) if {
	kind in {
		"aws_instance",
		"aws_vpc",
		"aws_subnet",
		"aws_security_group",
		"aws_ecr_repository",
		"aws_secretsmanager_secret",
		"aws_cloudwatch_log_group",
	}
}
