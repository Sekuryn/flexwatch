package kubernetes.workload_test

import rego.v1

import data.kubernetes.workload

hardened_deployment := {
	"kind": "Deployment",
	"metadata": {"name": "flexwatch"},
	"spec": {"template": {"spec": {
		"automountServiceAccountToken": false,
		"securityContext": {
			"runAsNonRoot": true,
			"seccompProfile": {"type": "RuntimeDefault"},
		},
		"containers": [{
			"name": "flexwatch",
			"image": "ghcr.io/sekuryn/flexwatch@sha256:abc",
			"securityContext": {
				"allowPrivilegeEscalation": false,
				"readOnlyRootFilesystem": true,
				"capabilities": {"drop": ["ALL"]},
			},
			"resources": {
				"requests": {"cpu": "10m", "memory": "32Mi"},
				"limits": {"cpu": "200m", "memory": "128Mi"},
			},
			"livenessProbe": {"httpGet": {"path": "/healthz", "port": 2112}},
			"readinessProbe": {"httpGet": {"path": "/readyz", "port": 2112}},
		}],
	}}},
}

test_deployment_durci_accepte if {
	result := workload.deny with input as hardened_deployment
	count(result) == 0
}

# Le cas le plus courant en vrai : un conteneur sans securityContext du tout.
# Il doit être refusé, pas ignoré.
test_container_sans_security_context_refuse if {
	dep := json.remove(hardened_deployment, ["/spec/template/spec/containers/0/securityContext"])

	result := workload.deny with input as dep
	count(result) == 3 # allowPrivilegeEscalation, readOnlyRootFilesystem, capabilities
}

test_image_par_tag_refuse if {
	dep := json.patch(hardened_deployment, [{
		"op": "replace",
		"path": "/spec/template/spec/containers/0/image",
		"value": "ghcr.io/sekuryn/flexwatch:v1.0.0",
	}])

	result := workload.deny with input as dep
	count(result) == 1
}

test_latest_refuse if {
	dep := json.patch(hardened_deployment, [{
		"op": "replace",
		"path": "/spec/template/spec/containers/0/image",
		"value": "ghcr.io/sekuryn/flexwatch:latest",
	}])

	result := workload.deny with input as dep

	# Deux motifs : pas de digest ET tag :latest.
	count(result) == 2
}

test_sans_limits_refuse if {
	dep := json.remove(hardened_deployment, ["/spec/template/spec/containers/0/resources/limits"])

	result := workload.deny with input as dep
	count(result) == 1
}

test_privilegie_refuse if {
	dep := json.patch(hardened_deployment, [{
		"op": "add",
		"path": "/spec/template/spec/containers/0/securityContext/privileged",
		"value": true,
	}])

	result := workload.deny with input as dep
	count(result) == 1
}

test_service_account_token_monte_refuse if {
	dep := json.patch(hardened_deployment, [{
		"op": "replace",
		"path": "/spec/template/spec/automountServiceAccountToken",
		"value": true,
	}])

	result := workload.deny with input as dep
	count(result) == 1
}

test_secret_en_clair_refuse if {
	secret := {
		"kind": "Secret",
		"metadata": {"name": "flexwatch-telegram"},
		"stringData": {"TELEGRAM_TOKEN": "123456:vrai-token"},
	}

	result := workload.deny with input as secret
	count(result) == 1
}

test_sondes_manquantes_averties if {
	dep := json.remove(hardened_deployment, [
		"/spec/template/spec/containers/0/livenessProbe",
		"/spec/template/spec/containers/0/readinessProbe",
	])

	result := workload.warn with input as dep
	count(result) == 2
}
