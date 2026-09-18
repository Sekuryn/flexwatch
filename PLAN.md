# PLAN.md — Runbook DevSecOps flexwatch

**Ce document est fait pour être exécuté à la main, par toi, dans l'ordre.**
Aucune automatisation de ce dépôt ne déploie quoi que ce soit : la CI s'arrête
à « image signée et publiée ». Tout ce qui touche AWS est une commande que tu
tapes, après l'avoir lue.

Chaque phase suit le même format : **objectif**, **étapes**, **outil**,
**pourquoi ça compte**, **terminé quand**.

---

## Ordre d'exécution réel

Les phases sont numérotées par thème (comme demandé), mais l'ordre
chronologique n'est pas exactement le même : Terraform (phase 5) crée
l'infrastructure que la phase 4 décrit et utilise.

| # | Phase | Quand l'exécuter | Durée réaliste |
|---|---|---|---|
| 0 | Poste de travail et dépôt | 1er | 45 min |
| 1 | Threat model STRIDE | 2e | 2 h |
| 2 | CI qualité et sécurité | 3e | 2–3 h |
| 3 | Build, SBOM, signature | 4e | 2 h |
| 4A | Décision d'archi + préparation du compte AWS | 5e | 1 h |
| 5 | Terraform + validation OPA, puis `apply` | 6e | 3–4 h |
| 4B | k3s, Kyverno, Falco, déploiement, ingress | 7e | 3–4 h |
| 6 | Secrets | 8e (en partie pendant 4B) | 1 h |
| 7 | Observabilité | 9e | 3 h |
| 8 | Phase 2 esquissée (non implémentée) | lecture seule | — |

> **Coût AWS estimé** (ca-central-1, à la demande) : t4g.small ≈ 13 USD/mois,
> 30 Gio gp3 ≈ 2,70 USD, EIP attachée 0 USD, flow logs ≈ 1–2 USD, ECR < 1 USD.
> **Total ≈ 18–20 USD/mois.** Avec `terraform destroy` entre les sessions de
> travail, on descend à quelques dollars. Le budget alarm de la phase 4A n'est
> pas décoratif.

---

## Phase 0 — Poste de travail et dépôt

**Objectif.** Pouvoir compiler, tester et pousser, avec les mêmes outils que la
CI. Rien d'autre.

**Outil.** Go, git, GitHub CLI, PowerShell/WSL.

### Étapes

1. **Installer Go sous Windows.**
   ```powershell
   winget install --id GoLang.Go --exact
   # nouvelle session PowerShell, puis :
   go version
   ```
   > **État actuel de ce poste.** Go **1.27.1** est déjà installé *dans la WSL
   > Debian*, sous `~/.local/go/bin` (installation sans sudo, empreinte SHA256
   > vérifiée). Rien n'est installé côté Windows. Pour travailler depuis WSL :
   > ```bash
   > echo 'export PATH="$HOME/.local/go/bin:$HOME/go/bin:$PATH"' >> ~/.bashrc
   > cd /mnt/c/Users/lecoc/Documents/Github/CommunAutoBook
   > ```
   > L'installation Windows reste utile pour l'intégration VS Code ; les deux
   > peuvent cohabiter.

2. **Vérifier que le projet compile et que les tests passent.**
   ```bash
   gofmt -s -l .        # doit ne rien afficher
   go vet ./...
   go test ./...        # ajouter -race si gcc est disponible
   go run ./cmd/flexwatch -once
   ```
   Le dernier appel touche la vraie API. S'il affiche `in_bbox` > 0, le contrat
   d'API est conforme à ce que le décodeur attend.

   Puis le banc d'essai de bout en bout, qui teste le binaire câblé pour de
   vrai (faux serveur local, ~60 s, 19 assertions) :
   ```bash
   make e2e
   ```

   > **Déjà exécuté le 2026-09-17** : 38 tests verts, couverture 77,0 %,
   > `go vet` propre, `staticcheck` et `golangci-lint` à 0 finding,
   > `govulncheck` sans vulnérabilité, `-once` a retourné 19 véhicules dans
   > 3 km. Trois défauts réels ont été corrigés à cette occasion : une méthode
   > `WriteTo` non conforme à `io.WriterTo` (`go vet`), un backoff dont le
   > jitter dépassait son propre plafond et pouvait déborder `int64`, et un
   > décodeur qui ne correspondait pas au vrai schéma de l'API
   > (`vehicleLocation`, `vehicleNb`, `energyLevelPercentage`).

3. **Installer les outils de la CI en local.** Versions vérifiées :
   `golangci-lint` **2.13.0**, `staticcheck` **2026.2.1**.
   ```bash
   go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.13.0
   go install honnef.co/go/tools/cmd/staticcheck@2026.2.1
   go install golang.org/x/vuln/cmd/govulncheck@latest
   make check           # ou .\scripts\make.ps1 check sous PowerShell
   ```
   `make lint-postgres` est à part : linter la variante Postgres exige
   d'ajouter d'abord la dépendance pgx, qui n'est pas dans `go.mod`.

4. **Initialiser le dépôt.** Attention : `Documents\Github` est **déjà** un
   dépôt git contenant d'autres projets. Ne pas imbriquer.
   ```powershell
   cd C:\Users\lecoc\Documents\Github\CommunAutoBook
   git init -b main
   git add .
   git commit -m "feat: detecteur Communauto Flex (lecture seule) + runbook DevSecOps"
   gh repo create flexwatch --private --source=. --push
   ```

