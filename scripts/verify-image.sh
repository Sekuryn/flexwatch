#!/usr/bin/env bash
# Vérifie qu'une image publiée a bien été signée par le workflow de CI de ce
# dépôt — et par personne d'autre.
#
#   ./scripts/verify-image.sh <digest sha256:...>
#
# C'est l'étape que tout le monde saute (PLAN.md phase 3, étape 7). Sans elle,
# la signature produite par la CI n'est vérifiée par personne et ne prouve
# rien : elle devient un rituel, pas un contrôle.
#
# ATTENTION AUX CODES DE SORTIE : `cosign ... | head` renverrait le code de
# `head`, donc toujours 0. Un script de vérification qui annonce « OK » quoi
# qu'il arrive est plus dangereux que pas de script du tout. D'où PIPESTATUS
# partout ci-dessous.
set -uo pipefail

IMAGE="${IMAGE:-ghcr.io/sekuryn/flexwatch}"
IDENTITY="${IDENTITY:-https://github.com/Sekuryn/flexwatch/\.github/workflows/ci\.yml@.*}"
ISSUER="${ISSUER:-https://token.actions.githubusercontent.com}"

DIGEST="${1:-}"
if [ -z "$DIGEST" ]; then
  echo "usage: $0 sha256:<64 caracteres hexa>"
  echo "Le digest est affiche a la fin du job « build, sbom, sign » de la CI."
  exit 2
fi
case "$DIGEST" in
  sha256:*) ;;
  *) echo "digest invalide : attendu sha256:..., recu $DIGEST"; exit 2 ;;
esac

command -v cosign >/dev/null 2>&1 || {
  echo "cosign introuvable. Installation sans sudo :"
  echo "  curl -fsSL -o ~/.local/bin/cosign https://github.com/sigstore/cosign/releases/latest/download/cosign-linux-amd64"
  echo "  chmod +x ~/.local/bin/cosign   # et verifier l'empreinte publiee"
  exit 127
}

REF="${IMAGE}@${DIGEST}"
fail=0

echo "======== 1. signature de l'image ========"
echo "cible : $REF"
if cosign verify "$REF" \
  --certificate-identity-regexp "$IDENTITY" \
  --certificate-oidc-issuer "$ISSUER" >/tmp/cosign-verify.json 2>/tmp/cosign-verify.err; then
  echo "  [OK] signee par le workflow de ce depot"
else
  echo "  [KO] verification refusee :"
  sed 's/^/        /' /tmp/cosign-verify.err | head -6
  fail=1
fi

echo
echo "======== 2. attestation SBOM (CycloneDX) ========"
if cosign verify-attestation "$REF" \
  --type cyclonedx \
  --certificate-identity-regexp "$IDENTITY" \
  --certificate-oidc-issuer "$ISSUER" >/tmp/cosign-attest.json 2>/tmp/cosign-attest.err; then
  echo "  [OK] SBOM attache et signe par la meme identite"
else
  echo "  [KO] attestation non verifiee :"
  sed 's/^/        /' /tmp/cosign-attest.err | head -6
  fail=1
fi

echo
echo "======== 3. controle negatif ========"
# Si une identite quelconque validait, la verification ne prouverait rien.
#
# Mais ce controle n'a de sens QUE si l'etape 1 a reussi : sinon il « passe »
# parce que le registre refuse l'acces, pas parce que l'identite est mauvaise.
# Un controle negatif qui reussit pour la mauvaise raison est un piege.
if [ "$fail" -ne 0 ]; then
  echo "  [--] non concluant : l'etape 1 a echoue, un refus ici ne prouve rien"
elif cosign verify "$REF" \
  --certificate-identity-regexp "https://github\.com/quelquun-dautre/.*" \
  --certificate-oidc-issuer "$ISSUER" >/dev/null 2>&1; then
  echo "  [KO] GRAVE : une identite etrangere a ete acceptee"
  fail=1
else
  echo "  [OK] une autre identite est bien refusee"
fi

echo
if [ "$fail" -eq 0 ]; then
  echo "===== IMAGE VERIFIEE ====="
else
  echo "===== VERIFICATION EN ECHEC ====="
  echo
  echo "Si l'erreur est « UNAUTHORIZED: authentication required », le paquet"
  echo "GHCR est prive. Deux facons de debloquer :"
  echo "  a) s'authentifier :"
  echo "       gh auth refresh -s read:packages"
  echo "       cosign login ghcr.io -u <compte> -p \"\$(gh auth token)\""
  echo "  b) rendre le paquet public (Packages -> flexwatch -> Change visibility),"
  echo "     ce qui permet a n'importe qui de verifier la signature — l'interet"
  echo "     meme d'une signature keyless sur un projet portfolio."
fi
exit "$fail"
