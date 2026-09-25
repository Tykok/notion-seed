# YAML

## Arborescence

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
effacer une déclaration, et le dernier chargé gagnerait.

## workspace.yaml

```yaml
# workspace.yaml
version: 1
workspace:
  parent_page_id: 33333333-3333-4333-8333-333333333333
lifecycle:
  prevent_destroy:
    - database.projects
```

| Champ | Obligatoire | Rôle |
|---|---|---|
| `version` | oui | version de format de la configuration — `1` |
| `workspace.parent_page_id` | oui | la page Notion sous laquelle les databases sont créées |
| `lifecycle.prevent_destroy` | non | liste de `database.<key>` : accusé de lecture, affiché dans le plan — ne bloque rien |
| `lifecycle.allow_data_loss` | non | liste de `database.<key>` : accusé de lecture, affiché dans le plan — ne bloque rien |

## databases/*.yaml

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

La `key` est stable — elle ancre la database à travers un renommage et c'est
elle que le state et `import` utilisent pour l'identifier. Le `name` peut
changer librement, l'`icon` est l'emoji optionnel affiché dans Notion, et
`properties` déclare chaque propriété par son nom.

## Types de propriétés

Dix types sont gérés aujourd'hui :

| Type | Champs en plus |
|---|---|
| `title` | — (exactement une par database) |
| `rich_text` | — |
| `number` | `format` |
| `url` | — |
| `select` | `options` |
| `multi_select` | `options` |
| `status` | `options`, chacune avec un `group` obligatoire |
| `date` | — |
| `checkbox` | — |
| `people` | — |

Une propriété d'un autre type présente dans Notion est laissée intacte — voir
[Ce qui n'est pas déclaré](#ce-qui-n-est-pas-declare).

## Options

::: warning Retirer une option de status réécrit des lignes
L'API réassigne les lignes à une autre option, sans erreur. Le plan les compte
avant que vous appliquiez.
:::

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
`destructive` ou `silent rewrite` selon le type et le nombre de lignes
concernées — faute de pouvoir la suivre à travers le changement de nom.

## Icône

L'`icon` — un emoji — est écrite sur la database, jamais sur son data source :
mesuré, écrire sur la database met les deux à jour, écrire sur le data source les
fait diverger. `notion-seed` ne lit que l'icône de la database : si celle du data
source est changée à part, dans Notion, il ne la voit pas.

## lifecycle — des accusés de lecture

::: danger prevent_destroy n'empêche rien
Malgré son nom, une database listée dans `prevent_destroy` part à la corbeille
comme les autres quand elle quitte le YAML. La clé ajoute seulement une mention
au plan.
:::

`prevent_destroy` et `allow_data_loss` ne bloquent **plus rien**. Malgré son
nom, `prevent_destroy` n'empêche pas la destruction : ces deux clés ne sont que
des accusés de lecture, affichés sous la ressource qu'elles nomment. Une
database sortie du YAML, déclarée dans `prevent_destroy` :

```
ntn 0.22.11 — workspace Example Space (33333333-3333-4333-8333-333333333333)

Plan: 0 to add, 0 to change, 1 to destroy

  - database.tasks  [destructive]
      - database.tasks — present in the state, absent from the configuration  [destructive]
          → 3 row(s) go to the trash with it.
      → declared in lifecycle.prevent_destroy.

Impact: 1 database(s) in the trash with 3 row(s).
```

Elles disent « je sais ce que cette ressource porte », et rien de plus. `apply`
met donc à la corbeille une database déclarée dans `prevent_destroy` exactement
comme une autre, en affichant la mention. C'est écrit noir sur blanc parce
qu'une clé nommée `prevent_destroy` qu'on croirait bloquante serait un piège :
vous compteriez sur elle, et elle ne vous retiendrait pas.

Ce qui arrête une commande, désormais, c'est ce que vous demandez dans votre
workflow : [`--fail-on`](/fr/commands#en-ci). Ce qui informe, c'est la mesure.
Ce qui décide, c'est vous.

## Ce qui n'est pas déclaré

::: warning Les options non déclarées sont détruites
Une propriété non déclarée n'est jamais touchée. Une option non déclarée est
supprimée dès que sa propriété est écrite : l'API remplace toute la liste.
:::

Une **propriété** présente dans Notion et absente du YAML n'est jamais touchée :
elle apparaît sous `Unmanaged — present in Notion, left untouched`. L'API
modifie les propriétés une par une, donc ne pas la déclarer suffit à ne pas y
toucher.

Une **option** présente dans Notion et absente du YAML, elle, sera détruite dès
qu'on écrit sa propriété : l'API remplace la liste entière des options au lieu
de la fusionner. Le plan la fait donc ressortir en retrait, avec le nombre de
lignes qui la portent : `destructive` pour `select` et `multi_select`,
`silent rewrite` pour `status` — et `safe` si ce nombre est nul.

Autrement dit, « non déclaré = non touché » est vrai pour les propriétés et
faux pour les options. C'est exactement le genre d'écart que cet outil existe
pour rendre visible.

## Schéma JSON

Le schéma complet est
[`schema/notion-seed.schema.json`](https://github.com/tykok/notion-seed/blob/main/schema/notion-seed.schema.json).
