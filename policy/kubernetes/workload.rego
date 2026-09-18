# Policies OPA/Rego appliquées aux manifests Kubernetes, en CI.
#
#   conftest test --policy policy/kubernetes deploy/k8s/ --all-namespaces
#
# Ces règles doublent volontairement Pod Security Admission et Kyverno : trois
# barrières à des moments différents (CI, admission, runtime). Celle-ci est la
# moins chère — elle échoue avant même qu'un cluster soit impliqué.
package kubernetes.workload

import rego.v1

# Les charges de travail ont toutes un podTemplate ; les autres kinds sont
# hors périmètre de ces règles.
workload_kinds := {"Deployment", "StatefulSet", "DaemonSet", "Job"}

is_workload if input.kind in workload_kinds

pod_spec := input.spec.template.spec if is_workload

containers := array.concat(
	object.get(pod_spec, "containers", []),
	object.get(pod_spec, "initContainers", []),
)

name := object.get(input.metadata, "name", "<sans nom>")

# Valeurs par defaut explicites : un securityContext absent est le cas le plus
# dangereux, il ne doit pas rendre les regles muettes.
sec_ctx(c) := object.get(c, "securityContext", {})

pod_sec_ctx := object.get(pod_spec, "securityContext", {})

# ------------------------------------------------------- durcissement du pod

deny contains msg if {
	is_workload
	some c in containers
	object.get(sec_ctx(c), "allowPrivilegeEscalation", true) != false
	msg := sprintf("%s/%s: allowPrivilegeEscalation doit etre explicitement false.", [name, c.name])
}

deny contains msg if {
	is_workload
	some c in containers
	object.get(sec_ctx(c), "privileged", false) == true
	msg := sprintf("%s/%s: conteneur privilegie. Ce projet n'en a aucun besoin.", [name, c.name])
}

deny contains msg if {
	is_workload
	some c in containers
	object.get(sec_ctx(c), "readOnlyRootFilesystem", false) != true
	msg := sprintf("%s/%s: systeme de fichiers racine non en lecture seule. Un binaire Go statique n'a rien a y ecrire.", [name, c.name])
}

deny contains msg if {
	is_workload
	some c in containers
	caps := object.get(object.get(sec_ctx(c), "capabilities", {}), "drop", [])
	not "ALL" in caps
	msg := sprintf("%s/%s: les capabilities doivent etre droppees (drop: [\"ALL\"]).", [name, c.name])
}

deny contains msg if {
	is_workload
	object.get(pod_sec_ctx, "runAsNonRoot", false) != true
	msg := sprintf("%s: runAsNonRoot absent au niveau du pod.", [name])
}

deny contains msg if {
	is_workload
	profile := object.get(object.get(pod_sec_ctx, "seccompProfile", {}), "type", "")
	not profile in {"RuntimeDefault", "Localhost"}
	msg := sprintf("%s: seccompProfile doit etre RuntimeDefault (ou Localhost).", [name])
}

# ------------------------------------------------------------------ ressources

deny contains msg if {
	is_workload
	some c in containers
	not object.get(object.get(c, "resources", {}), "limits", false)
	msg := sprintf("%s/%s: pas de limits. Sur un t4g.small, un conteneur sans plafond peut evincer Prometheus.", [name, c.name])
}

deny contains msg if {
	is_workload
	some c in containers
	not object.get(object.get(c, "resources", {}), "requests", false)
	msg := sprintf("%s/%s: pas de requests, l'ordonnanceur ne peut rien garantir.", [name, c.name])
}

# ---------------------------------------------------------------------- images

deny contains msg if {
	is_workload
	some c in containers
	not contains(c.image, "@sha256:")
	msg := sprintf("%s/%s: image referencee par tag (%s). Utiliser un digest.", [name, c.name, c.image])
}

deny contains msg if {
	is_workload
	some c in containers
	endswith(c.image, ":latest")
	msg := sprintf("%s/%s: tag :latest interdit.", [name, c.name])
}

# ------------------------------------------------------------------- sondes

warn contains msg if {
	is_workload
	some c in containers
	not object.get(c, "livenessProbe", false)
	msg := sprintf("%s/%s: pas de livenessProbe.", [name, c.name])
}

warn contains msg if {
	is_workload
	some c in containers
	not object.get(c, "readinessProbe", false)
	msg := sprintf("%s/%s: pas de readinessProbe.", [name, c.name])
}

# ------------------------------------------------------------------- secrets

# Un secret en clair dans un manifest finit dans git. C'est la règle qui
# protège le token Telegram (cf. PLAN.md phase 6).
deny contains msg if {
	input.kind == "Secret"
	object.get(input, "stringData", false)
	msg := sprintf("Secret %s: stringData en clair dans un manifest. Passer par ExternalSecret ou une creation hors git.", [name])
}

deny contains msg if {
	input.kind == "Secret"
	some key, value in object.get(input, "data", {})
	count(value) > 0
	not startswith(value, "REMPLACER")
	msg := sprintf("Secret %s: la cle %q porte une valeur. Un base64 n'est pas un chiffrement.", [name, key])
}

# ------------------------------------------------------ token de service account

deny contains msg if {
	is_workload
	object.get(pod_spec, "automountServiceAccountToken", true) != false
	msg := sprintf("%s: automountServiceAccountToken doit etre false, l'app n'appelle pas l'API Kubernetes.", [name])
}
