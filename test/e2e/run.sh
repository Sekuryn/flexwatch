#!/usr/bin/env bash
# Test de bout en bout LOCAL, à lancer avant tout déploiement.
#
# Il fait tourner le VRAI binaire, contre un faux serveur qui joue à la fois
# l'API Communauto et l'API Telegram, et vérifie la chaîne complète :
# poll -> filtrage géographique -> détection -> notification -> métriques.
#
# Ce que les tests unitaires ne couvrent pas et que celui-ci couvre :
#   - le câblage réel du binaire (config, sinks, serveur d'admin) ;
#   - qu'une apparition déclenche VRAIMENT un message Telegram ;
#   - qu'un état stable n'en déclenche pas un deuxième ;
#   - que le token n'apparaît pas dans les logs de production.
#
# Durée : ~60 s (le plancher de poll de 10 s est volontairement non contournable).
set -uo pipefail

cd "$(dirname "$0")/../.." || exit 1

# Go installé en userspace (cf. PLAN.md phase 0) : on le trouve tout seul,
# pour que le script marche aussi depuis un shell non interactif.
if ! command -v go >/dev/null 2>&1 && [ -x "$HOME/.local/go/bin/go" ]; then
  export PATH="$HOME/.local/go/bin:$PATH"
fi
command -v go >/dev/null 2>&1 || { echo "go introuvable (cf. PLAN.md phase 0)"; exit 1; }

ADDR="127.0.0.1:18080"
METRICS="127.0.0.1:12112"
TOKEN="123456:FAUX-TOKEN-DE-TEST-NE-PAS-UTILISER"
CHAT="42"
LOG=/tmp/flexwatch-e2e.log
MOCKLOG=/tmp/mockapi-e2e.log

fail=0
ok()   { echo "  [OK]   $*"; }
ko()   { echo "  [KO]   $*"; fail=1; }
step() { echo; echo "======== $* ========"; }

cleanup() {
  [ -n "${BOT_PID:-}" ] && kill "$BOT_PID" 2>/dev/null
  [ -n "${MOCK_PID:-}" ] && kill "$MOCK_PID" 2>/dev/null
  wait 2>/dev/null
}
trap cleanup EXIT

step "compilation"
go build -o /tmp/flexwatch ./cmd/flexwatch || exit 1
go build -o /tmp/mockapi ./test/e2e/mockapi || exit 1
ok "binaires construits"

step "demarrage du faux serveur"
/tmp/mockapi -addr "$ADDR" > "$MOCKLOG" 2>&1 &
MOCK_PID=$!
for _ in $(seq 1 20); do
  curl -fsS "http://$ADDR/_recorded" >/dev/null 2>&1 && break
  sleep 0.2
done
curl -fsS "http://$ADDR/_recorded" >/dev/null || { ko "faux serveur injoignable"; exit 1; }
ok "faux serveur pret sur $ADDR"

step "demarrage de flexwatch (rayon 1 km, poll 10 s)"
FLEX_API_BASE_URL="http://$ADDR/api/v2" \
TELEGRAM_API_BASE_URL="http://$ADDR" \
TELEGRAM_TOKEN="$TOKEN" \
TELEGRAM_CHAT_ID="$CHAT" \
FLEX_CENTER_LAT=45.5017 FLEX_CENTER_LON=-73.5673 FLEX_RADIUS_KM=1 \
FLEX_POLL_INTERVAL=10s FLEX_HTTP_TIMEOUT=5s FLEX_LOG_LEVEL=debug \
FLEX_METRICS_ADDR="$METRICS" \
  /tmp/flexwatch > "$LOG" 2>&1 &
BOT_PID=$!

sleep 3
kill -0 "$BOT_PID" 2>/dev/null && ok "processus vivant" || { ko "arret immediat, voir $LOG"; cat "$LOG"; exit 1; }

