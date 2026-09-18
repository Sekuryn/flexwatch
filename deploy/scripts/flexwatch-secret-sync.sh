#!/usr/bin/env bash
# Synchronise le secret Telegram depuis AWS Secrets Manager vers un Secret k8s.
#
# POURQUOI CE SCRIPT plutôt que External Secrets Operator :
#
# L'instance impose IMDSv2 avec `http_put_response_hop_limit = 1`, ce qui
# empêche VOLONTAIREMENT les conteneurs d'atteindre 169.254.169.254 (le
# franchissement du bridge réseau consomme le saut). C'est une protection forte
# contre le vol de credentials d'instance depuis un pod compromis — mais ça
# empêche aussi ESO d'utiliser le rôle de l'instance.
#
# Les trois options, et le choix retenu :
#   A. ESO + clés AWS statiques dans un Secret k8s  -> on remplace un secret
#      par un autre, plus puissant. Non.
#   B. hop limit = 2 + NetworkPolicy interdisant 169.254.0.0/16 partout sauf
#      pour ESO -> ça marche, mais la protection dépend alors d'une policy
#      réseau correcte dans chaque namespace.
#   C. (retenu) ce script, exécuté sur l'HÔTE, qui lui a un accès IMDS légitime
#      via le rôle de l'instance. Aucune clé statique, aucun pod n'atteint
#      l'IMDS, et la rotation est automatique via un timer systemd.
#
# Le prix à payer : c'est du shell sur la machine plutôt qu'un opérateur
# déclaratif. Assumé pour une instance unique ; avec plusieurs noeuds, l'option
# B + ESO redevient la bonne réponse.
set -euo pipefail

SECRET_ID="${SECRET_ID:-flexwatch/telegram}"
NAMESPACE="${NAMESPACE:-flexwatch}"
SECRET_NAME="${SECRET_NAME:-flexwatch-telegram}"
KUBECONFIG_PATH="${KUBECONFIG_PATH:-/etc/rancher/k3s/k3s.yaml}"

export KUBECONFIG="$KUBECONFIG_PATH"

log() { echo "[$(date -Is)] $*"; }

# Lecture du secret. La valeur ne transite que par des variables shell ; elle
# n'est jamais écrite sur le disque ni passée en argument de commande (les
# arguments sont visibles dans /proc et dans `ps`).
log "lecture de $SECRET_ID"
payload="$(aws secretsmanager get-secret-value \
  --secret-id "$SECRET_ID" \
  --query SecretString \
  --output text)"

token="$(printf '%s' "$payload" | jq -r '.token')"
chat_id="$(printf '%s' "$payload" | jq -r '.chat_id')"

if [ -z "$token" ] || [ "$token" = "null" ] || [ -z "$chat_id" ] || [ "$chat_id" = "null" ]; then
  log "ERREUR: le secret ne contient pas les cles 'token' et 'chat_id'"
  exit 1
fi

# --dry-run=client + apply : idempotent, et ça met à jour la valeur si elle a
# changé côté Secrets Manager (rotation).
log "application du Secret $NAMESPACE/$SECRET_NAME"
kubectl -n "$NAMESPACE" create secret generic "$SECRET_NAME" \
  --from-literal=TELEGRAM_TOKEN="$token" \
  --from-literal=TELEGRAM_CHAT_ID="$chat_id" \
  --dry-run=client -o yaml | kubectl apply -f -

unset token chat_id payload

# Un Secret mis à jour n'est PAS rechargé par un pod qui l'a monté en env :
# il faut redémarrer le déploiement. On ne le fait que si la révision a
# effectivement changé, pour ne pas redémarrer le bot toutes les heures.
current_hash="$(kubectl -n "$NAMESPACE" get secret "$SECRET_NAME" \
  -o jsonpath='{.metadata.resourceVersion}')"
last_hash_file="/var/lib/flexwatch/secret-revision"

install -d -m 0700 "$(dirname "$last_hash_file")"
if [ ! -f "$last_hash_file" ] || [ "$(cat "$last_hash_file")" != "$current_hash" ]; then
  log "secret modifie, redemarrage du deploiement"
  kubectl -n "$NAMESPACE" rollout restart deployment/flexwatch
  printf '%s' "$current_hash" > "$last_hash_file"
else
  log "secret inchange, rien a faire"
fi

log "termine"
