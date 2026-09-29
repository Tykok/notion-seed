# Commandes

La plupart des commandes lisent le dossier de configuration (`--dir`, `.` par
défaut) et parlent à Notion via `ntn`. `init` ne prend pas de `--dir` — elle ne
fait que vérifier `ntn` — et `version` ne fait ni l'un ni l'autre : elle
affiche seulement son numéro. Seules `import` et `apply` écrivent — dans
Notion et dans le state.

| Commande | Écrit dans Notion | Écrit le state |
|---|---|---|
| `init` | non | non |
| `plan` | non | non |
| `diff` | non | non |
| `apply` | oui | oui |
| `import` | non | oui |
| `version` | non | non |

## init

```sh
notion-seed init    # vérifie que ntn est présent, assez récent et authentifié
```

Elle n'écrit rien : elle vérifie seulement que `ntn` est présent, assez récent
et authentifié, avant que toute autre commande ne tente de joindre Notion.

## plan

`plan` calcule et affiche les changements, sans rien appliquer.

### Sortie

```
ntn 0.22.11 — workspace Example Space (33333333-3333-4333-8333-333333333333)

Plan: 2 to add, 0 to change, 0 to destroy

  + database.projects (new)
      + name "Projects"
      + property "Name" (title)

  + database.tasks (new)
      + name "Tasks"
      + property "Estimate" (number)
      + property "Name" (title)
```

Texte brut, sans couleur : la sortie doit rester lisible dans un pipe et en
CI. Le plan part sur stdout, les attentes de retry sur stderr — stdout ne
porte que le plan, pour qu'il reste identique entre deux runs.

Un plan bloqué sort en code non nul — une ressource gérée que Notion ne connaît
plus, par exemple. Le coût mesuré, lui, ne fait sortir en erreur que si vous
l'avez demandé avec [`--fail-on`](#en-ci).

### Flags