step "deroulement du scenario (4 polls, ~45 s)"
for i in 1 2 3 4; do
  sleep 11
  calls=$(curl -fsS "http://$ADDR/_recorded" | grep -o '"api_calls":[0-9]*' | cut -d: -f2)
  echo "  poll $i termine (appels API vus par le faux serveur : $calls)"
done

step "verification des notifications"
RECORDED=$(curl -fsS "http://$ADDR/_recorded")
NB_TG=$(echo "$RECORDED" | grep -o '"chat_id"' | wc -l)

if [ "$NB_TG" -eq 1 ]; then
  ok "exactement 1 notification Telegram (l'apparition), pas de doublon sur l'etat stable"
else
  ko "$NB_TG notification(s) Telegram, attendu 1"
fi

# La voiture qui apparaît est proche2 = Flex 6106, à ~280 m, chargée à 82 %.
echo "$RECORDED" | grep -q 'Flex 6106' \
  && ok "la notification nomme le bon vehicule (Flex 6106)" \
  || ko "vehicule attendu absent de la notification"
echo "$RECORDED" | grep -q '82%' \
  && ok "le niveau de charge est transmis" \
  || ko "niveau de charge absent"
echo "$RECORDED" | grep -q '"chat_id":"42"' \
  && ok "envoye au bon chat" \
  || ko "mauvais chat_id"
echo "$RECORDED" | grep -q 'Flex 9999' \
  && ko "la voiture HORS RAYON a ete notifiee (filtrage haversine casse)" \
  || ok "la voiture a 4,3 km n'est pas notifiee (filtrage haversine correct)"

step "verification des metriques"
M=$(curl -fsS "http://$METRICS/metrics")
expect_metric() { # <motif> <description>
  if echo "$M" | grep -q "^$1\$"; then ok "$2"; else
    ko "$2 — obtenu : $(echo "$M" | grep "^${1%% *}" || echo '(absent)')"
  fi
}
expect_metric 'flexwatch_new_appearances_total 1' "1 apparition comptee"
expect_metric 'flexwatch_disappearances_total 1' "1 disparition comptee"
expect_metric 'flexwatch_vehicles_in_fence 1' "1 vehicule dans le rayon a la fin"
expect_metric 'flexwatch_vehicles_in_city 2' "2 vehicules annonces par l'API"
expect_metric 'flexwatch_notifications_total{outcome="telegram_ok"} 1' "envoi telegram compte comme reussi"
expect_metric 'flexwatch_notifications_total{outcome="console_ok"} 1' "sink console compte comme reussi"

echo "$M" | grep -q 'flexwatch_polls_total{result="error"}' \
  && ko "des polls en erreur" \
  || ok "aucun poll en erreur"

step "sondes"
[ "$(curl -fsS -o /dev/null -w '%{http_code}' "http://$METRICS/healthz")" = "200" ] \
  && ok "/healthz 200" || ko "/healthz"
[ "$(curl -fsS -o /dev/null -w '%{http_code}' "http://$METRICS/readyz")" = "200" ] \
  && ok "/readyz 200" || ko "/readyz"

step "arret propre"
kill -TERM "$BOT_PID"
wait "$BOT_PID"; code=$?
BOT_PID=""
[ "$code" -eq 0 ] && ok "code de sortie 0 sur SIGTERM" || ko "code de sortie $code"

step "aucune fuite de secret"
if grep -q "FAUX-TOKEN" "$LOG"; then
  ko "LE TOKEN EST DANS LES LOGS"
  grep -n "FAUX-TOKEN" "$LOG" | head -3
else
  ok "le token n'apparait pas dans les logs"
fi

step "journal applicatif"
grep -E 'msg":"(etat initial|nouveau vehicule|vehicule sorti|arret demande)' "$LOG" \
  | sed 's/^/  /' | head -6

echo
if [ "$fail" -eq 0 ]; then
  echo "===== E2E OK : la chaine complete fonctionne ====="
else
  echo "===== E2E EN ECHEC (logs : $LOG et $MOCKLOG) ====="
fi
exit "$fail"
