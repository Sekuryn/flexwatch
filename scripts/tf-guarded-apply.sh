#!/usr/bin/env bash
# Enchaîne plan -> policies -> apply, et refuse de sauter l'étape du milieu.
#
# À lancer depuis terraform/envs/prod. Le but est de rendre IMPOSSIBLE l'apply
# non validé par habitude ou par fatigue : c'est le genre de garde-fou qui
# compte à 23 h, pas à 10 h du matin.
set -euo pipefail

POLICY_DIR="${POLICY_DIR:-../../../policy/terraform}"
PLAN_FILE=tfplan
PLAN_JSON=tfplan.json

cleanup() {
	# Le plan JSON contient l'état cible en clair : il ne traîne pas.
	rm -f "$PLAN_FILE" "$PLAN_JSON"
}
trap cleanup EXIT

for tool in terraform conftest; do
	command -v "$tool" >/dev/null || {
		echo "outil manquant: $tool"
		exit 1
	}
done

echo "==> terraform plan"
terraform plan -out="$PLAN_FILE"

echo "==> conversion en JSON"
terraform show -json "$PLAN_FILE" > "$PLAN_JSON"

echo "==> conftest (policies de securite)"
if ! conftest test --policy "$POLICY_DIR" "$PLAN_JSON"; then
	echo
	echo "APPLY ANNULE : le plan viole une policy de securite."
	echo "Corriger le code Terraform, pas la policy."
	exit 1
fi

echo
echo "==> relire le plan ci-dessus, puis confirmer"
read -r -p "appliquer ? [oui/NON] " answer
if [ "$answer" != "oui" ]; then
	echo "annule."
	exit 0
fi

echo "==> terraform apply"
terraform apply "$PLAN_FILE"
