# flexwatch

[![ci](https://github.com/Sekuryn/flexwatch/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/Sekuryn/flexwatch/actions/workflows/ci.yml)
[![codeql](https://github.com/Sekuryn/flexwatch/actions/workflows/codeql.yml/badge.svg?branch=main)](https://github.com/Sekuryn/flexwatch/actions/workflows/codeql.yml)
[![go](https://img.shields.io/github/go-mod/go-version/Sekuryn/flexwatch?label=go)](go.mod)
[![quality gate](https://sonarcloud.io/api/project_badges/measure?project=Sekuryn_flexwatch&metric=alert_status)](https://sonarcloud.io/summary/new_code?id=Sekuryn_flexwatch)
[![couverture](https://sonarcloud.io/api/project_badges/measure?project=Sekuryn_flexwatch&metric=coverage)](https://sonarcloud.io/component_measures?id=Sekuryn_flexwatch&metric=coverage)
[![dépendances tierces](https://img.shields.io/badge/d%C3%A9pendances%20tierces-0-brightgreen)](go.mod)
[![licence](https://img.shields.io/github/license/Sekuryn/flexwatch)](LICENSE)

Détecteur de véhicules **Communauto Flex** dans un rayon donné, en Go.
Il surveille, il prévient. **Il ne réserve rien.**

Projet portfolio DevSecOps : l'application est le support, le vrai livrable est
le runbook d'hébergement et de pipeline — **[PLAN.md](PLAN.md)**.

---

## Chaîne de livraison

Ce que la CI exécute à chaque push et à chaque pull request :

| Étape | Outil | Bloque le merge |
|---|---|---|
| Format, `vet`, tests `-race`, couverture | Go 1.27, staticcheck | oui |
| Lint | golangci-lint 2.13 | oui |
| Vulnérabilités du code Go | govulncheck | oui |
| SAST (flux de données) | CodeQL | oui |
| Secrets dans l'historique | gitleaks | oui |
| Dépendances, IaC, secrets | Trivy 0.74 | oui |
| Policies IaC et Kubernetes | conftest / OPA | oui |
| Image arm64, scan, SBOM, signature | Docker, Syft, cosign | oui |
| Quality gate (new code) | SonarQube Cloud | oui |

Puis, **sans déployer** : image distroless nonroot poussée sur GHCR, **signée
en keyless** (Sigstore) avec le SBOM CycloneDX attesté. Le déploiement reste
manuel — c'est l'objet de [PLAN.md](PLAN.md).

### Vérifier une image publiée

La signature ne vaut que si quelqu'un la vérifie. Depuis n'importe quelle
machine, sans compte ni clé :

```bash
./scripts/verify-image.sh sha256:<digest affiche par la CI>
```

Le script contrôle la signature, l'attestation SBOM, **et** qu'une identité
étrangère est bien refusée. Le certificat Fulcio relie l'image à un commit et à
un workflow précis :

```
Subject   https://github.com/Sekuryn/flexwatch/.github/workflows/ci.yml@refs/heads/main
Issuer    https://token.actions.githubusercontent.com
```

### Protection de `main`

`main` n'accepte que des pull requests, en `squash` ou `rebase`, avec les
**8 checks verts**, les commits **signés**, et un résultat CodeQL. Ni push
direct, ni force-push, ni suppression.

---

## Ce que fait l'app

- Interroge l'endpoint **public et non authentifié** de disponibilité
  free-floating de Reservauto, toutes les 15–30 s (jamais moins de 10 s).
- Calcule la **bounding box** du cercle demandé (l'API ne sait filtrer que par
  rectangle), puis resserre au **vrai cercle en haversine** côté client.
- Détecte les **nouvelles apparitions** dans le rayon, avec déduplication par
  identifiant de véhicule et anti-flapping.
- Notifie : **console** toujours, **Telegram** si les variables sont présentes.
- Expose `/metrics` (Prometheus), `/healthz`, `/readyz`.

## Ce qu'elle ne fait pas, volontairement

| Non implémenté | Pourquoi |
|---|---|
| Réserver ou bloquer une voiture | Partie authentifiée de l'API. Risque CGU et risque de compte. Cf. PLAN.md phase 8. |
| Stocker un identifiant Communauto | Aucune donnée d'authentification n'entre dans ce projet. |
| Poller plus vite que 10 s | Le cache serveur est d'environ 5 s : aller plus vite n'apporte rien et ressemble à un abus. |
| Tourner en plusieurs répliques | L'état de détection est en mémoire. Deux instances = deux notifications. |

---

## Démarrage rapide (Windows 11 / PowerShell)

Prérequis : [Go 1.23+](https://go.dev/dl/) (vérifié sur **1.27.1**).

```powershell
# un seul poll, affiche ce que l'API renvoie, puis sort
go run ./cmd/flexwatch -once

# surveillance continue
$env:FLEX_CENTER_LAT = "45.5088"
$env:FLEX_CENTER_LON = "-73.5617"
$env:FLEX_RADIUS_KM  = "1.2"
go run ./cmd/flexwatch

# tests + lint (équivalent du Makefile, sans make)
.\scripts\make.ps1 test
.\scripts\make.ps1 check
```

Sous WSL / Linux / CI : `make help`, `make test`, `make check`, `make build`.

### Notifications Telegram

```powershell
$env:TELEGRAM_TOKEN   = "123456:ABC..."   # @BotFather
$env:TELEGRAM_CHAT_ID = "987654321"       # @userinfobot
go run ./cmd/flexwatch
```

Les deux variables vont ensemble : n'en fournir qu'une est une erreur de
configuration, et l'app le dit au démarrage.

---

## Configuration (100 % par variables d'environnement)

| Variable | Défaut | Rôle |
|---|---|---|
| `FLEX_CITY_ID` | `59` | Ville Reservauto (59 = Montréal). |
| `FLEX_CENTER_LAT` | `45.5017` | Latitude du centre du géofence. |
| `FLEX_CENTER_LON` | `-73.5673` | Longitude du centre. |
| `FLEX_RADIUS_KM` | `1.5` | Rayon, en km (0 < r ≤ 50). |
| `FLEX_POLL_INTERVAL` | `20s` | Intervalle de poll. **Refusé sous 10 s.** |
| `FLEX_HTTP_TIMEOUT` | `10s` | Timeout HTTP (doit être ≤ intervalle). |
| `FLEX_REALERT_AFTER` | `10m` | Délai avant de re-signaler un véhicule déjà vu. `0` = jamais. |
| `FLEX_METRICS_ADDR` | `:2112` | Adresse d'écoute de `/metrics`. |
| `FLEX_LOG_LEVEL` | `info` | `debug` / `info` / `warn` / `error`. |
| `FLEX_USER_AGENT` | `flexwatch/1.0 (...)` | On s'identifie honnêtement. |
| `FLEX_API_BASE_URL` | API Reservauto v2 | HTTPS obligatoire ; `http://` toléré uniquement vers la boucle locale (banc d'essai). |
| `TELEGRAM_TOKEN` | — | Token du bot. Jamais loggé. |
| `TELEGRAM_CHAT_ID` | — | Destinataire. |
| `TELEGRAM_API_BASE_URL` | `https://api.telegram.org` | Uniquement pour le banc d'essai local. `http://` n'est accepté que vers la boucle locale. |
| `FLEX_STORE_ENABLED` | `false` | Persistance des snapshots (build `-tags postgres`). |
| `DATABASE_URL` | — | DSN Postgres si la persistance est active. |

---

## Architecture

```
cmd/flexwatch            câblage, signaux, mode -once
internal/config          chargement + validation de l'environnement
internal/geo             haversine, bounding box
internal/communauto      client HTTP lecture seule + décodage tolérant
internal/watch           boucle de poll, backoff, détection des apparitions
internal/notify          interface Notifier, sinks console et Telegram
internal/metrics         exposition Prometheus (stdlib, sans dépendance)
internal/store           persistance optionnelle (build tag postgres)
test/e2e                 banc d'essai local : faux serveur + script d'assertions
```

### Zéro dépendance tierce

`go.mod` ne contient **aucun `require`**. L'exposition Prometheus est écrite à
la main (~150 lignes testées) plutôt que d'embarquer `client_golang` et ses
dépendances transitives. Conséquences concrètes : SBOM minimal, aucune CVE
transitive à trier, `go build` qui marche hors-ligne.

`pgx` est la seule dépendance possible, derrière `-tags postgres` : elle
n'entre dans le binaire que si on la demande explicitement.

### Contrat d'API observé

Relevé le **2026-09-17** sur `CityId=59`. L'API n'est pas documentée
publiquement : ceci est une observation, pas une garantie.

```json
{
  "totalNbVehicles": 42,
  "cachingInfo": { "cachingDurationInSec": 5, "servedFromCache": true },
  "vehicles": [
    {
      "vehicleId": 10428,              // identifiant interne -> clé de déduplication
      "vehicleNb": 8129,               // numéro PEINT sur la voiture -> ce que l'utilisateur cherche
      "cityId": 59,
      "vehiclePropulsionTypeId": 1,    // observé : 1 = essence, 2 = électrique (non documenté)
      "vehicleTypeId": 4,              // codes non documentés : volontairement non interprétés
      "vehicleLocation": { "latitude": 45.4721, "longitude": -73.5872 },
      "energyLevelPercentage": null,   // renseigné uniquement sur les électriques (3 sur 42)
      "satisfiesFilters": true
    }
  ]
}
```

Points à retenir :
- **il n'y a pas de plaque d'immatriculation** — d'où le champ `Number`
  (`vehicleNb`) et non `Plate` ;
- le modèle n'est disponible que sous forme d'identifiants numériques non
  documentés : le champ `Model` reste donc vide plutôt qu'inventé ;
- `cachingDurationInSec: 5` confirme le cache serveur, donc le plancher de 10 s.

Cette réponse réelle est figée dans
`internal/communauto/testdata/freefloating_montreal.json` et sert de test de
non-régression du contrat.

### Le point fragile, assumé

La casse et l'imbrication des champs peuvent changer sans préavis.
`internal/communauto/decode.go` décode donc sur des **listes de clés
candidates** et lève `ErrSchemaDrift` si rien ne correspond — une erreur
bruyante plutôt qu'un détecteur muet. Une alerte Prometheus dédiée
(`FlexwatchSchemaDrift`) surveille précisément ça.

---

## Qualité — état vérifié

Exécuté le 2026-09-17 sur Go 1.27.1 (Linux) :

| Contrôle | Résultat |
|---|---|
| `gofmt -s -l .` | aucun écart |
| `go build ./...` | OK |
| `go vet ./...` | OK |
| `go test ./...` | **41 tests**, tous verts |
| couverture | **77,3 %** des instructions (code applicatif) |
| `staticcheck` 2026.2.1 | 0 finding |
| `golangci-lint` 2.13.0 | **0 issue** |
| `govulncheck` | aucune vulnérabilité |
| dépendances tierces | **0** (pas de `go.sum`) |
| binaire | 7,2 Mo (amd64), cross-compile `linux/arm64` OK |
| API réelle (`-once`) | 22 véhicules reçus, 19 retenus dans 3 km |
| banc d'essai `make e2e` | **19/19 assertions** |

Et sur les fichiers de référence du plan :

| Contrôle | Résultat |
|---|---|
| `terraform fmt -check -recursive` | aucun écart |
| `terraform validate` (Terraform 1.16.3, provider AWS 5.100) | valide |
| `.terraform.lock.hcl` | verrouillé pour `linux_amd64` + `windows_amd64` |
| `conftest verify` (tests des policies rego) | **23 tests, 23 passés** |
| `conftest test` sur `deploy/k8s/` | **120 contrôles, 0 échec** |
| `promtool check rules` | **10 règles** valides |
| `promtool check config` | syntaxe valide |
| `trivy fs` (Trivy 0.74.0, scan identique à la CI) | 0 finding — 1 corrigé, 3 en exception datée |
| `cosign verify` de l'image publiée (depuis un poste) | **signature + attestation SBOM valides**, identité étrangère refusée |

Les exceptions Trivy sont dans **`.trivyignore.yaml`**, chacune avec son
`statement` et son `expired_at` : rien n'est ignoré sans justification ni date
de péremption (détail dans PLAN.md phase 2).

Non vérifiés par exécution, faute d'outils sur ce poste : `Dockerfile`
(Docker absent), le workflow CI (demande un push) et les `ClusterPolicy`
Kyverno (demandent un cluster). L'étape `terraform plan` + `conftest` sur le
plan réel demande des identifiants AWS : c'est la phase 5 du runbook.

```bash
make check     # gofmt, vet, golangci-lint, staticcheck, govulncheck, tests -race
make cover     # couverture
```

> `go test -race` demande un compilateur C. Sans gcc en local, les tests
> tournent sans le détecteur de course ; la CI (`ubuntu-latest`) l'active.

### Banc d'essai local — à lancer avant tout déploiement

```bash
make e2e                      # ou : .\scripts\make.ps1 e2e  (passe par WSL)
```

`test/e2e/run.sh` fait tourner le **vrai binaire** contre un faux serveur
(`test/e2e/mockapi`) qui joue à la fois l'API Communauto et l'API Telegram,
avec un scénario déterministe piloté par le numéro d'appel :

| Poll | Flotte servie | Attendu |
|---|---|---|
| 1 | 1 proche + 1 à 4,3 km | état initial, **aucune** notification |
| 2 | 2 proches + 1 loin | **1** notification (la nouvelle) |
| 3 | inchangé | **aucune** notification (pas de doublon) |
| 4 | retour à 1 proche | 1 disparition détectée |

19 assertions : notification unique et nominative, charge transmise, voiture
hors rayon **non** notifiée, compteurs Prometheus exacts, `/healthz` et
`/readyz` à 200, `SIGTERM` → code 0, et **token absent des logs**.

C'est ce que les tests unitaires ne peuvent pas prouver : le câblage réel du
binaire, et qu'une détection déclenche vraiment un message.

**Résultat au 2026-09-17 : 19/19.**

Les tests couvrent notamment : haversine et invariant bbox ⊃ cercle, diff
d'apparitions et anti-flapping, **décodage de la vraie réponse de l'API**
(fixture figée) et de ses variantes, backoff — dont le plafonnement et le
débordement d'entier —, respect de `Retry-After`, format d'exposition
Prometheus, et **l'absence de fuite du token Telegram dans les erreurs**.

---

## Licence

[MIT](LICENSE). C'est aussi la valeur déclarée par le label OCI
`org.opencontainers.image.licenses` de l'image : les deux doivent rester
alignés — une image qui annonce une licence absente du dépôt est une
incohérence qu'un audit de conformité relève tout de suite.

## Suite : le vrai sujet

**[PLAN.md](PLAN.md)** — threat model, CI, supply chain (SBOM + signature),
hébergement AWS (EC2 ARM + k3s + Kyverno + Falco), Terraform validé par OPA,
secrets, observabilité, et pourquoi la phase 2 (réservation automatique) n'est
pas implémentée.