5. **Protéger la branche** (l'intérêt d'une CI qui bloque est nul si on peut
   pousser à côté) :
   ```powershell
   gh api -X PUT repos/:owner/flexwatch/branches/main/protection `
     --input .github/branch-protection.json
   ```
   À défaut : *Settings → Branches → Add rule* → exiger les checks `quality`,
   `lint`, `govulncheck`, `trivy-repo`, et interdire le push direct.

6. **Activer les alertes de sécurité GitHub** : *Settings → Code security* →
   Dependabot alerts, Dependabot security updates, secret scanning, push
   protection.

**Pourquoi ça compte.** Un pipeline de sécurité qui peut être contourné par un
`git push --force` sur la branche par défaut ne protège rien. La protection de
branche est ce qui transforme la CI d'un indicateur en un contrôle.

**Terminé quand.**
- `make check` (ou `.\scripts\make.ps1 check`) passe en vert localement ;
- `make e2e` passe (19 assertions) ;
- `go run ./cmd/flexwatch -once` liste des véhicules réels ;
- le dépôt est sur GitHub, `main` protégée, push direct refusé.

---

## Phase 1 — Threat model (STRIDE / Threat Dragon)

**Objectif.** Savoir ce qu'on protège, contre quoi, et pouvoir justifier chaque
contrôle technique des phases suivantes par une menace nommée.

**Outil.** [OWASP Threat Dragon](https://www.threatdragon.com/) (desktop ou
web), méthode STRIDE.

### Étapes

1. **Lister les actifs** (avant tout diagramme) :
   - le **token Telegram** — permet d'usurper le bot et de spammer le chat ;
   - les **credentials IAM de l'instance** — permettent d'agir dans le compte AWS ;
   - l'**état Terraform** (S3) — cartographie complète de l'infra ;
   - la **chaîne de build** (workflow GitHub, registre) — permet de faire
     exécuter du code arbitraire en production ;
   - **le géofence lui-même** : `FLEX_CENTER_LAT/LON` est, en pratique,
     l'adresse du domicile de l'utilisateur. C'est une donnée personnelle, et
     elle est dans une ConfigMap. À traiter comme telle ;
   - la **disponibilité du service** — un bot muet est un bot inutile.

2. **Tracer le diagramme de flux de données** dans Threat Dragon, avec ces
   frontières de confiance (une frontière = un endroit où le niveau de
   confiance change) :
   ```
   [API Reservauto] ──HTTPS──┐
   [API Telegram]   ──HTTPS──┤
                             ├─▶ (frontière Internet) ─▶ [pod flexwatch]
   [IMDS 169.254.169.254] ───┘        (frontière pod/hôte)
                                             │
                     [k3s / hôte EC2] ◀──────┘
                             │
                     (frontière compte AWS) ─▶ [Secrets Manager, ECR, S3]

   [GitHub Actions] ─(frontière CI/prod)─▶ [registre] ─▶ [cluster]
   ```

3. **Dérouler STRIDE sur chaque élément.** Le tableau ci-dessous est le
   résultat attendu — le reproduire dans Threat Dragon élément par élément,
   pas le recopier en bloc.

| STRIDE | Menace concrète | Contrôle dans ce projet | Phase |
|---|---|---|---|
| **S**poofing | DNS détourné → faux endpoint Reservauto qui renvoie de fausses voitures | TLS vérifié (CA distroless) ; `FLEX_API_BASE_URL` et `TELEGRAM_API_BASE_URL` refusés par la config sauf en `https://` — `http://` n'est toléré que vers `127.0.0.1`/`localhost`, pour le banc d'essai | 3 |
| **S**poofing | Quelqu'un pousse une image `flexwatch` non issue de la CI | cosign keyless + Kyverno `verify-flexwatch-signature` | 3, 4B |
| **T**ampering | Tag d'image réécrit après vérification | ECR `IMMUTABLE`, déploiement **par digest**, `mutateDigest` Kyverno | 4B, 5 |
| **T**ampering | Script `get.k3s.io` altéré, exécuté en root par cloud-init | empreinte SHA256 vérifiée avant exécution (`user_data.sh`) | 5 |
| **T**ampering | Action GitHub compromise (tag mutable repointé) | épinglage des actions par SHA + audit `zizmor` | 2 |
| **R**epudiation | « Qui a supprimé ce déploiement ? » sans réponse | audit log de l'API k3s, CloudTrail, VPC flow logs | 4B, 5 |
| **I**nfo disclosure | Le token Telegram atterrit dans les logs → Loki → capture d'écran | le token n'est jamais emballé dans une erreur (`telegram.go`), et un **test le vérifie** | app |
| **I**nfo disclosure | SSRF/RCE dans le bot → lecture de l'IMDS → credentials de l'instance | IMDSv2 requis, hop limit 1, NetworkPolicy qui exclut `169.254.0.0/16` | 4B, 5 |
| **I**nfo disclosure | Bucket d'état Terraform lisible | bucket privé, chiffré, versionné, `BlockPublicAcls` | 5 |
| **I**nfo disclosure | Le domicile de l'utilisateur est déductible des métriques/logs | dépôt privé, `/metrics` en ClusterIP sans Ingress, Grafana via tunnel SSM | 4B, 7 |
| **D**enial of service | **Nous** DoSons Communauto → IP bannie | plancher de 10 s codé en dur, jitter, backoff exponentiel plafonné, respect de `Retry-After` | app |
| **D**enial of service | Réponse API géante → OOM du pod | lecture bornée à 8 MiB, `limits.memory: 128Mi` | app, 4B |
| **D**enial of service | Telegram rate-limite le bot → alertes perdues | erreurs de notification comptées et alertées, jamais fatales | 7 |
| **E**levation of privilege | Évasion de conteneur | distroless nonroot, `readOnlyRootFilesystem`, `drop: ALL`, seccomp `RuntimeDefault`, PSA `restricted` | 4B |
| **E**levation of privilege | Rôle IAM trop large utilisé après compromission | policies portées à l'ARN, aucune action d'écriture ECR, `conftest` refuse `Resource: "*"` | 5 |
| **E**levation of privilege | Token de ServiceAccount monté → API k8s | `automountServiceAccountToken: false` + règle conftest | 4B |

4. **Ajouter les risques hors STRIDE**, dans le même document :
   - **risque juridique** : les CGU Communauto n'autorisent pas explicitement
     l'usage automatisé. En lecture seule sur un endpoint public et à 20 s
     d'intervalle, on reste dans un usage raisonnable ; **réserver**
     automatiquement change la nature du risque (phase 8) ;
   - **dérive de schéma silencieuse** : l'API n'est pas documentée et peut
     changer sans préavis. C'est le mode de panne le plus probable du projet,
     et le seul contre lequel un contrôle de sécurité classique ne fait rien.
     D'où `ErrSchemaDrift` + l'alerte `FlexwatchSchemaDrift`.

5. **Exporter** le modèle : `docs/threat-model/flexwatch.threatdragon.json` +
   une capture du diagramme dans `docs/threat-model/`. Committer les deux.

**Pourquoi ça compte.** Un contrôle qu'on ne peut pas rattacher à une menace
est du bruit ; une menace sans contrôle est une dette qu'on assume
explicitement. Ce tableau est aussi la meilleure réponse en entretien à « et
pourquoi tu as mis ça ? ».

**Terminé quand.**
- le modèle Threat Dragon est committé et son diagramme montre les 4 frontières ;
- chaque ligne du tableau STRIDE pointe vers une phase ou est marquée
  « risque accepté » avec sa raison ;
