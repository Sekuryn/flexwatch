# Tests des policies Terraform : `conftest verify --policy policy/`.
#
# Une policy non testée est une fausse assurance : celle qui ne refuse jamais
# rien passe la CI en silence. Chaque règle a donc un cas qui doit être refusé
# ET un cas conforme qui doit passer.
package terraform.security_test

import rego.v1

import data.terraform.security

# ------------------------------------------------------------------ fixtures

plan_with(changes) := {"resource_changes": changes}

good_tags := {
	"Project": "flexwatch",
	"Environment": "prod",
	"ManagedBy": "terraform",
}

compliant_instance := {
	"address": "module.compute.aws_instance.app",
	"type": "aws_instance",
	"change": {
		"actions": ["create"],
		"after": {
			"metadata_options": [{
				"http_tokens": "required",
				"http_put_response_hop_limit": 1,
			}],
			"root_block_device": [{"encrypted": true}],
			"tags_all": good_tags,
		},
	},
}

# -------------------------------------------------------------------- IMDSv2

test_imdsv1_refuse if {
	instance := json.patch(compliant_instance, [{
		"op": "replace",
		"path": "/change/after/metadata_options/0/http_tokens",
		"value": "optional",
	}])

	result := security.deny with input as plan_with([instance])
	count(result) == 1
}

test_metadata_options_absent_refuse if {
	instance := json.remove(compliant_instance, ["/change/after/metadata_options"])

	result := security.deny with input as plan_with([instance])
	count(result) == 1
}

test_hop_limit_eleve_averti if {
	instance := json.patch(compliant_instance, [{
		"op": "replace",
		"path": "/change/after/metadata_options/0/http_put_response_hop_limit",
		"value": 2,
	}])

	result := security.warn with input as plan_with([instance])
	count(result) == 1
}

# --------------------------------------------------------------- chiffrement

test_volume_racine_non_chiffre_refuse if {
	instance := json.patch(compliant_instance, [{
		"op": "replace",
		"path": "/change/after/root_block_device/0/encrypted",
		"value": false,
	}])

	result := security.deny with input as plan_with([instance])
	count(result) == 1
}

# ------------------------------------------------------------------- reseau

test_ingress_monde_entier_refuse if {
	sg := {
		"address": "aws_security_group.web",
		"type": "aws_security_group",
		"change": {"actions": ["create"], "after": {
			"ingress": [{"from_port": 22, "cidr_blocks": ["0.0.0.0/0"]}],
			"tags_all": good_tags,
		}},
	}

	result := security.deny with input as plan_with([sg])
	count(result) == 1
}

test_ssh_refuse if {
	rule := {
		"address": "aws_vpc_security_group_ingress_rule.ssh",
		"type": "aws_vpc_security_group_ingress_rule",
		"change": {"actions": ["create"], "after": {
			"cidr_ipv4": "10.0.0.0/8",
			"from_port": 22,
		}},
	}

	result := security.deny with input as plan_with([rule])
	count(result) == 1
}

# ---------------------------------------------------------------------- IAM

test_iam_action_wildcard_refuse if {
	policy := {
		"address": "aws_iam_role_policy.too_much",
		"type": "aws_iam_role_policy",
		"change": {"actions": ["create"], "after": {"policy": json.marshal({
			"Version": "2012-10-17",
			"Statement": [{
				"Effect": "Allow",
				"Action": "*",
				"Resource": "arn:aws:s3:::bucket/*",
			}],
		})}},
	}

	result := security.deny with input as plan_with([policy])
	count(result) >= 1
}

test_iam_resource_wildcard_refuse if {
	policy := {
		"address": "aws_iam_role_policy.wide",
		"type": "aws_iam_role_policy",
		"change": {"actions": ["create"], "after": {"policy": json.marshal({
			"Version": "2012-10-17",
			"Statement": [{
				"Effect": "Allow",
				"Action": ["secretsmanager:GetSecretValue"],
				"Resource": "*",
			}],
		})}},
	}

	result := security.deny with input as plan_with([policy])
	count(result) == 1
}

# L'exception documentée doit vraiment passer, sinon elle serait contournée
# par un `--ignore` global qui masquerait aussi les vrais problèmes.
test_iam_exception_get_authorization_token_acceptee if {
	policy := {
		"address": "aws_iam_role_policy.ecr_pull",
		"type": "aws_iam_role_policy",
		"change": {"actions": ["create"], "after": {"policy": json.marshal({
			"Version": "2012-10-17",
			"Statement": [{
				"Effect": "Allow",
				"Action": "ecr:GetAuthorizationToken",
				"Resource": "*",
			}],
		})}},
	}

	result := security.deny with input as plan_with([policy])
	count(result) == 0
}

# ------------------------------------------------------------------ secrets

test_secret_en_clair_dans_le_plan_refuse if {
	version := {
		"address": "aws_secretsmanager_secret_version.telegram",
		"type": "aws_secretsmanager_secret_version",
		"change": {"actions": ["create"], "after": {"secret_string": "123456:vrai-token"}},
	}

	result := security.deny with input as plan_with([version])
	count(result) == 1
}

# ------------------------------------------------------------------ registre

test_tags_mutables_refuse if {
	repo := {
		"address": "module.ecr.aws_ecr_repository.this",
		"type": "aws_ecr_repository",
		"change": {"actions": ["create"], "after": {
			"image_tag_mutability": "MUTABLE",
			"image_scanning_configuration": [{"scan_on_push": true}],
			"tags_all": good_tags,
		}},
	}

	result := security.deny with input as plan_with([repo])
	count(result) == 1
}

# --------------------------------------------------------------------- tags

test_tags_manquants_refuse if {
	instance := json.patch(compliant_instance, [{
		"op": "replace",
		"path": "/change/after/tags_all",
		"value": {"Project": "flexwatch"},
	}])

	result := security.deny with input as plan_with([instance])
	count(result) == 1
}

# ------------------------------------------------------ cas nominal conforme

test_instance_conforme_acceptee if {
	result := security.deny with input as plan_with([compliant_instance])
	count(result) == 0
}

# Une ressource en cours de suppression ne doit pas bloquer l'apply.
test_suppression_ignoree if {
	instance := json.patch(compliant_instance, [
		{"op": "replace", "path": "/change/actions", "value": ["delete"]},
		{"op": "replace", "path": "/change/after/root_block_device/0/encrypted", "value": false},
	])

	result := security.deny with input as plan_with([instance])
	count(result) == 0
}
