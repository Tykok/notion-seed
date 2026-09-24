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

MVP 2 : première écriture. `apply` crée les databases déclarées et absentes de
Notion, et inscrit leur identité dans le state après chaque création. Les
modifications de propriétés et les destructions ne sont pas encore écrites :
elles sont affichées, nommées, et `apply` sort en code non nul tant qu'elles
restent.

| | |
|---|---|
| `init`, `version`, `plan`, `diff`, `import` | disponibles |
| `apply` | créations uniquement — voir [Appliquer](#appliquer) |
| fichier de state | `notion-seed.state.json`, écrit par `import` et `apply` |
| `lifecycle.prevent_destroy` / `allow_data_loss` | appliqués au plan |

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

### macOS, Linux (Homebrew)

```sh
brew install tykok/tap/notion-seed
```

### Debian, Ubuntu

apt n'a pas de mécanisme de tap par utilisateur, mais un dépôt apt n'est qu'un
arbre de fichiers statiques : celui-ci est hébergé sur GitHub Pages et signé.

```sh
sudo install -d /etc/apt/keyrings
curl -fsSL https://tykok.github.io/notion-seed/apt/gpg.key \
  | sudo tee /etc/apt/keyrings/notion-seed.asc > /dev/null
echo "deb [signed-by=/etc/apt/keyrings/notion-seed.asc] https://tykok.github.io/notion-seed/apt stable main" \
  | sudo tee /etc/apt/sources.list.d/notion-seed.list > /dev/null
sudo apt update && sudo apt install notion-seed
```

`signed-by` restreint la clé à ce seul dépôt : sans lui, la clé vaudrait pour
toutes les sources apt de la machine.

Un `.deb`, `.rpm` ou `.apk` est aussi attaché à chaque release, si vous préférez
ne rien ajouter aux sources :

```sh
sudo apt install ./notion-seed_<version>_linux_amd64.deb
```

### Depuis les sources

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
          - key: todo
            name: À faire
            group: To-do
          - key: done
            name: Fait
            group: Complete
```

Une propriété `status` doit déclarer ses `options`, et `group` est
**obligatoire** sur chacune. Un `status` créé sans options se fait peupler par
l'API de ses propres options par défaut, que le plan n'aura pas affichées — et
leur retrait ultérieur réassigne silencieusement les lignes. `select` et
`multi_select` n'ont pas cette contrainte : leurs options peuvent être gérées à
la main sans ce risque.

`group` n'accepte que
`To-do`, `In progress` ou `Complete`. notion-seed ne choisit pas de groupe à
votre place : une option envoyée sans `group` est rangée par l'API dans le
premier groupe, sans erreur — donc une écriture que le plan n'aurait pas
annoncée. Sur les autres types, `group` est refusé, comme `format` hors
`number`.

La `key` est ce qui ancre l'identité d'une option à travers un renommage : sans
elle, une option renommée dans le YAML ressort en retrait suivi d'un ajout —
`destructif` ou `réécriture silencieuse` selon le type — faute de pouvoir la
suivre à travers le changement de nom.

Le schéma JSON complet est dans [`schema/notion-seed.schema.json`](schema/notion-seed.schema.json).

## State

`notion-seed.state.json`, à côté de `workspace.yaml`, retient l'identité Notion
de chaque ressource gérée et son dernier état appliqué. **Versionnez-le** : il
ne contient aucun secret, et c'est lui qui rend le plan reproductible entre
machines et en CI.

Sans lui, `notion-seed` n'a aucune ancre d'identité : toute database déclarée
ressort en création, même si elle existe déjà dans Notion.

C'est ce fichier qui permet de distinguer « le YAML a changé » de « quelqu'un a
changé Notion à la main ». Le second cas s'affiche sous la section `Dérive
détectée hors de notion-seed`, avant le plan qui ramène le réel vers le YAML.

Seules les commandes `import` et `apply` l'écrivent. `plan` et `diff` lisent le
réel mais n'y touchent jamais : un `plan` en CI ne peut donc pas produire un
diff git surprise, et une dérive ne s'efface pas d'elle-même.

## Adopter une database existante

```sh
notion-seed import database.tasks https://www.notion.so/space/Tasks-1b2c3d4e5f60...
```

La key doit être déclarée dans `databases/`. `import` adopte la database telle
qu'elle est, sans exiger qu'elle corresponde déjà au YAML — c'est le `plan`
suivant qui affiche l'écart.

Les `key` d'options sont accrochées à cet instant, en joignant sur le nom.
Une option présente dans Notion et absente du YAML est enregistrée sans key et
comptée dans la sortie :

```
database.tasks importée — id 1b2c3d4e-5f60-4a1b-8c2d-3e4f5a6b7c8d (8 propriétés, 2 options sans key de config)
```

### Ce qui n'est pas déclaré

Une **propriété** présente dans Notion et absente du YAML n'est jamais touchée :
elle apparaît sous `Hors config — présent dans Notion, non touché`. L'API
modifie les propriétés une par une, donc ne pas la déclarer suffit à ne pas y
toucher.

Une **option** présente dans Notion et absente du YAML, elle, sera détruite dès
qu'on écrit sa propriété : l'API remplace la liste entière des options au lieu
de la fusionner. Le plan la fait donc ressortir en retrait, `destructif` pour
`select` et `multi_select`, `réécriture silencieuse` pour `status`.

Autrement dit, « non déclaré = non touché » est vrai pour les propriétés et faux
pour les options. C'est exactement le genre d'écart que cet outil existe pour
rendre visible.

## Appliquer

```sh
notion-seed apply
```

`apply` recalcule le plan, l'affiche, demande confirmation, puis écrit. Il ne
prend aucun argument : il n'y a pas de fichier de plan à rejouer, donc pas de
plan périmé à appliquer par mégarde.

### Ce qu'il écrit

Les **créations**. Une database déclarée dans le YAML et absente du state est
créée dans la page parente, relue, puis inscrite dans le state. Le state est
sauvegardé après *chaque* création : une interruption laisse un fichier
exactement vrai, jamais une database créée sans ancre — donc jamais un doublon
au run suivant.

La relecture n'est pas du zèle. Elle rapporte les ids d'options, sans lesquels
le state est aveugle à la dérive, et elle confronte le réel à ce qui avait été
annoncé. Si l'API n'a pas écrit ce que le plan promettait, `apply` le dit — sur
sa propre écriture.

Il retire aussi les **entrées de state obsolètes** : une ressource que le YAML
ne déclare plus et qui a déjà été supprimée dans Notion. C'est un nettoyage
local, rien n'est écrit dans Notion.

### Ce qu'il n'écrit pas encore

Les modifications de propriétés et les destructions. Elles ressortent sous
`Non appliqué par cette version`, et `apply` sort en code non nul tant qu'il
reste du travail — un apply qui ne converge pas doit être bruyant en CI.

Une ressource est entièrement écrite ou pas touchée du tout. Il n'y a pas
d'exécution partielle silencieuse.

### La confirmation

Le mot `apply`, tapé en entier. Elle ne lève aucun garde-fou : un plan bloqué
par `lifecycle` ou par une réécriture silencieuse n'atteint jamais le prompt. Le
consentement se déclare dans `workspace.yaml`, où il se relit en revue — pas
devant un terminal, où il ne survit ni à un pipeline ni à une touche entrée.

Hors terminal, `apply` exige `--auto-approve` plutôt que de s'exécuter parce que
personne ne répondait.

| Flag | Défaut | Rôle |
|---|---|---|
| `--auto-approve` | `false` | applique sans demander confirmation (mode CI) |

`apply` partage `--dir`, `--rate` et `--burst` avec `plan`, et refuse
`--skip-preflight` : écrire hors ligne n'a pas de sens.

### En cas d'échec

Aucun rollback : archiver ce qu'on vient de créer serait une destruction que
personne n'a demandée. Ce qui a été créé reste créé, et reste dans le state.

Si l'issue d'une création est **inconnue** — un timeout ne dit pas si le serveur
a appliqué la mutation — `apply` s'arrête net sans enchaîner, et nomme la
database, la page parente et la marche à suivre : vérifier dans Notion, puis
`notion-seed import` si elle existe.

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
| `--skip-preflight` | `false` | mode entièrement hors ligne : ni vérification de `ntn`, ni vérification de la page parente. Valide la configuration et rend le plan sans aucun appel réseau — les ressources déjà importées ne sont pas comparées au réel dans ce mode, et ressortent sous `Non comparé` plutôt que sous `Aucun changement` |
| `--rate` | `5` | plafond d'appels API par seconde |
| `--burst` | `10` | appels tolérés en rafale |

`import` prend `--dir`, `--rate` et `--burst`, mais pas `--skip-preflight` :
la commande lit l'état réel, elle n'a aucun sens hors ligne.

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

Les mises à jour de dépendances arrivent par Dependabot, groupées une fois par
semaine. Elles sont fusionnées automatiquement dès que la CI passe, sans
relecture : ce sont les quatre commandes ci-dessus qui font office de revue. Une
CI rouge laisse la PR ouverte.

## Licence

GPL-3.0-or-later. Voir [`LICENSE`](LICENSE).