- au moins un contrôle du tableau est **testé** dans le code (celui du token
  Telegram l'est : `TestTelegramErrorsNeverLeakToken`).

---

## Phase 2 — CI : qualité et sécurité

**Objectif.** Qu'aucun code non formaté, non linté, vulnérable ou porteur d'un
secret n'atteigne `main`.

**Outil.** GitHub Actions, golangci-lint v2, staticcheck, govulncheck,
gitleaks, Trivy, SonarQube, zizmor.

Fichier de référence : **`.github/workflows/ci.yml`** (déjà écrit).

### Étapes

1. **Pousser le workflow et regarder le premier run échouer.** C'est normal et
   c'est utile : on veut savoir ce que chaque job détecte avant de le corriger.
   ```powershell
   git push
   gh run watch
   ```

2. **Corriger job par job**, dans cet ordre : `quality` → `lint` →
   `govulncheck` → `trivy-repo` → `secrets-scan`.

   **Reproduire un échec en local avant de re-pousser.** Deux allers-retours de
   CI coûtent plus cher qu'une installation d'outil :
   ```bash
   trivy fs --scanners vuln,secret,misconfig --severity HIGH,CRITICAL \
     --ignore-unfixed --ignorefile .trivyignore.yaml --exit-code 1 \
     --skip-dirs terraform/envs/prod/.terraform .
   ```

3. **Traiter les findings Trivy — et la discipline qui va avec.** Le premier
   scan a remonté 4 misconfigurations sur l'IaC. La règle appliquée : *on
   corrige ce qui peut l'être, on date ce qui ne peut pas l'être, on ne
   désactive jamais le scanner ni ne baisse le seuil de sévérité.*

   | Finding | Traitement |
   |---|---|
   | `AWS-0164` (HIGH) — le subnet attribue une IP publique | **Corrigé.** `map_public_ip_on_launch = false` ; l'instance demande son IP explicitement. Gain réel : une instance future ne reçoit plus d'IP publique par accident. |
   | `AWS-0104` (CRITICAL ×3) — sortie vers `0.0.0.0/0` | **Risque accepté, daté.** Les deux API cibles sont derrière des CDN : un security group ne filtre que par IP, pas par domaine. Compensé par la restriction aux ports 443/53/123, la NetworkPolicy qui exclut l'IMDS, et les Flow Logs. |

   Les exceptions vivent dans **`.trivyignore.yaml`**, avec pour chacune un
   `statement` (pourquoi) et un `expired_at` (jusqu'à quand). À l'échéance,
   Trivy la rejette et le sujet revient à l'ordre du jour — c'est ce qui
   distingue un risque *accepté* d'un risque *oublié*.

   Deux détails qui font échouer silencieusement une exception :
   - le format YAML n'est **pas** détecté automatiquement (contrairement au
     `.trivyignore` historique) : il faut `--ignorefile` / l'entrée
     `trivyignores` de l'action ;
   - Trivy affiche `AWS-0104` mais l'identifiant canonique est `AVD-AWS-0104` :
     lister les deux formes évite une exception qui ne s'applique pas.

4. **Épingler la version du binaire Trivy** (`version: v0.74.0`). Celle par
   défaut de l'action prend du retard, et un scanner en retard rate des CVE
   récentes. C'est la seule version qu'on met à jour *volontairement*, pas au
   petit bonheur de l'action.

5. **Monter SonarQube Cloud.** Gratuit sur dépôt public — c'est une des
   raisons d'avoir rendu celui-ci public.

   **L'ordre compte**, sans quoi la CI passe au rouge entre deux étapes :

   a. **Importer le projet** sur [sonarcloud.io](https://sonarcloud.io) →
      *Analyze new project* → `Sekuryn/flexwatch`.
      `sonar-project.properties` étant déjà committé, SonarQube Cloud en déduit
      un montage CI-based et propose le bon tutoriel.

   b. **Désactiver l'Automatic Analysis** : *Administration → Analysis Method →
      Automatic Analysis : Off*. Ce n'est pas optionnel — **la couverture Go
      n'est pas supportée en automatic analysis**, et laisser les deux modes
      actifs fait échouer le scan sur un conflit de méthodes.

   c. **Vérifier la clé d'organisation** affichée dans l'UI et l'aligner avec
      `sonar.organization` du fichier de propriétés (on a supposé `sekuryn`).

   d. **Générer un token** (*My Account → Security*) puis :
      ```bash
      gh secret set SONAR_TOKEN          # colle le token quand il le demande
      ```

   e. **En DERNIER**, activer le job :
      ```bash
      gh variable set SONAR_HOST_URL --body https://sonarcloud.io
      ```

   Le job est conditionné à `vars.SONAR_HOST_URL != ''` : tant que la variable
   est absente, il est `skipped` et la CI reste verte. La poser avant le token
   rendrait la CI rouge — d'où l'ordre.

   **Le blocage sur gate rouge est porté par `sonar.qualitygate.wait=true`**
   dans `sonar-project.properties`, méthode documentée par SonarSource. Pas
   d'action `sonarqube-quality-gate-action` : elle ferait doublon et dépendrait
   d'un `report-task.txt` — une pièce mobile de plus pour le même résultat.

   f. **Donner le token à Dependabot aussi** — piège non évident :
      ```bash
      gh secret set SONAR_TOKEN --app dependabot   # meme valeur
      ```
      Les secrets Actions et les secrets Dependabot sont **deux magasins
      distincts**. Une PR Dependabot n'a pas accès aux secrets Actions : sans
      ce doublon, le job `sonarqube` y échouerait faute de token. Et s'il est
      devenu un check requis, **plus aucune PR Dependabot ne pourrait être
      mergée** — la même impasse que la règle CodeQL, en plus sournois.

   g. Une fois le premier scan vert sur `main` ET le secret Dependabot posé,
      **ajouter `sonarqube` aux checks requis** du ruleset `Protect main`, et
      les badges quality gate / couverture au README. Pas avant : exiger un
      check qui ne peut pas aboutir bloque toutes les PR.

   > **Fait le 2026-09-18.** Premier scan CI-based sur `main` : quality gate
   > **OK**, couverture **70,5 %**, 0 bug, 0 security hotspot. Le passage de
   > l'automatic analysis au CI-based a changé trois choses visibles :
   > la couverture est enfin mesurée (elle était absente), `ncloc` est tombé de
   > 4711 à 2340 lignes (les `sonar.exclusions` sont respectées — Terraform,
   > policies et manifests ne sont plus comptés comme du code applicatif), et
   > les vulnérabilités de 20 à 15.

6. **Régler la quality gate** sur le *new code* : 0 bug, 0 vulnérabilité, 0
   security hotspot non revu, couverture ≥ 70 % sur le code nouveau. Ne pas
   viser 90 % global : une couverture gonflée par des tests sans assertion est
   pire qu'une couverture honnête de 70 %.

7. **Épingler les actions par SHA** et auditer les workflows.

   > **Fait le 2026-09-18.** `pinact` a épinglé les **18 actions** des deux
   > workflows. Deux effets, pas un seul :
   > - un tag (`@v7`) est mutable, il peut être repoussé sur un autre commit ;
   >   un SHA ne l'est pas. C'est ce qui empêche qu'une action compromise
   >   s'exécute avec les permissions du workflow ;
   > - les **majeures flottantes** (`@v0`, `@v1`, `@v3`, `@v4`, `@v9`) étaient
   >   les plus exposées — elles se mettaient à jour toutes seules, sans revue.
   >   Elles sont maintenant figées sur des versions concrètes.
   >
   > Contrepartie assumée : plus rien ne se met à jour tout seul. C'est
   > Dependabot qui prend le relais (PR hebdomadaire groupée, il met à jour le
   > SHA **et** le commentaire de version). Et la régression est surveillée
   > sans outil supplémentaire : SonarQube signale toute action non épinglée
   > (`githubactions:S7637`), et c'est un check requis.

   Les commandes :
   ```bash
   go install github.com/suzuki-shunsuke/pinact/cmd/pinact@latest
   pinact run                      # remplace @v5 par @<sha> # v5
   pipx install zizmor && zizmor .github/workflows/
   ```
   `zizmor` détecte notamment les `pull_request_target` dangereux et les
   injections de template — deux façons classiques de voler les secrets d'une CI.

8. **Vérifier le principe du moindre privilège du workflow** : `permissions:
   contents: read` au niveau racine, et uniquement le job `image` qui ouvre
   `packages: write` et `id-token: write`.

**Pourquoi ça compte.** La CI est le seul endroit où un contrôle s'applique à
*tous* les changements sans dépendre de la discipline du développeur. Et une CI
elle-même trop permissive est une cible : un workflow avec `write-all` et un
tag d'action mutable, c'est une porte ouverte sur le registre de production.

**Terminé quand.**
- les 6 jobs de base passent en vert sur une PR ;
- une PR volontairement fautive est **bloquée** — à tester pour de vrai :
  ajouter `password := "hunter2"`, du code mal formaté, et un `http.Get` sans
  contexte, puis vérifier que `secrets-scan`, `quality` et `lint` échouent ;
- la quality gate Sonar est appliquée sur le new code ;
- `zizmor` ne remonte plus de finding de sévérité haute.

---

## Phase 3 — Build et supply chain

**Objectif.** Produire une image minimale, savoir exactement ce qu'elle
contient (SBOM), et pouvoir prouver qu'elle vient bien de cette CI (signature).

**Outil.** Docker buildx, distroless, Syft, Trivy, cosign/Sigstore.

Fichier de référence : **`Dockerfile`** (déjà écrit).

### Étapes

1. **Construire en local, pour la cible ARM** (l'EC2 est Graviton) :
   ```powershell
   docker buildx build --platform linux/arm64 --build-arg VERSION=dev -t flexwatch:dev --load .
   docker images flexwatch:dev      # attendu : ~8-12 Mo
   ```

2. **Vérifier le durcissement de l'image**, pas seulement sa taille :
   ```bash
   docker run --rm flexwatch:dev -version
   docker run --rm --entrypoint /bin/sh flexwatch:dev   # doit ÉCHOUER : pas de shell
   docker inspect flexwatch:dev --format '{{.Config.User}}'  # 65532:65532
   ```

3. **Épingler les images de base par digest.** Les `ARG` du Dockerfile
   acceptent un digest ; relever les valeurs et les committer :
   ```bash
   docker buildx imagetools inspect golang:1.23-alpine | grep Digest
   docker buildx imagetools inspect gcr.io/distroless/static-debian12:nonroot | grep Digest
   ```

4. **Générer et lire le SBOM** :
   ```bash
   syft flexwatch:dev -o cyclonedx-json=sbom.json
   syft flexwatch:dev -o table          # lecture humaine
   ```
   Sur ce projet, la liste doit être **très courte** : le binaire Go n'a aucune
   dépendance tierce et l'image de base n'a ni shell ni gestionnaire de
   paquets. C'est le résultat concret de la décision « stdlib uniquement ».

5. **Scanner** :
   ```bash
   trivy image --severity HIGH,CRITICAL --ignore-unfixed flexwatch:dev
   trivy sbom sbom.json                 # scan du SBOM, sans l'image
   ```

6. **Signer et attester** (la CI le fait ; le faire une fois à la main pour
   comprendre ce qui se passe) :
   ```bash
   cosign sign --yes ghcr.io/sekuryn/flexwatch@sha256:<digest>
   cosign attest --yes --predicate sbom.json --type cyclonedx \
     ghcr.io/sekuryn/flexwatch@sha256:<digest>
   ```

7. **Vérifier depuis un poste tiers** — c'est l'étape que tout le monde oublie,
   et la seule qui prouve que la signature sert à quelque chose. Le dépôt
   fournit un script qui enchaîne les trois contrôles (signature, attestation
   SBOM, et un **contrôle négatif** vérifiant qu'une autre identité est bien
   refusée) :
   ```bash
   ./scripts/verify-image.sh sha256:<digest affiche par la CI>
   ```

   Deux pièges rencontrés en le construisant :
   - `cosign verify ... | head` renvoie le code de `head`, donc **toujours 0**.
     Un script de vérification qui annonce « OK » quoi qu'il arrive est pire
     que pas de script ;
   - le contrôle négatif n'a de sens que si le contrôle positif a réussi. Sur
     un paquet privé sans authentification, il « passe » parce que le registre
     refuse l'accès — pas parce que l'identité est mauvaise. Le script le
     marque désormais « non concluant » dans ce cas.

   Les commandes sous-jacentes, pour comprendre ce que fait le script :
   ```bash
   cosign verify ghcr.io/sekuryn/flexwatch@sha256:<digest> \
     --certificate-identity-regexp 'https://github.com/Sekuryn/flexwatch/.github/workflows/ci.yml@.*' \
     --certificate-oidc-issuer https://token.actions.githubusercontent.com

   cosign verify-attestation ghcr.io/sekuryn/flexwatch@sha256:<digest> \
     --type cyclonedx \
     --certificate-identity-regexp 'https://github.com/Sekuryn/flexwatch/.*' \
     --certificate-oidc-issuer https://token.actions.githubusercontent.com
   ```

**Pourquoi ça compte.** Le SBOM répond à « suis-je affecté par la CVE du
jour ? » en quelques secondes au lieu d'une demi-journée d'archéologie. La
signature keyless répond à « cette image vient-elle bien de mon code ? » sans
clé privée à stocker, faire tourner et perdre.

**Terminé quand.**
- l'image fait moins de 15 Mo, tourne en UID 65532 et n'a pas de shell ;
- `sbom.json` est produit par la CI et attaché à l'image ;
- `cosign verify` réussit depuis une machine qui n'a jamais vu le dépôt ;
- `trivy image` ne remonte aucun HIGH/CRITICAL corrigeable.

> **Fait le 2026-09-18.** `./scripts/verify-image.sh` a validé la signature et
> l'attestation SBOM de
> `ghcr.io/sekuryn/flexwatch@sha256:7e71dbc2...`, et refusé une identité
> étrangère. Le certificat Fulcio porte :
> ```
> Subject                  https://github.com/Sekuryn/flexwatch/.github/workflows/ci.yml@refs/heads/main
> Issuer                   https://token.actions.githubusercontent.com
> githubWorkflowSha        bd5c831722051180c42cf39f328e1c4c7ca822bb
> githubWorkflowTrigger    push
> ```
> C'est la propriété qui compte : la signature relie l'image à un **commit
> précis** et à un **workflow précis**. Personne ne peut produire une image
> acceptée par Kyverno sans passer par ce workflow sur ce dépôt.
>
> Prérequis découvert au passage : rendre le dépôt public ne rend PAS le paquet
> GHCR public. C'est un réglage séparé (Packages → settings → Change
> visibility), et sans lui `cosign verify` échoue en `UNAUTHORIZED` — une
> signature que personne ne peut vérifier ne sert à rien.

---

## Phase 4A — Architecture AWS : décision et préparation du compte

**Objectif.** Choisir l'architecture cible en connaissance de cause, et
préparer le compte AWS avant d'y créer quoi que ce soit.

### Les deux options

**Option A — EC2 ARM (`t4g.small`) + k3s, application en conteneur.**

```
          Internet
             │ 443 sortant uniquement
   ┌─────────▼──────────────────────────────────┐
   │ VPC 10.42.0.0/16   subnet public 10.42.1.0/24│
   │  ┌──────────────────────────────────────┐   │
   │  │ EC2 t4g.small — Amazon Linux 2023    │   │
   │  │  k3s (server unique)                 │   │
   │  │   ├─ ns flexwatch   : le bot         │   │
   │  │   ├─ ns kyverno     : admission      │   │
   │  │   ├─ ns falco       : runtime (eBPF) │   │
   │  │   └─ ns monitoring  : Prom/Graf/Loki │   │
   │  └──────────────────────────────────────┘   │
   │  SG : AUCUNE entrée. Sorties 443/53/123.    │
   └─────────────────────────────────────────────┘
        │ rôle IAM (pas de clés)
        ▼
   ECR (pull) · Secrets Manager (read) · CloudWatch (flow logs)
   Administration : SSM Session Manager (aucun port 22)
```

**Option B — ECS Fargate + ECR, sans Kubernetes.**

```
   Internet ◀─ NAT ou subnet public ─ Tâche Fargate (arm64)
                                       │ task role
                                       ▼
                       ECR · Secrets Manager (injection native)
   Observabilité : CloudWatch Logs + Container Insights
```

### Comparaison honnête

| Critère | Option A (EC2 + k3s) | Option B (Fargate) |
|---|---|---|
| Coût mensuel | ~18 USD (fixe) | ~10–12 USD (+ NAT 35 USD si subnet privé) |
| Effort d'exploitation | patcher l'OS, gérer k3s | quasi nul |
| Surface d'attaque | plus large (hôte + cluster) | réduite (pas d'hôte à toi) |
| **Kyverno (admission)** | **oui** | non (pas d'admission control) |
| **Falco (runtime)** | **oui** (eBPF, accès kernel) | non (pas d'accès kernel) |
| NetworkPolicy | oui | non (security groups seulement) |
| Injection de secrets | ESO ou sync hôte | native, plus simple |
| Valeur portfolio DevSecOps | élevée | faible |

### Recommandation : **option A**

Pour ce projet précis, l'option A est la bonne — non pas parce qu'elle est
techniquement supérieure (pour faire tourner un binaire de 10 Mo, Fargate est
objectivement plus sain), mais parce que **l'objectif est de démontrer la
sécurité de la chaîne de bout en bout**. Kyverno et Falco sont impossibles sur
Fargate, et ce sont précisément les deux contrôles qui rendent le reste du
projet crédible : sans Kyverno, la signature cosign de la phase 3 n'est jamais
*vérifiée* par personne ; sans Falco, rien ne détecte un comportement anormal à
l'exécution.

Dit autrement : sur Fargate, la phase 3 produit une signature que personne ne
lit. Sur k3s, elle bloque un déploiement non signé — et ça se démontre en 30
secondes devant un recruteur.

*Note d'honnêteté à mettre dans le README du portfolio : sur un vrai service en
production mono-conteneur sans besoin d'admission control, Fargate serait le
choix raisonnable. Savoir arbitrer entre les deux vaut mieux que de connaître
l'un des deux.*

### Étapes de préparation du compte (avant tout `terraform apply`)

1. **Ne jamais travailler en root.** Créer un utilisateur IAM ou (mieux) un
   accès via IAM Identity Center, avec MFA :
   *IAM → Users → Security credentials → MFA*. Le compte root reste avec MFA
   matériel et sans clés d'accès.

2. **Activer les garde-fous du compte** (console, une fois) :
   - CloudTrail : un trail multi-régions vers un bucket S3 dédié ;
   - S3 : *Block all public access* au niveau du compte ;
   - EBS : *Encryption by default* dans la région ;
   - IAM Access Analyzer : un analyseur au niveau du compte.

3. **Poser un garde-fou de coût AVANT de créer des ressources** :
   ```bash
   aws budgets create-budget --account-id <ID> --budget '{
     "BudgetName":"flexwatch-mensuel","BudgetLimit":{"Amount":"25","Unit":"USD"},
     "TimeUnit":"MONTHLY","BudgetType":"COST"}' \
     --notifications-with-subscribers '[{
       "Notification":{"NotificationType":"ACTUAL","ComparisonOperator":"GREATER_THAN",
       "ThresholdType":"PERCENTAGE","Threshold":80},
       "Subscribers":[{"SubscriptionType":"EMAIL","Address":"<ton-email>"}]}]'
   ```

4. **Configurer la CLI** sur la région du projet :
   ```powershell
   aws configure set region ca-central-1
   aws sts get-caller-identity        # confirme QUI tu es avant d'agir
   ```
   *`ca-central-1` (Montréal) : la latence est anecdotique ici, mais garder les
   données d'un service montréalais au Canada est le choix par défaut correct.*

5. **Installer le plugin Session Manager** (l'accès shell sans port 22) :
   ```powershell
   winget install --id Amazon.SessionManagerPlugin
   ```

**Pourquoi ça compte.** L'hygiène du compte est la seule couche qu'on ne peut
pas rattraper après coup : sans CloudTrail activé *avant* l'incident, il n'y a
rien à analyser. Et un budget alarm est le contrôle de sécurité le plus
rentable d'un projet perso — la menace la plus probable n'est pas un attaquant,
c'est une ressource oubliée.

**Terminé quand.**
- `aws sts get-caller-identity` renvoie un utilisateur **non root** avec MFA ;
- CloudTrail écrit dans son bucket, chiffrement EBS par défaut actif ;
- le budget de 25 USD existe et l'alerte à 80 % a été reçue en test ;
- le choix de l'option A est écrit et justifié dans ton portfolio.

---

## Phase 5 — IaC : Terraform validé par OPA avant `apply`

**Objectif.** Décrire l'infrastructure en code, la faire relire par des
policies, puis l'appliquer — dans cet ordre, jamais l'inverse.

**Outil.** Terraform ≥ 1.10, conftest/OPA, tflint, Trivy (misconfig).

Fichiers de référence : **`terraform/`** et **`policy/terraform/`** (déjà écrits).

### Étapes

1. **Créer le backend d'état à la main** (Terraform ne peut pas gérer le
   backend qu'il utilise — problème de la poule et de l'œuf) :
   ```bash
   BUCKET=flexwatch-tfstate-$(aws sts get-caller-identity --query Account --output text)
   aws s3api create-bucket --bucket "$BUCKET" --region ca-central-1 \
     --create-bucket-configuration LocationConstraint=ca-central-1
   aws s3api put-bucket-versioning --bucket "$BUCKET" \
     --versioning-configuration Status=Enabled
   aws s3api put-bucket-encryption --bucket "$BUCKET" \
     --server-side-encryption-configuration \
     '{"Rules":[{"ApplyServerSideEncryptionByDefault":{"SSEAlgorithm":"AES256"}}]}'
   aws s3api put-public-access-block --bucket "$BUCKET" \
     --public-access-block-configuration \
     'BlockPublicAcls=true,IgnorePublicAcls=true,BlockPublicPolicy=true,RestrictPublicBuckets=true'
   echo "$BUCKET"   # à reporter dans terraform/envs/prod/backend.tf
   ```
   *Le versioning n'est pas un détail : c'est ce qui permet de revenir en
   arrière après un `terraform state rm` malheureux.*

2. **Relever l'empreinte de l'installeur k3s** (exigée par la validation de
   variable, cf. le threat model, ligne « Tampering / get.k3s.io ») :
   ```bash
   curl -sfL https://get.k3s.io | sha256sum
   ```
   La reporter dans `terraform.tfvars`.

3. **Préparer les variables** :
   ```bash
   cd terraform/envs/prod
   cp terraform.tfvars.example terraform.tfvars   # ignoré par git
   # renseigner owner et k3s_installer_sha256
   ```

4. **Init, format, validation statique** :
   ```bash
   terraform init
   terraform fmt -check -recursive ../..   # attention : sans -recursive, les modules sont ignorés
   terraform validate
   tflint --recursive          # erreurs de typage/provider que validate rate
   trivy config .              # misconfigurations connues (checks tfsec)
   ```

   Puis **verrouiller les empreintes des providers pour toutes les
   plateformes**. C'est le `go.sum` de Terraform, et `terraform init` n'y
   inscrit que l'empreinte de la machine courante : une CI Linux échouerait
   alors la vérification faite depuis un poste Windows.
   ```bash
   terraform providers lock -platform=linux_amd64 -platform=windows_amd64
   ```
   `.terraform.lock.hcl` **doit être committé** — il n'est pas dans le
   `.gitignore`, c'est volontaire. Sans lui, rien ne garantit que le provider
   téléchargé demain est celui qui a été validé aujourd'hui.

   > Le répertoire `.terraform/` pèse ~675 Mo (binaire du provider AWS). Il est
   > ignoré par git, et le supprimer ne coûte qu'un `terraform init` de plus.

5. **Produire le plan, puis le faire valider par les policies** — c'est le
   cœur de cette phase :
   ```bash
   terraform plan -out=tfplan
   terraform show -json tfplan > tfplan.json
   conftest test --policy ../../../policy/terraform tfplan.json
   ```
   Les policies refusent notamment : volume racine non chiffré, IMDSv1 actif,
   entrée `0.0.0.0/0`, port 22 ouvert, `Action: "*"`, `Resource: "*"` (hors
   exception documentée `ecr:GetAuthorizationToken`), tags ECR mutables,
   **valeur de secret présente dans le plan**, log group sans rétention, tags
   obligatoires manquants.

6. **Vérifier que les policies fonctionnent vraiment** — une policy qui ne
   refuse jamais rien est pire qu'aucune policy :
   ```bash
   conftest verify --policy ../../../policy/          # tests unitaires des regos
   ```
   Puis une vérification empirique : passer temporairement
   `http_tokens = "optional"` dans `modules/compute/main.tf`, régénérer le
   plan, constater le refus, **annuler la modification**.

7. **Appliquer**, en lisant le plan avant de confirmer :
   ```bash
   terraform apply tfplan
   rm -f tfplan tfplan.json     # tfplan.json contient l'état cible en clair
   terraform output next_steps
   ```

8. **Ancrer l'enchaînement dans un script local**, pour ne jamais faire
   d'`apply` sans conftest — `scripts/tf-guarded-apply.sh` :
   ```bash
   #!/usr/bin/env bash
   set -euo pipefail
   terraform plan -out=tfplan
   terraform show -json tfplan > tfplan.json
   conftest test --policy ../../../policy/terraform tfplan.json
   terraform apply tfplan
   rm -f tfplan tfplan.json
   ```

**Pourquoi ça compte.** La policy porte sur le **plan**, pas sur le HCL : le
plan est l'état cible réel, variables et modules résolus, donc impossible à
contourner par une variable bien placée. Et cette validation est le seul
moment où l'on peut refuser une infrastructure dangereuse avant qu'elle existe.

**Terminé quand.**
- `terraform fmt -check -recursive` et `terraform validate` passent, et
  `.terraform.lock.hcl` est committé avec les deux plateformes ;
- `conftest verify` passe (les policies sont testées) ;
- `conftest test` sur le plan final ne renvoie **aucun** `deny` ;
- une dégradation volontaire (IMDSv1) est bien refusée, puis annulée ;
- `terraform apply` a réussi et `terraform output` donne l'`instance_id` ;
- `tfplan.json` n'existe plus sur le disque.

---

## Phase 4B — k3s, admission, runtime, déploiement

**Objectif.** Faire tourner l'image signée sur le cluster, avec Kyverno qui
vérifie la signature et Falco qui surveille le comportement.

**Outil.** SSM Session Manager, kubectl, Helm, Kyverno, Falco.

> **Budget mémoire du nœud — à lire avant de tout installer.** Un `t4g.small`
> a 2 Gio. Répartition observée : k3s ≈ 500 Mio, Kyverno ≈ 300 Mio, Falco
> (modern eBPF) ≈ 250 Mio, flexwatch ≈ 20 Mio. Il reste ≈ 900 Mio, ce qui est
> juste pour Prometheus + Grafana + Loki. **Deux options honnêtes** : passer en
> `t4g.medium` (4 Gio, ≈ 26 USD/mois) pour la stack complète, ou rester en
> `small` et envoyer les métriques/logs vers Grafana Cloud (offre gratuite).
> Choisis avant la phase 7, pas pendant.

### Étapes

1. **Se connecter sans SSH** :
   ```bash
   aws ssm start-session --target "$(terraform -chdir=terraform/envs/prod output -raw instance_id)"
   sudo -i
   kubectl get nodes          # k3s a été installé par le user_data
   journalctl -u k3s -n 50    # et vérifier que rien n'a échoué au bootstrap
   cat /var/log/flexwatch-bootstrap.log
   ```
   *Aucun port 22 n'est ouvert et aucune clé SSH n'existe : les sessions SSM
   sont journalisées côté AWS, donc auditables — contrairement à un accès SSH
   avec une clé partagée.*

2. **Installer Helm** puis **Kyverno** :
   ```bash
   curl -fsSL https://raw.githubusercontent.com/helm/helm/main/scripts/get-helm-3 -o get-helm.sh
   sha256sum get-helm.sh     # même principe que pour k3s : vérifier avant d'exécuter
   bash get-helm.sh

   helm repo add kyverno https://kyverno.github.io/kyverno
   helm install kyverno kyverno/kyverno -n kyverno --create-namespace \
     --set admissionController.replicas=1 \
     --set backgroundController.resources.limits.memory=256Mi
   kubectl -n kyverno rollout status deploy/kyverno-admission-controller
   ```

3. **Appliquer les policies d'admission** :
   ```bash
   kubectl apply -f policy/kyverno/verify-images.yaml
   kubectl get clusterpolicy
   ```

4. **Tester que l'admission refuse vraiment** — étape non négociable, c'est
   elle qui donne un sens à la phase 3 :
   ```bash
   kubectl create ns flexwatch
   # une image non signée, dans le namespace surveillé :
   kubectl -n flexwatch run pirate --image=nginx:latest
   # attendu : erreur d'admission « image is not signed » / registre non autorisé
   kubectl -n flexwatch get events --sort-by=.lastTimestamp | tail -5
   ```

5. **Installer Falco** (détection au runtime) :
   ```bash
   helm repo add falcosecurity https://falcosecurity.github.io/charts
   helm install falco falcosecurity/falco -n falco --create-namespace \
     --set driver.kind=modern_ebpf \
     --set tty=true \
     --set falcosidekick.enabled=false \
     --set metrics.enabled=true
   kubectl -n falco logs -l app.kubernetes.io/name=falco --tail=20
   ```
   *`modern_ebpf` plutôt que le module noyau : pas de compilation de driver à
   chaque mise à jour du kernel, et pas de code privilégié chargé dans le noyau.*

6. **Tester Falco** avec un comportement anormal, dans un pod jetable :
   ```bash
   kubectl -n default run tester --image=busybox:1.36 --restart=Never -it -- sh
   # dans le conteneur :
   cat /etc/shadow
   exit
   kubectl -n falco logs -l app.kubernetes.io/name=falco | grep -i "sensitive file"
   ```
   Cela doit produire un événement. *Sur flexwatch lui-même, Falco ne verra
   jamais rien de tel — l'image n'a ni shell ni `cat`. C'est exactement
   l'intérêt de distroless : la détection n'a même pas à intervenir.*

7. **Passer le banc d'essai local AVANT de déployer.** Il fait tourner le vrai
   binaire contre un faux serveur (API Communauto + API Telegram) et vérifie
   la chaîne complète, y compris qu'une apparition déclenche bien un message
   et qu'un état stable n'en déclenche pas un deuxième :
   ```bash
   make e2e     # ~60 s, 19 assertions ; détail dans README.md
   ```
   Un déploiement dont le binaire n'a pas passé ce test fait dépendre la
   première vérification de la production — ce qui est exactement ce qu'on
   cherche à éviter.

8. **Déployer l'application.** D'abord le namespace et la configuration,
   ensuite le secret (phase 6), enfin le Deployment :
   ```bash
   kubectl apply -f deploy/k8s/00-namespace.yaml
   kubectl apply -f deploy/k8s/10-config.yaml
   kubectl apply -f deploy/k8s/30-service-netpol.yaml

   # Résoudre le digest de l'image et le mettre dans le manifest :
   crane digest ghcr.io/sekuryn/flexwatch:main   # ou : docker buildx imagetools inspect
   # remplacer le digest de 20-deployment.yaml, puis :
   kubectl apply -f deploy/k8s/20-deployment.yaml
   kubectl -n flexwatch rollout status deploy/flexwatch
   ```

9. **Vérifier le fonctionnement réel**, pas seulement que le pod est `Running` :
   ```bash
   kubectl -n flexwatch logs -l app=flexwatch --tail=30
   # attendu : surveillance demarree, puis "etat initial enregistre"
   kubectl -n flexwatch port-forward deploy/flexwatch 2112:2112 &
   curl -s localhost:2112/metrics | grep -E 'flexwatch_(polls|vehicles)'
   curl -s localhost:2112/readyz
   ```

10. **Vérifier la NetworkPolicy** (le contrôle anti-SSRF du threat model) :
   ```bash
   kubectl -n flexwatch exec deploy/flexwatch -- /flexwatch -version   # OK
   # Depuis un pod de test DANS le namespace flexwatch, l'IMDS doit être injoignable :
   kubectl -n flexwatch run probe --image=curlimages/curl:8.10.1 --restart=Never -it -- \
     curl -m 3 -s http://169.254.169.254/latest/meta-data/
   # attendu : timeout. Si ça répond, la NetworkPolicy n'est pas appliquée
   # (k3s embarque Flannel : vérifier que le contrôleur de NetworkPolicy est actif).
   ```

11. **Ingress — et pourquoi flexwatch n'en a pas.**
    L'application est un worker **sortant** : elle n'a aucune API à offrir.
    Lui donner un Ingress reviendrait à publier `/metrics` sur Internet, donc à
    publier l'adresse du géofence (cf. threat model, « Info disclosure »).
    Le seul composant qui mérite une interface est Grafana, et le bon défaut
    reste le tunnel :
    ```bash
    # depuis le poste Windows, sans rien exposer :
    aws ssm start-session --target <instance-id> \
      --document-name AWS-StartPortForwardingSession \
      --parameters '{"portNumber":["3000"],"localPortNumber":["3000"]}'
    # puis http://localhost:3000
    ```
    Si tu veux **quand même** un Ingress public pour la démonstration (k3s
    embarque Traefik), le minimum acceptable est : TLS via cert-manager,
    authentification devant Grafana, et une règle de SG ouvrant 443 — donc un
    retour en arrière assumé sur « aucune entrée », à documenter :
    ```bash
    helm repo add jetstack https://charts.jetstack.io
    helm install cert-manager jetstack/cert-manager -n cert-manager \
      --create-namespace --set crds.enabled=true
    kubectl apply -f deploy/k8s/60-ingress-grafana.yaml   # à écrire si ce choix est fait
    ```

**Pourquoi ça compte.** C'est ici que la sécurité devient vérifiable plutôt
que déclarative : Kyverno refuse une image non signée *devant témoin*, Falco
produit un événement *devant témoin*, et la NetworkPolicy fait échouer une
requête vers l'IMDS *devant témoin*. Trois démonstrations de 30 secondes.

**Terminé quand.**
- `kubectl -n flexwatch get pods` montre 1/1 `Running` et `/readyz` répond 200 ;
- un `kubectl run` avec une image non signée est **refusé** par Kyverno ;
- Falco a produit au moins un événement de test ;
- la requête vers 169.254.169.254 depuis le namespace `flexwatch` **échoue** ;
- une notification est arrivée (console dans les logs, ou Telegram).

---

## Phase 6 — Secrets

**Objectif.** Que le token Telegram n'existe en clair qu'à deux endroits :
AWS Secrets Manager, et la mémoire du process.

**Outil.** AWS Secrets Manager, systemd timer (retenu) ou External Secrets
Operator (documenté), gitleaks.

### Étapes

1. **Créer le bot et récupérer les valeurs** : `@BotFather` → token ;
   `@userinfobot` → chat id.

2. **Écrire la valeur du secret hors Terraform.** La ressource
   `aws_secretsmanager_secret` est créée par Terraform, mais **jamais sa
   valeur** — une valeur passée à Terraform finit en clair dans l'état S3,
   lisible par quiconque accède au bucket :
   ```bash
   aws secretsmanager put-secret-value \
     --secret-id "$(terraform -chdir=terraform/envs/prod output -raw telegram_secret_arn)" \
     --secret-string '{"token":"123456:ABC...","chat_id":"987654321"}'
   ```
   *Attention : cette commande met le token dans l'historique du shell. Le
   préfixer d'un espace (bash, si `HISTCONTROL=ignorespace`) ou passer par
   `--secret-string fileb://secret.json` puis supprimer le fichier.*
   C'est exactement le genre de détail que la policy conftest
   `test_secret_en_clair_dans_le_plan_refuse` empêche d'oublier côté IaC.

3. **Installer la synchronisation côté hôte** (option retenue) :
   ```bash
   sudo install -m 0750 deploy/scripts/flexwatch-secret-sync.sh /usr/local/bin/
   sudo install -m 0644 deploy/systemd/flexwatch-secret-sync.* /etc/systemd/system/
   sudo dnf -y install jq
   sudo systemctl daemon-reload
   sudo systemctl enable --now flexwatch-secret-sync.timer
   sudo systemctl start flexwatch-secret-sync.service
   journalctl -u flexwatch-secret-sync -n 20
   ```

4. **Comprendre pourquoi ce n'est pas External Secrets Operator.** L'instance
   impose `http_put_response_hop_limit = 1` : les conteneurs ne peuvent pas
   atteindre l'IMDS, ce qui neutralise le vol de credentials d'instance depuis
   un pod compromis. ESO ne peut donc pas utiliser le rôle de l'instance, et il
   ne resterait que des clés AWS statiques stockées dans un Secret k8s — on
   remplacerait un secret par un secret **plus puissant**. Les alternatives,
   avec leur prix :

   | Option | Avantage | Prix à payer |
   |---|---|---|
   | Sync systemd (**retenue**) | aucune clé statique, aucun pod ne voit l'IMDS | du shell sur l'hôte, rotation par timer |
   | ESO + clés statiques | déclaratif, propre | des credentials AWS longue durée dans le cluster |
   | ESO + hop limit 2 | déclaratif ET sans clés | la protection IMDS dépend alors d'une NetworkPolicy correcte dans chaque namespace |

   Avec plusieurs nœuds, la troisième option redevient la bonne réponse.
   `deploy/k8s/50-externalsecret.yaml` est prêt pour ce jour-là.

5. **Vérifier la non-fuite**, dans les trois endroits où un secret fuit
   d'habitude :
   ```bash
   # 1. les logs applicatifs
   kubectl -n flexwatch logs -l app=flexwatch | grep -i -E '[0-9]{6,}:[A-Za-z0-9_-]{30,}'   # doit être vide
   # 2. le manifest et l'image
   kubectl -n flexwatch get deploy flexwatch -o yaml | grep -i token   # seulement secretRef
   docker run --rm flexwatch:dev -version    # le binaire ne contient aucun secret
   # 3. l'historique git
   gitleaks detect --no-git=false --redact
   ```
   Le test `TestTelegramErrorsNeverLeakToken` couvre déjà le cas le plus
   sournois : le token dans l'URL d'une erreur de transport `net/http`.

6. **Tester la rotation** — un secret qu'on ne sait pas faire tourner n'est pas
   géré :
   ```bash
   # révoquer le token via @BotFather, en créer un nouveau, puis :
   aws secretsmanager put-secret-value --secret-id <arn> --secret-string '{...}'
   sudo systemctl start flexwatch-secret-sync.service
   kubectl -n flexwatch rollout status deploy/flexwatch
   # attendu : les notifications repartent sans intervention sur le code
   ```

**Pourquoi ça compte.** Un secret dans git est compromis pour toujours, même
supprimé — l'historique le conserve. Et un secret qu'on ne peut pas faire
tourner en cinq minutes devient un secret qu'on ne fera jamais tourner.

**Terminé quand.**
- le token n'apparaît ni dans git, ni dans un manifest, ni dans l'image, ni
  dans les logs (les 4 vérifications ci-dessus sont faites) ;
- le timer systemd est actif et `journalctl` montre une synchronisation ;
- une rotation complète a été effectuée de bout en bout.

---

## Phase 7 — Observabilité

**Objectif.** Savoir, sans se connecter au serveur, si le bot fonctionne
vraiment — et pas seulement s'il tourne.

**Outil.** Prometheus, Alertmanager, Grafana, Loki + Promtail.

Fichiers de référence : **`observability/prometheus/prometheus.yml`** et
**`observability/prometheus/rules/flexwatch.rules.yml`** (déjà écrits).

### Ce que l'application expose déjà

| Métrique | Type | À quoi elle sert |
|---|---|---|
| `flexwatch_polls_total{result}` | counter | taux de succès, SLI du bot |
| `flexwatch_poll_errors_total{kind}` | counter | distinguer `http` / `network` / **`schema`** |
| `flexwatch_poll_duration_seconds` | histogram | latence de l'API amont (p50/p95) |
| `flexwatch_vehicles_in_city` | gauge | volume renvoyé pour la bbox |
| `flexwatch_vehicles_in_fence` | gauge | **la série de la heatmap** |
| `flexwatch_new_appearances_total` | counter | détections utiles |
| `flexwatch_disappearances_total` | counter | rotation du parc dans le rayon |
| `flexwatch_vehicles_undecodable_total` | counter | dérive partielle de schéma |
| `flexwatch_notifications_total{outcome}` | counter | succès/échec par sink |
| `flexwatch_last_success_timestamp_seconds` | gauge | **fraîcheur** — la métrique la plus importante |
| `flexwatch_build_info{version,go_version}` | gauge | relier un incident à une version |

### Étapes

1. **Installer la stack** (choix confirmé en phase 4B : `t4g.medium`, ou
   Grafana Cloud) :
   ```bash
   helm repo add prometheus-community https://prometheus-community.github.io/helm-charts
   helm repo add grafana https://grafana.github.io/helm-charts
   kubectl create ns monitoring
   kubectl label ns monitoring kubernetes.io/metadata.name=monitoring --overwrite

   helm install prometheus prometheus-community/prometheus -n monitoring \
     --set server.retention=15d \
     --set server.resources.limits.memory=512Mi \
     --set alertmanager.enabled=true
   ```
   *La NetworkPolicy de `deploy/k8s/30-service-netpol.yaml` n'autorise le
   scrape que depuis le namespace `monitoring` : le label ci-dessus est
   indispensable, sinon le scrape est bloqué et la cible reste `DOWN`.*

2. **Charger les règles d'alerte** :
   ```bash
   kubectl -n monitoring create configmap flexwatch-rules \
     --from-file=observability/prometheus/rules/flexwatch.rules.yml
   # puis référencer le configmap dans les values du chart (server.extraConfigmapMounts)
   ```
   Alertes fournies : `FlexwatchDown`, `FlexwatchPollsStale`,
   `FlexwatchPollErrorRate`, **`FlexwatchSchemaDrift`**,
   `FlexwatchUndecodableVehicles`, `FlexwatchFenceAlwaysEmpty`,
   `FlexwatchNotificationsFailing`.

3. **Vérifier que les alertes se déclenchent** — une alerte jamais testée est
   une alerte qui ne marchera pas le jour J :
   ```bash
   # simuler une panne d'API : casser l'URL amont
   kubectl -n flexwatch set env deploy/flexwatch FLEX_API_BASE_URL=https://invalid.example.com
   # attendre ~5 min : FlexwatchPollsStale doit passer en firing
   kubectl -n flexwatch set env deploy/flexwatch --containers=flexwatch FLEX_API_BASE_URL-
   ```

4. **Router les alertes vers le même Telegram que les détections** (
   Alertmanager `telegram_configs`), avec un chat **différent** : mélanger
   « une Flex est dispo » et « le bot est cassé » garantit qu'on finira par
   ignorer les deux.

5. **Installer Loki + Promtail** :
   ```bash
   helm install loki grafana/loki-stack -n monitoring \
     --set loki.persistence.enabled=false \
     --set promtail.enabled=true \
     --set grafana.enabled=false
   ```
   Les logs de l'app sont du **JSON via slog** : aucun parsing fragile côté
   Promtail, `json` suffit comme stage. Requêtes utiles :
   ```logql
   {namespace="flexwatch"} | json | msg = "nouveau vehicule dans le rayon"
   {namespace="flexwatch"} | json | level = "ERROR"
   {namespace="flexwatch"} | json | msg =~ "vehicules indecodables.*"
   ```

6. **Construire le dashboard Grafana** (4 panneaux suffisent) :
   - **Fraîcheur** (stat, seuils vert/rouge) :
     `time() - flexwatch_last_success_timestamp_seconds`
   - **Taux de succès 24 h** (gauge) : `flexwatch:poll_success_ratio:24h`
   - **Latence API** (timeseries) :
     `histogram_quantile(0.95, sum by (le) (rate(flexwatch_poll_duration_seconds_bucket[5m])))`
   - **Heatmap de disponibilité** (panneau *Heatmap*, axe Y = heure du jour) :
     ```promql
     avg_over_time(flexwatch_vehicles_in_fence[1h])
     ```
     avec la règle d'enregistrement `flexwatch:vehicles_in_fence:avg5m` comme
     source pour l'historique long. *C'est le panneau qui répond à la vraie
     question : « à quelle heure ai-je une chance de trouver une voiture près
     de chez moi ? »* Pour une heatmap **géographique** (par zone), il faut la
     persistance Postgres : `make build-postgres`, `FLEX_STORE_ENABLED=true`,
     puis un panneau Geomap sur `vehicle_position`.

7. **Sauvegarder le dashboard en JSON** dans
   `observability/grafana/flexwatch-dashboard.json` et le committer : un
   dashboard qui n'existe que dans une base Grafana éphémère disparaîtra.

**Pourquoi ça compte.** Le mode de panne le plus probable de ce projet n'est
pas un crash — c'est un bot qui tourne, répond `200` sur `/healthz`, et ne
détecte plus rien parce que le schéma de l'API a changé. Seules la fraîcheur
(`last_success_timestamp`) et la dérive de schéma (`kind="schema"`) rendent
cette panne visible. C'est la différence entre surveiller un process et
surveiller un service.

**Terminé quand.**
- la cible `flexwatch` est `UP` dans Prometheus (donc la NetworkPolicy et le
  label de namespace sont corrects) ;
- les 7 alertes sont chargées et **au moins une a été vue en `firing`** lors du
  test de panne simulée ;
- les logs JSON sont requêtables dans Grafana via Loki ;
- le dashboard 4 panneaux existe, est committé, et la heatmap montre des
  données sur au moins 24 h.

---

## Phase 8 — Phase 2 esquissée : réservation automatique (NON implémentée)

**Objectif.** Décrire ce que serait le mode `--auto`, et expliquer pourquoi il
n'est pas écrit aujourd'hui. Cette phase se lit ; elle ne s'exécute pas.

### Ce qu'il faudrait construire

1. **Authentification.** Partie authentifiée de l'API Reservauto : obtention
   d'un jeton à partir d'identifiants Communauto, rafraîchissement, gestion de
   l'expiration. Les identifiants iraient dans Secrets Manager, avec la même
   chaîne que le token Telegram (phase 6) — mais avec un impact bien supérieur
   en cas de fuite : **un compte Communauto, c'est un moyen de paiement.**

2. **Un module `internal/reserve` isolé**, avec une interface étroite :
   ```go
   type Reserver interface {
       Hold(ctx context.Context, vehicleID string) (HoldID, error)
       Release(ctx context.Context, id HoldID) error
   }
   ```
   Séparé du détecteur, testable avec un faux, et **jamais** appelé depuis un
   sink de notification.

3. **Le drapeau `--auto`, désactivé par défaut**, et refusant de démarrer sans
   trois garde-fous simultanés :
   - un **budget d'actions** : au plus N blocages par jour, compteur persisté
     (sinon un redémarrage remet le compteur à zéro — et la boucle infinie de
     réservations est exactement le scénario qui fait bannir un compte) ;
   - un **kill switch** : un fichier ou une clé Secrets Manager relue à chaque
     tentative ; absente ou à `off`, aucune action. Plus un endpoint
     `POST /admin/killswitch` local, pour couper sans redéployer ;
   - une **fenêtre horaire et un rayon plus stricts** que la surveillance :
     bloquer une voiture à 3 h du matin à 4 km n'a aucun intérêt.

4. **Idempotence et libération garantie** : un blocage sans `Release` associé
   est une voiture immobilisée pour rien — donc `defer` de libération, timeout
   court, et une alerte Prometheus sur `holds_active > 0` depuis trop
   longtemps.

5. **Journal d'audit applicatif** : chaque action (tentative, succès, échec,
   libération) écrite en JSON avec son déclencheur. Sans ça, impossible de
   répondre à « pourquoi cette voiture a-t-elle été bloquée à 7 h 12 ? ».

### Pourquoi ce n'est pas fait maintenant

- **CGU.** L'endpoint de disponibilité est public et non authentifié : le lire
  à 20 s d'intervalle, avec un User-Agent honnête, est un usage raisonnable.
  **Bloquer** un véhicule est une action authentifiée qui engage un contrat et
  consomme une ressource partagée ; l'automatiser sans autorisation explicite
  sort du cadre. Ça ne se corrige pas par du code propre.
- **Risque de compte.** Un bug de boucle (le genre qui arrive une fois dans
  tout projet) peut enchaîner des dizaines de réservations. La sanction n'est
  pas technique, c'est la suspension du compte — et le remboursement des frais.
- **Impact sur les autres.** Une voiture bloquée est une voiture indisponible
  pour quelqu'un d'autre. Le mode notify-first n'a pas cet effet de bord.
- **Valeur portfolio.** La partie intéressante à démontrer est la chaîne
  DevSecOps, pas l'automatisation d'une réservation. Et savoir **s'arrêter à
  la bonne frontière**, en l'écrivant, se remarque plus qu'une fonctionnalité
  de plus.

**Décision.** Risque **accepté et non mitigé** : la fonctionnalité n'est pas
implémentée. À reconsidérer uniquement si Communauto documente un usage
automatisé autorisé, ou sur autorisation écrite.

**Terminé quand.** Cette section est dans le dépôt, référencée par le README,
et le code ne contient aucun appel authentifié — ce qui est vérifiable :
```bash
grep -riE "authorization|bearer|login|password|reserve|booking" --include="*.go" . | grep -v _test.go
# attendu : aucun résultat côté API Communauto
```

---

## Annexe A — Runbook d'incident

| Symptôme | Première commande | Cause probable |
|---|---|---|
| Plus aucune notification | `curl localhost:2112/readyz` | polls périmés → voir alerte `PollsStale` |
| `readyz` 503, `healthz` 200 | `kubectl logs -l app=flexwatch \| tail -50` | API amont en panne, ou réseau sortant coupé |
| `poll_errors_total{kind="schema"}` monte | `go run ./cmd/flexwatch -once` en local | schéma de l'API changé → ajuster `decode.go` |
| `kind="http"` avec 429 | `kubectl logs ... \| grep Retry-After` | trop de requêtes : augmenter `FLEX_POLL_INTERVAL` |
| `vehicles_in_fence` toujours à 0 | `flexwatch -once` et comparer `in_bbox`/`in_fence` | géofence mal réglé (centre/rayon) |
| Notifications Telegram en échec | `kubectl get secret flexwatch-telegram -o yaml` | token révoqué → rotation (phase 6, étape 6) |
| Pod en `CrashLoopBackOff` | `kubectl logs --previous` | config invalide : le message liste **toutes** les variables fautives |
| Déploiement refusé | `kubectl get events -n flexwatch` | Kyverno : image non signée ou référencée par tag |

## Annexe B — Arrêter les frais

```bash
# Arrêt temporaire (l'instance ne coûte plus, le disque oui) :
aws ec2 stop-instances --instance-ids <id>

# Destruction complète :
cd terraform/envs/prod && terraform destroy
# puis, manuellement (créés hors Terraform) :
aws s3 rm "s3://$BUCKET" --recursive && aws s3api delete-bucket --bucket "$BUCKET"
aws secretsmanager delete-secret --secret-id flexwatch/telegram --recovery-window-in-days 7
```
Vérifier ensuite dans *Billing → Cost Explorer* qu'aucune ressource ne persiste.

## Annexe C — Checklist de fin de projet

- [ ] `make check` vert, couverture connue et assumée
- [ ] Threat model committé, chaque menace rattachée à un contrôle ou acceptée
- [ ] CI : 6 jobs verts, actions épinglées par SHA, `zizmor` propre
- [ ] Image < 15 Mo, distroless nonroot, SBOM attaché, `cosign verify` OK
- [ ] `conftest verify` + `conftest test` sur le plan : aucun `deny`
- [ ] Kyverno refuse une image non signée (démontré)
- [ ] Falco a produit un événement (démontré)
- [ ] Requête vers l'IMDS depuis le pod : bloquée (démontré)
- [ ] Token absent de git, des manifests, de l'image et des logs (4 vérifs)
- [ ] Rotation de secret effectuée de bout en bout
- [ ] Prometheus `UP`, 7 alertes chargées, 1 vue en `firing`
- [ ] Dashboard Grafana committé, heatmap sur ≥ 24 h
- [ ] Phase 8 écrite, aucun appel authentifié dans le code
- [ ] Budget AWS < 25 USD/mois, alarme testée