| Flag | Défaut | Rôle |
|---|---|---|
| `--dir` | `.` | dossier de configuration |
| `--skip-preflight` | `false` | mode entièrement hors ligne : ni vérification de `ntn`, ni vérification de la page parente. Valide la configuration et rend le plan sans aucun appel réseau — les ressources déjà importées ne sont pas comparées au réel dans ce mode, et ressortent sous `Not compared` plutôt que sous `No changes` |
| `--rate` | `5` | plafond d'appels API par seconde |
| `--burst` | `10` | appels tolérés en rafale |
| `--fail-on` | vide | classes de changement qui font sortir en code non nul — voir [En CI](#en-ci). Vide : rien ne fait échouer |
| `--out` | vide | écrit aussi le plan dans ce fichier, pour `notion-seed apply <fichier>` — voir [Un plan relu](#un-plan-relu). Ne change ni la sortie ni le code de retour ; refuse `--skip-preflight` |

`import` prend `--dir`, `--rate` et `--burst`, mais refuse `--skip-preflight` —
la commande lit l'état réel, elle n'a aucun sens hors ligne — et `--fail-on`,
puisqu'elle ne calcule aucun plan.

### Ce qui peut être compté, et ce qui ne peut pas

Un **retrait d'option** est toujours chiffré : les options n'existent que sur
`select`, `status` et `multi_select`, et chacun a son filtre.

Une **destruction** l'est aussi, sans filtre : le compte porte sur toutes les
lignes du data source de la database — celui que le plan vient de relire —,
celles qui partent à la corbeille avec elle. Une database qui porte plusieurs
data sources les emporte tous, mais un seul est compté : la ligne et l'agrégat
disent alors « at least N row(s) », et combien de data sources n'ont pas été
comptés. Elle reste `destructive` quel que soit ce compte, 0 compris.

Un **changement de type** est chiffré avec le filtre que la campagne du
2026-09-25 a vérifié contre les lignes que chaque couple touche réellement, sur
la colonne de départ, avant l'écriture. Tous les filtres ne sont pas exacts, et
le chiffre dit quelle borne il est :

| Chiffre | Filtre | Couples |
|---|---|---|
| `N rows` | toutes les lignes | tout type → `status`, quand aucune option ne peut garder une valeur |
| `N rows` | `is_not_empty` | tout couple où rien ne survit (`date` → `number`, `people` → `select`, `status` → `checkbox`…) |
| `N rows` | lignes cochées | `checkbox` → `number`, `date`, `people`, et → `select` / `multi_select` sans option `Yes` |
| `N rows` | lignes vides | `select` → `status` : les lignes vides reçoivent une option |
| `N rows` | non vides (toutes les lignes vers `status`), sauf les options déclarées écrites comme des nombres | `number` → `select`, `multi_select`, `status` avec options déclarées : seule une option nommée par l'écriture décimale exacte du nombre, sans exposant, le garde (`7` garde 7, `7.0` ne garde rien). À partir d'un nombre déclaré de magnitude 1e21, jamais mesuré, le chiffre devient `at least N` |
| `at least N rows` | `is_not_empty` sur `rich_text` | `rich_text` → `select`, `multi_select`, `checkbox`, `people` : un texte fait seulement d'espaces ou de sauts de ligne n'est pas compté, et il est perdu aussi |
| `at least N rows` | non vides, sauf les options déclarées | `rich_text` / `url` → `select`, `multi_select`, `status` avec options déclarées : le filtre ignore la casse et les espaces de fin (sur `url`, pas une barre oblique finale), la conversion non — une telle valeur n'est pas comptée, et ne survit pas non plus |
| `up to N rows` | `is_not_empty` | `url`, `select`, `multi_select` → `number` ou `date`, `multi_select` → `select` : certaines valeurs survivent à la conversion |
| `up to N rows` | toutes les lignes | `multi_select` → `status` : une ligne qui ne porte qu'une valeur déclarée la garde |
| inconnu | aucun | `rich_text` → `number` / `date`, `status` → `rich_text`, `url`, `select`, `multi_select` |

Un minorant nul ne rend jamais un changement `safe` : il se lit « no rows
counted, which does not mean none is touched ». Un zéro exact ou majorant, si
— une colonne vide n'a rien à perdre.

Une `checkbox` ne compte que ses lignes cochées : une case décochée passe à
vide aussi, mais une checkbox n'a pas d'état vide, donc « décochée » ne porte
rien que « jamais renseignée » ne porte pas. La ligne le dit.

Là où aucun filtre n'est sain, la ligne ne porte pas de chiffre et dit
pourquoi :

```
      ~ property "Notes" — rich_text → number: the leading number is kept ('42 text' → 42, '2026-01-15' → 2026, a false value); everything else is emptied  [silent rewrite]
          → actual impact unknown: no filter separates the text that survives the conversion from the rest.
```

```
      ~ property "Statut" — status → select: a value survives only where an option with the same name is declared; a row that never received a status is emptied  [destructive]
          → actual impact unknown: a row that never received a status reads as the default option, is emptied too, and no filter isolates it.
      + option "Not started" (property "Statut")
      + option "Done" (property "Statut")
      - option "In progress" (property "Statut") — not redeclared under this name: the type change re-creates the options  [destructive]
          → 2 rows will be emptied.

Impact: at least 2 values lost.
```

Le total dit « at least » : les lignes jamais renseignées sont perdues aussi,
et personne n'a pu les compter. Depuis `multi_select`, les lignes qui portent
une option non redéclarée sont comptées une fois, sur leur ligne de retrait, et
laissées hors de la ligne de propriété.

La classe reste celle de la mesure — vous savez que le changement est
dangereux, vous ne savez pas toujours sur combien de lignes.

### Changements de type

Les 90 couples ordonnés de types gérés ont été mesurés contre l'API, 7 le
2026-09-24 et les 83 autres le 2026-09-25. La classe est le coût du corps que
notion-seed envoie : les options que le YAML déclare, par nom, sans id. La ligne
du plan dit, pour chaque couple, ce qui survit.

| de \ vers | title | rich_text | number | url | select | status | multi_select | date | checkbox | people |
|---|---|---|---|---|---|---|---|---|---|---|
| **title** | | M | M | M | M | M | M | M | M | M |
| **rich_text** | M | | D | S | D¹ | R | D¹ | D | D | D |
| **number** | M | S | | S | D | R | D | D | D | D |
| **url** | M | S | D | | D | R | D | D | D | D |
| **select** | M | S | D | S | | R² | S² | D | D | D |
| **status** | M | D | D | D | D² | | D² | D | D | D |
| **multi_select** | M | S | D | S | R² | R² | | D | D | D |
| **date** | M | S | D | D | D | R | D | | D | D |
| **checkbox** | M | S | D | S | D³ | R³ | D³ | D | | D |
| **people** | M | S | D | D | D | R | D | D | D | |

S `safe`, D `destructive`, R `silent rewrite`, M `migration required`.

- **L'API ne crée jamais d'option.** Vers `select`, `multi_select` ou `status`,
  une valeur ne survit que si une option portant exactement son texte est
  déclarée dans la même écriture ; depuis `rich_text`, le texte est coupé à la
  première virgule (`multi_select` : découpé sur les virgules). ¹ Avec options
  déclarées, ces couples deviennent `silent rewrite`.
- **Vers `number`**, un texte garde son nombre de tête (`'2026-01-15'` → 2026)
  et tout le reste passe à vide : mesuré `destructive`, et la ligne nomme les
  valeurs réécrites.
- ² Entre types à options, chaque option actuelle que le YAML ne redéclare pas
  sous le même nom ressort en ligne `-` avec son compte. Vers `status`, ses
  lignes ne passent pas à vide : elles sont réécrites vers l'une des options
  déclarées.
- ³ `checkbox` devient `Yes` / `No` : → `select` / `multi_select` est `safe`
  avec une option `Yes`, → `status` avec `Yes` et `No`.
- **Vers `status`, chaque ligne reçoit une valeur**, vides comprises : l'une
  des options déclarées — un `status` les déclare toujours.
- **`status` en départ :** une ligne qui n'a jamais reçu de status se lit comme
  l'option par défaut, et pourtant toute conversion la vide.
- **`status` → `select` était documenté sans perte jusqu'au 2026-09-25.** C'était
  faux : re-mesuré, il vide toutes les lignes sans option redéclarée, et vide
  les lignes jamais renseignées même avec.
- **`title` :** l'API refuse les deux sens en `400`. notion-seed retient la
  database — voir [Ce que l'API ne sait pas faire](/fr/#ce-que-l-api-ne-sait-pas-faire).

Vous êtes garant de votre base. notion-seed est garant de ce que vous savez en
appuyant sur entrée. En CI, [`--fail-on`](#en-ci) rend la décision au workflow.

## diff

`diff` est l'équivalent en lecture seule de `plan`, sans toucher au state.

`diff` est aujourd'hui identique à `plan`, puisque `plan` n'écrit pas encore
de state. Les deux restent distinctes pour que l'usage en CI soit stable le
jour où `plan` y touchera.

## apply

```sh
notion-seed apply
notion-seed apply plan.out   # un plan relu avec plan --out
```

`apply` recalcule le plan, l'affiche, demande confirmation, puis écrit. Il ne
rejoue jamais un plan : même avec un fichier écrit par `plan --out`, il
recalcule, et confronte le nouveau plan au plan relu — voir [Un plan
relu](#un-plan-relu).

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

Les **modifications**. Une database déjà ancrée par le state est écrite en deux
appels, toujours dans cet ordre :

1. `PATCH /v1/databases/{id}` — le nom, la description et l'icône, s'ils
   changent ;
2. `PATCH /v1/data_sources/{id}` — les propriétés qui portent une ligne dans le
   plan, et elles seules.

Ce qui part est exactement ce que le plan a affiché : une propriété déclarée
mais identique au réel ne part pas, une propriété non déclarée non plus. Les
options existantes sont transmises avec leur id, les neuves sans : l'API leur en
crée un, que la relecture rapporte au state. Sous un changement de type, les
options sont recréées : seules celles du YAML partent, sans id, et chaque option
actuelle que le YAML ne redéclare pas sous le même nom ressort en `-`, avec le
nombre de lignes qu'elle vide — ou, vers `status`, qu'elle réécrit vers l'une
des options déclarées. Mesuré le 2026-09-25 pour tous les couples entre
`select`, `multi_select` et `status`.

L'ordre est choisi pour l'échec. Si le second appel échoue, le nom et l'icône
sont à jour et **aucune donnée de ligne n'a été touchée** — l'échec le moins
coûteux. `apply` le dit, et inscrit dans le state ce qui est passé.

Les **destructions**. Une database que le state ancre, que le YAML ne déclare
plus et que Notion porte encore est mise à la corbeille par un seul appel,
`PATCH /v1/databases/{id}` avec `{"in_trash":true}`, puis son entrée est retirée
du state. L'entrée n'est retirée que si la réponse de l'API confirme la
corbeille : sinon elle est gardée, et `apply` le signale comme un écart.
`plan` et `apply` disent avant combien de lignes partent avec elle ; si le
comptage échoue, la ligne dit que ce nombre n'est pas mesuré, jamais 0.
`lifecycle.acknowledge_destroy` n'y change rien — voir
[lifecycle](/fr/yaml#lifecycle-des-accuses-de-lecture). Une database mise à la corbeille
se restaure depuis la corbeille de Notion ; pour que notion-seed la gère de
nouveau, redéclarez-la puis lancez `notion-seed import`.

Renommer la `key` d'une database dans le YAML n'est pas un renommage pour
notion-seed, qui n'a que la key pour ancrer l'identité : l'ancienne key en
ressort orpheline et part à la corbeille, la nouvelle est créée vide, et
`plan` montre les deux séparément. Pour garder la database, gardez sa `key` —
avec une `key` explicite, `name` peut changer librement ; sans elle, la key est
dérivée de `name` et un renommage la déplace (voir
[databases/*.yaml](/fr/yaml#databases-yaml)) — ou, si la key a déjà changé, ré-attachez la
database existante à la nouvelle key avec `notion-seed import` avant de
lancer `apply`.

Il retire aussi les **entrées de state obsolètes** : une ressource que le YAML
ne déclare plus et qui a déjà été supprimée dans Notion. C'est un nettoyage
local, rien n'est écrit dans Notion — à ne pas confondre avec une destruction,
qui écrit, et que la confirmation annonce sur sa propre ligne.

### Ce qu'il retient

::: warning Une limite de l'API, pas un jugement
Renommer une option, changer sa couleur, ou changer le type d'une propriété
`title` sont les seuls changements déclarés que notion-seed refuse d'écrire :
l'API ne sait pas les exprimer.
:::

Ce que l'API ne sait pas exprimer ressort sous `Withheld — migration required` : ce
n'est pas une limite de cette version, et attendre n'y changera rien — voir
[Ce que l'API ne sait pas faire](/fr/#ce-que-l-api-ne-sait-pas-faire). `apply` sort en
code non nul tant qu'il reste une ressource retenue — un apply qui ne converge
pas doit être bruyant en CI.

Une ressource retenue n'est pas touchée du tout : aucun appel ne part pour elle.
Une ressource écrite l'est en entier, à une exception près, qui n'est jamais
silencieuse : une modification peut s'arrêter entre ses deux appels — voir
[En cas d'échec](#en-cas-d-echec).

La migration se fait à la main, avec le nombre de lignes que `plan` a compté :

1. créer la nouvelle option dans Notion ;
2. y déplacer les lignes que le plan a comptées ;
3. retirer l'ancienne option, puis relancer.

```
  ~ database.tasks  [migration required]
      ~ option "Fait" → "Terminé" (property "Statut") — the API returns 200 without changing anything: create, migrate the rows, then remove  [migration required]
          → 2 rows hold "Fait": migrate them by hand before applying.

Withheld — migration required

  ~ database.tasks
      an option must be migrated by hand: the API can neither rename an option nor change its color
      → create the new option in Notion, move the rows counted above to it, remove the old one, then rerun
```

Un changement de type d'un `title` est retenu de la même façon, avec sa propre
procédure :

```
Withheld — migration required

  ~ database.tasks
      the API refuses to change the type of a title property, in either direction
      → add a new property of the wanted type, copy the values into it in Notion, then remove the type change from the YAML and rerun
```

Ce sont les seuls changements déclarés que notion-seed refuse d'écrire — et ce
n'est pas un jugement sur le coût, c'est une limite de l'API. Écrire quand même
inscrirait dans le state un état que Notion ne porte pas, et chaque run suivant
afficherait une dérive fantôme.

### La confirmation

Le mot `apply`, tapé en entier, après le plan et ce qui va se passer, par
nature : créations, modifications, mises à la corbeille, et entrées de state
obsolètes à retirer — chacune sur sa ligne, puisque la dernière n'écrit rien
dans Notion. La ligne `Impact` qu'affiche `apply` ne compte que les ressources
qu'il va écrire : une ressource retenue en est exclue, puisqu'`apply` ne causera
pas ce qu'elle coûterait. Sans ressource retenue, elle est identique à celle de
`plan`.

```
Impact: 1 database(s) in the trash with 3 row(s).

1 database(s) will be moved to the trash in Notion.
Type "apply" to confirm:
```

Elle ne lève rien de ce qui arrête la commande : un plan bloqué — une ressource
gérée que Notion ne connaît plus — ou une classe refusée par `--fail-on`
n'atteint jamais le prompt.

Hors terminal, `apply` exige `--auto-approve` plutôt que de s'exécuter parce que
personne ne répondait.

Dans un terminal, une fin d'entrée au prompt — `Ctrl-D` — refuse elle aussi,
avec son propre message : `confirmation interrupted (end of input): nothing
was applied`. Une entrée branchée sur `/dev/null` n'est pas un terminal, et
réclame `--auto-approve`.

| Flag | Défaut | Rôle |
|---|---|---|
| `--auto-approve` | `false` | applique sans demander confirmation (mode CI) |

`apply` partage `--dir`, `--rate`, `--burst` et `--fail-on` avec `plan`, et
refuse `--skip-preflight` : écrire hors ligne n'a pas de sens. `--fail-on` y
est vérifié **avant** la confirmation et avant la moindre écriture.

### En cas d'échec

Aucun rollback : archiver ce qu'on vient de créer serait une destruction que
personne n'a demandée. Ce qui a été créé, modifié ou mis à la corbeille reste
écrit, et le state le reflète.

Une modification peut s'arrêter **entre ses deux appels** : le nom, la
description ou l'icône sont passés, les propriétés non, et aucune donnée de ligne
n'a été touchée. `apply` nomme ce qui est passé, l'inscrit dans le state, et
relancer `notion-seed plan` montre ce qui reste.

Une database dont une page ancêtre est à la corbeille se lit comme vivante, mais
refuse toute écriture. `apply` le diagnostique et demande de restaurer la page
parente, plutôt que de relayer le `404` de l'API, qui accuse à tort le partage
avec l'intégration.

Quand c'est la page de `workspace.parent_page_id` elle-même qui est à la
corbeille, `plan` et `apply` s'arrêtent dès la vérification de départ, avant
tout plan et donc avant toute écriture : restaurez-la, ou faites pointer
`parent_page_id` vers une page vivante. C'est vrai aussi quand c'est une page
au-dessus d'elle qui est à la corbeille : Notion le signale sur la page
parente. `--skip-preflight` saute cette vérification comme les autres.

Une mise à la corbeille qui échoue — refus de l'API, `404`, issue inconnue —
laisse l'entrée de state **en place** : `apply` s'arrête et renvoie à
`notion-seed plan`, qui relit le réel. Une database déjà partie y ressort en
entrée de state obsolète, qu'un `apply` suivant retire sans rien écrire ; une
database encore là y ressort en destruction. Abandonner l'identité sur la foi
d'un échec rendrait invisible une database peut-être encore vivante.

Sous une page ancêtre déjà à la corbeille, Notion refuse aussi la mise à la
corbeille, et la database y part de toute façon avec sa page. `apply` propose
les deux issues : restaurer la page parente puis relancer `apply`, ou supprimer
définitivement la page parente depuis la corbeille de Notion, puis relancer
`notion-seed plan` : si Notion ne connaît plus la database, son entrée y
ressort en entrée de state obsolète, qu'un `apply` suivant retire sans rien
écrire.

Si l'issue d'une création est **inconnue** — un timeout ne dit pas si le serveur
a appliqué la mutation — `apply` s'arrête net sans enchaîner, et nomme la
database, la page parente et la marche à suivre : vérifier dans Notion, puis
`notion-seed import` si elle existe. Pour une modification, l'identité est déjà
dans le state : `notion-seed plan` suffit à voir ce que Notion porte.

### Un plan relu

`notion-seed plan --out plan.out` écrit le plan dans un fichier en plus de
l'afficher. `notion-seed apply plan.out` applique alors **le plan relu, ou
n'écrit rien et dit pourquoi**.

`apply` ne rejoue pas le fichier. Il recalcule le plan exactement comme sans
fichier — mêmes vérifications, même relecture, mêmes comptages —, puis, avant
de l'afficher, avant la confirmation et avant toute écriture, le confronte au
plan relu :

- la même version de notion-seed : une autre version peut classer ou compter
  autrement — un build non publié s'annonce toujours `0.0.0-dev`, donc cette
  vérification ne distingue que les versions publiées entre elles, pas un build
  de développement d'un autre ;
- le même workspace, la même configuration et le même state. La configuration
  est comparée telle que notion-seed la comprend : un commentaire ou une
  réindentation ne rendent pas un plan périmé, une `key` d'option ou une entrée
  de `lifecycle`, si ;
- les mêmes ressources, de même nature, retenues pour la même raison, avec les
  mêmes lignes — opération, cible, classe — et les mêmes entrées de state
  obsolètes ;
- sur chaque ligne comptée, pas plus de lignes que relu, et un chiffre qui
  n'est pas plus vague.

| Relu \ maintenant | exact M | jusqu'à M | au moins M, plus de M | non mesuré |
|---|---|---|---|---|
| exact n | M ≤ n | refus | refus | refus |
| jusqu'à n | M ≤ n | M ≤ n | refus | refus |
| au moins n, plus de n | M ≤ n | M ≤ n | M ≤ n | refus |
| non mesuré | accepté | accepté | accepté | accepté |

Un impact moindre reste couvert par ce qui a été accepté : sur une base
vivante, moins de lignes que relu est le cas courant, et un plan strictement
identique survivrait rarement aux heures qui séparent une relecture d'un merge.
Une ligne relue sans chiffre a été acceptée sans chiffre — quelle que soit la
classe qu'elle prendra —, donc tout chiffre et toute classe recalculés restent
dans ce qui a été accepté. Un comptage qui tombe à zéro rend sa ligne `safe`,
et ce changement de classe-là ne provoque pas de refus.

Quand le plan passe, `apply` affiche le plan **recalculé** — ses comptes sont
ceux qui vont partir — et continue comme d'habitude : `--fail-on`, annonce,
confirmation ou `--auto-approve`, écriture. Sinon, il nomme chaque
écart sur sa ligne, et n'écrit rien :

```
error: the plan recomputed now is not the one reviewed in plan.out, nothing was applied
  state changed since the plan: another apply went through in between
  database.tasks: option "High" (property "Prio") — 12 rows reviewed, 15 rows now
  → rerun `notion-seed plan --out` and have the new plan reviewed
```

Les autres refus disent `configuration changed since the plan` ou `workspace
changed since the plan`, commencent par `change added`, `change gone`, `line
added` ou `line gone`, ou nomment la classe qui a changé. Un fichier de plan
écrit par une autre version de notion-seed, ou d'un format inconnu, est refusé
avant toute comparaison. Quand le seul écart est un comptage qui a échoué
maintenant — un `403`, un `429` à bout de patience —, la dernière ligne dit de
simplement relancer la même commande pour appliquer `plan.out` : le plan relu
tient peut-être encore.

Un plan relu qui était bloqué n'est jamais appliqué. Un plan vide s'écrit
aussi : l'appliquer n'écrit rien et sort en `0`. Une ressource retenue reste
retenue, et `apply` sort en code non nul comme sans fichier.

`plan --out` et `diff --out` ne changent ni la sortie ni le code de retour. Le
fichier est écrit après l'affichage du plan et après `--fail-on` : une CI qui
échoue sur `--fail-on` a tout de même son plan à relire. L'écriture est
atomique, comme celle du state. `--out` refuse `--skip-preflight` : un plan
hors ligne n'a rien mesuré à quoi `apply` puisse être tenu.

Le fichier est du JSON, format `1`. Il porte ce que le plan affiche et rien de
plus — ni payload, ni jeton, ni contenu de ligne —, il peut donc vivre dans un
artefact de CI. Son champ `rendered` est le plan tel qu'affiché, prêt à poster
en commentaire de pull request sans relancer notion-seed.

```json
{
  "format": 1,
  "notion_seed": "0.9.0",
  "workspace_id": "33333333-3333-4333-8333-333333333333",
  "created_at": "2026-09-25T14:00:00Z",
  "config_sha256": "…",
  "state_sha256": "…",
  "changes": [
    {
      "resource": "database.tasks",
      "kind": "update",
      "withheld": "",
      "details": [
        {
          "op": "-",
          "target": "option \"High\" (property \"Prio\")",
          "class": "destructive",
          "count": 12,
          "bound": "exact"
        }
      ]
    }
  ],
  "stale_state": [],
  "rendered": "Plan: 0 to add, 1 to change, 0 to destroy\n…"
}
```

`bound` vaut `exact`, `at_most`, `at_least`, `more_than` ou `unmeasured`. Une
ligne qui ne coûte rien ne porte ni `count` ni `bound` ; une ligne
`unmeasured` ne porte pas de `count`. Un plan bloqué porte en plus
`"blocked": true`.

## import

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
database.tasks imported — id 1b2c3d4e-5f60-4a1b-8c2d-3e4f5a6b7c8d (8 properties, 2 options without a config key)
```

Voir [ce qui n'est pas déclaré](/fr/yaml#ce-qui-n-est-pas-declare).

## version

Affiche la version de notion-seed.

Le binaire installé par `go install` annonce `0.0.0-dev` : la version n'est
injectée qu'au build de release.

## State

`notion-seed.state.json`, à côté de `workspace.yaml`, retient l'identité Notion
de chaque ressource gérée et son dernier état appliqué. **Versionnez-le** : il
ne contient aucun secret, et c'est lui qui rend le plan reproductible entre
machines et en CI.

Sans lui, `notion-seed` n'a aucune ancre d'identité : toute database déclarée
ressort en création, même si elle existe déjà dans Notion.

C'est ce fichier qui permet de distinguer « le YAML a changé » de « quelqu'un a
changé Notion à la main ». Le second cas s'affiche sous la section `Drift
detected outside notion-seed`, avant le plan qui ramène le réel vers le YAML.

Seules les commandes `import` et `apply` l'écrivent. `plan` et `diff` lisent le
réel mais n'y touchent jamais : un `plan` en CI ne peut donc pas produire un
diff git surprise, et une dérive ne s'efface pas d'elle-même.

## En CI

Par défaut, un plan qui coûte cher s'affiche et sort en `0` : notion-seed
mesure, il ne décide pas. `--fail-on` énumère les classes sur lesquelles
*votre* workflow, lui, veut s'arrêter.

```sh
notion-seed diff --fail-on=silent-rewrite,destructive
```

| Valeur | Ce qu'elle attrape |
|---|---|
| `destructive` | une donnée est perdue : des valeurs passent à vide. Vers `number`, certains textes ne gardent en plus que leur nombre de tête — la ligne le dit |
| `silent-rewrite` | une donnée est remplacée par une autre, sans trace |
| `unknown` | l'impact n'a pas pu être mesuré : comptage en échec, ou hors ligne |
| `migration` | l'API accepte la requête et ne change rien : il faut migrer les lignes à la main |

Cinq choses à savoir :

- La liste est **énumérée, pas un seuil**. `safe`, `destructive` et `silent
  rewrite` forment bien une échelle, mais `unknown impact` n'y a pas de
  place : un changement non mesuré peut se révéler anodin comme catastrophique.
  Demander `destructive` ne demande donc pas « tout ce qui est au moins aussi
  grave » — nommez chaque classe que vous voulez attraper.
- Une valeur inconnue — une faute de frappe dans un nom de classe — est refusée
  **avant le moindre appel réseau**, et le message liste les valeurs acceptées.
  Sinon une CI mal configurée passerait au vert en croyant se protéger, ce qui
  est le pire mode d'échec possible pour ce flag.
- Le plan est **rendu quand même** avant la sortie en erreur : le code de retour
  dit qu'il faut regarder, la sortie dit quoi.
- Le déclenchement se fait sur les lignes de détail, avec leur classe mesurée.
  Une option que personne n'utilise est classée `safe` et n'attrape rien, même
  sur une propriété `status` — c'est tout l'intérêt d'avoir compté.
- Un comptage **en échec** bascule sa ligne en `unknown impact`. Un `403`, un
  `429` qui n'a plus de patience, une réponse que notion-seed ne reconnaît pas :
  la ligne n'est alors plus classée `destructive` ni `silent rewrite`,
  donc un `--fail-on=destructive,silent-rewrite` ne l'attrape plus et sort en
  `0`. Ajoutez `unknown` à votre liste si vous voulez que le garde-fou tienne
  même quand l'API refuse de compter — sans quoi une CI se croit protégée
  précisément le jour où elle ne l'est pas. Deux exceptions : une destruction
  dont le comptage de lignes échoue reste `destructive`, puisque la database
  part à la corbeille quel qu'en soit le compte ; et un changement de type garde
  sa classe mesurée, puisque sa nature est connue — seule son ampleur ne l'est
  pas.

`--fail-on` vaut aussi pour `apply`, où il est vérifié avant toute écriture.
Attention à `--skip-preflight` : hors ligne, rien n'est compté, et chaque ligne
qui aurait pu coûter ressort en `unknown impact` — un `--fail-on=destructive`
n'y attrape donc plus rien, alors que `--fail-on=unknown` les attrape toutes.

### Relire dans la pull request, appliquer après le merge

```sh
# dans la pull request : le plan à relire, figé
notion-seed plan --out plan.out --fail-on=destructive,silent-rewrite,unknown
# garder plan.out en artefact, et poster son champ `rendered` en commentaire

# après le merge, avec le même plan.out
notion-seed apply plan.out --auto-approve
git add notion-seed.state.json   # puis le committer et le pousser
```

Le plan doit être calculé sur ce qui sera mergé : si `main` a reçu d'autres
changements de configuration entre-temps, `apply` refuse avec `configuration
changed since the plan` — rebasez la pull request sur `main` et replanifiez.

Le job d'apply **doit committer** `notion-seed.state.json` : notion-seed ne le
fait pas, cela dépend de chaque CI. Un plan calculé avant ce commit est alors
refusé avec `state changed since the plan`, et c'est ce qu'on veut. Un plan
calculé sur un state qui n'a jamais été committé n'est, lui, **pas** rattrapé :
le plan et l'apply lisent le même fichier périmé, qui ne sait plus ce que le
dernier `apply` a créé — ces databases ressortiraient en créations.
