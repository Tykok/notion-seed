# notion-seed

Déclare la structure d'un workspace Notion en YAML, et refuse par défaut les
changements que l'API applique sans broncher mais qu'on ne voulait pas.

## Pourquoi

Déclarer un workspace Notion en fichiers n'est pas le problème difficile. Le
problème difficile, c'est ce que l'API fait quand la déclaration change.

Trois comportements mesurés contre l'API, qu'un outil qui se contente
d'envoyer la requête ne vous signale pas :

| Changement | Ce que fait l'API | Classe |
|---|---|---|
| Renommer une option de `select` | Répond `200`, ne change rien | `migration requise` |
| Retirer une option de `select` / `multi_select` | Perd la valeur des lignes concernées | `destructif` |
| Retirer une option de `status` | **Réassigne les lignes à l'option par défaut**, sans erreur ni avertissement | `réécriture silencieuse` |

Le dernier cas est celui qui justifie l'outil. La donnée n'est pas perdue :
elle est remplacée par une valeur plausible et fausse, indistinguable après
coup. `notion-seed` bloque le plan dessus, et `allow_data_loss` ne le débloque
pas — consentir à perdre une donnée n'est pas consentir à ce qu'elle soit
remplacée par une autre.

## État actuel

MVP 0 : lecture seule. `plan` et `diff` calculent et affichent les
changements, rien n'est jamais écrit dans Notion.

| | |
|---|---|
| `init`, `version`, `plan`, `diff` | disponibles |
| `apply` | pas encore |
| fichier de state | pas encore — `plan` diffe contre un état réel vide, donc tout ressort en création |
| `lifecycle.prevent_destroy` / `allow_data_loss` | lus et validés, pas encore appliqués au plan |

## Prérequis

`notion-seed` ne gère pas l'authentification : il délègue les appels à
[`ntn`](https://www.npmjs.com/package/ntn), qui stocke le jeton dans le
keychain de l'OS.

```sh
npm i -g ntn@0.22.11
ntn login
```

La version est épinglée : `0.22.11` est la seule sur laquelle le format de
sortie de `ntn` a été mesuré, et `notion-seed` refuse de tourner en dessous.

## Installation

Binaire publié — Linux, macOS et Windows, en amd64 et arm64 — depuis la page
[Releases](https://github.com/tykok/notion-seed/releases) :

```sh
# macOS arm64, à adapter à votre plateforme
curl -fsSL https://github.com/tykok/notion-seed/releases/latest/download/notion-seed_<version>_darwin_arm64.tar.gz \
  | tar -xz notion-seed
```

Ou depuis les sources :

```sh
go install github.com/tykok/notion-seed@latest
```

Le binaire installé par `go install` annonce `0.0.0-dev` : la version n'est
injectée qu'au build de release.

## Démarrage

```sh
notion-seed init    # vérifie que ntn est présent, assez récent et authentifié
notion-seed plan    # affiche les changements
```

## Configuration

Un dossier, un fichier de workspace, un fichier YAML par database :

```
.
├── workspace.yaml
└── databases/
    ├── projects.yaml
    └── tasks.yaml
```

`workspace.yaml` porte les sections globales — `version`, `workspace`,
`lifecycle`. Un fichier de `databases/` ne déclare que des databases : sans
cette règle, un fichier quelconque pourrait détourner la cible d'écriture ou
effacer un garde-fou, et le dernier chargé gagnerait.

```yaml
# workspace.yaml
version: 1
workspace:
  parent_page_id: 33333333-3333-4333-8333-333333333333
lifecycle:
  prevent_destroy:
    - database.projects
```

```yaml
# databases/tasks.yaml
databases:
  - key: tasks
    name: Tasks
    properties:
      Name:
        type: title
      Estimate:
        type: number
        format: number
      Statut:
        type: status
        options:
          - name: À faire
            group: To-do
          - name: Fait
            group: Complete
```

Le schéma JSON complet est dans [`schema/notion-seed.schema.json`](schema/notion-seed.schema.json).

## Sortie

```
ntn 0.22.11 — workspace Example Space (33333333-3333-4333-8333-333333333333)

Plan: 2 to add, 0 to change, 0 to destroy

  + database.projects (new)
      + property "Name" (title)

  + database.tasks (new)
      + property "Estimate" (number)
      + property "Name" (title)
```

Texte brut, sans couleur : la sortie doit rester lisible dans un pipe et en
CI. Le plan part sur stdout, les attentes de retry sur stderr — stdout ne
porte que le plan, pour qu'il reste identique entre deux runs.

Un plan bloqué sort en code non nul.

## Flags de `plan` et `diff`

| Flag | Défaut | Rôle |
|---|---|---|
| `--dir` | `.` | dossier de configuration |
| `--skip-preflight` | `false` | mode entièrement hors ligne : ni vérification de `ntn`, ni vérification de la page parente. Valide la configuration et rend le plan sans aucun appel réseau |
| `--rate` | `5` | plafond d'appels API par seconde |
| `--burst` | `10` | appels tolérés en rafale |

`diff` est aujourd'hui identique à `plan`, puisque `plan` n'écrit pas encore
de state. Les deux restent distinctes pour que l'usage en CI soit stable le
jour où `plan` y touchera.

## Développement

```sh
go test ./...
gofmt -l .
go vet ./...
./scripts/check-spdx.sh
```

Les quatre tournent en CI sur chaque push et chaque pull request.

## Licence

GPL-3.0-or-later. Voir [`LICENSE`](LICENSE).
