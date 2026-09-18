#!/usr/bin/env bash
# Bootstrap de l'instance flexwatch : mises à jour, agent SSM, k3s.
#
# Ce script est rendu par templatefile() et exécuté en root par cloud-init.
# Il ne contient AUCUN secret : le user_data est lisible par n'importe quel
# process de l'instance via l'IMDS.
set -euo pipefail

exec > >(tee -a /var/log/flexwatch-bootstrap.log) 2>&1
echo "=== bootstrap flexwatch $(date -Is) ==="

# ------------------------------------------------------------------ base OS
dnf -y update
dnf -y install amazon-ssm-agent iptables-services
systemctl enable --now amazon-ssm-agent

# ------------------------------------------------------------------- k3s
# `curl | sh` en root est une porte d'entrée idéale pour une attaque de chaîne
# d'approvisionnement : on télécharge, on vérifie l'empreinte, PUIS on exécute.
K3S_VERSION="${k3s_version}"
EXPECTED_SHA256="${k3s_installer_sha256}"

# Ce script est execute en root : une redirection vers http serait une
# porte ouverte, d'ou --proto-redir.
curl -sfL --proto '=https' --proto-redir '=https' --tlsv1.2 https://get.k3s.io -o /tmp/k3s-install.sh
ACTUAL_SHA256="$(sha256sum /tmp/k3s-install.sh | awk '{print $1}')"

if [ "$ACTUAL_SHA256" != "$EXPECTED_SHA256" ]; then
  echo "ERREUR: empreinte de l'installeur k3s inattendue"
  echo "  attendu : $EXPECTED_SHA256"
  echo "  obtenu  : $ACTUAL_SHA256"
  echo "Bootstrap interrompu. Soit le script amont a change (relever la nouvelle"
  echo "empreinte et mettre a jour k3s_installer_sha256), soit le telechargement"
  echo "a ete altere."
  exit 1
fi
chmod 0700 /tmp/k3s-install.sh

# --secrets-encryption      : les Secrets k8s sont chiffres au repos dans etcd.
# --protect-kernel-defaults : refuse de demarrer si les sysctl ne sont pas surs
#                             (au lieu de les ecraser silencieusement).
# --write-kubeconfig-mode   : 0640, lisible par le groupe, pas par tout le monde.
# --disable=servicelb       : pas de LoadBalancer sur une instance unique.
INSTALL_K3S_VERSION="$K3S_VERSION" \
INSTALL_K3S_EXEC="server \
  --secrets-encryption \
  --protect-kernel-defaults \
  --write-kubeconfig-mode=0640 \
  --disable=servicelb \
  --kube-apiserver-arg=audit-log-path=/var/lib/rancher/k3s/server/logs/audit.log \
  --kube-apiserver-arg=audit-log-maxage=14 \
  --kube-apiserver-arg=audit-policy-file=/var/lib/rancher/k3s/server/audit-policy.yaml" \
  /tmp/k3s-install.sh

rm -f /tmp/k3s-install.sh

# Politique d'audit minimale : qui a fait quoi sur l'API k8s. Sans fichier, le
# flag audit-log-path ne journalise rien.
install -d -m 0700 /var/lib/rancher/k3s/server
cat > /var/lib/rancher/k3s/server/audit-policy.yaml <<'AUDIT'
apiVersion: audit.k8s.io/v1
kind: Policy
rules:
  # Les secrets : on journalise l'accès, jamais le contenu.
  - level: Metadata
    resources:
      - group: ""
        resources: ["secrets", "configmaps"]
  # Les changements d'état, avec le détail de la requête.
  - level: RequestResponse
    verbs: ["create", "update", "patch", "delete"]
  # Le reste en métadonnées : suffisant pour tracer, sans noyer le disque.
  - level: Metadata
AUDIT

systemctl restart k3s || true

echo "=== bootstrap termine $(date -Is) ==="
echo "Suite du runbook : PLAN.md phase 4 (Kyverno, Falco, deploiement)."
