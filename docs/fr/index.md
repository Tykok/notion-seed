---
layout: home

hero:
  name: notion-seed
  text: L'addition avant l'écriture.
  tagline: Déclarez vos bases Notion en YAML. Avant toute écriture, voyez — en nombre de lignes — ce que chaque changement coûtera à vos données.
  actions:
    - theme: brand
      text: Commencer
      link: /fr/installation
    - theme: alt
      text: GitHub
      link: https://github.com/tykok/notion-seed

features:
  - title: Chiffré avant d'écrire
    details: Chaque ligne du plan dit combien de lignes perdront leur valeur — et combien en recevront une plausible et fausse.
  - title: Détection de dérive
    details: Le state distingue « le YAML a changé » de « quelqu'un a modifié Notion à la main », et montre le second avant le plan.
  - title: Un garde-fou CI à votre main
    details: "plan --fail-on=<classes> arrête le workflow sur les changements que vous nommez, et rien d'autre."
  - title: Aucun refus de principe
    details: Il informe, vous décidez. Seul ce que l'API ne sait pas exprimer est retenu — avec la marche à suivre à la main.
---

## Pourquoi

Notion finit par faire tourner le quotidien : projets, tâches, docs, rituels.
Gérer un espace à la main est déjà compliqué. Gérer ses bases l'est encore
plus — une option renommée, un statut retiré, et des lignes changent de valeur
sans bruit. notion-seed est un petit outil né de là.

Déclarer un workspace Notion en fichiers n'est pas le problème difficile. Le
problème difficile, c'est de savoir ce que l'API va faire de vos données quand
la déclaration change.

Six comportements mesurés contre l'API, qu'un outil qui se contente d'envoyer
la requête ne vous signale pas :

| Changement | Ce que fait l'API |
|---|---|
| Renommer une option (avec son id) | Répond `200`, ne change rien |
| Retirer une option de `select` | Les lignes concernées passent à vide |
| Retirer une option de `multi_select` | Les lignes concernées perdent **cette valeur** — elles ne passent à vide que si elles n'en portaient pas d'autre |
| Retirer une option de `status` | **Réassigne les lignes à une autre option**, sans erreur |
| `multi_select` → `select` | **Ne garde qu'une valeur** sur les lignes qui en portaient plusieurs |
| `select` → `multi_select` | Recrée les options : une ligne ne garde sa valeur que si le YAML redéclare une option **de même nom** ; les autres passent à vide |

Mesurés le 2026-09-24 contre l'API `2025-09-03`, sur des lignes remplies — le
dernier le 2026-09-25.

Le retrait d'option de `status` et `multi_select` → `select` sont ceux qui
justifient l'outil : la donnée n'est pas perdue, elle est remplacée par une
valeur plausible et fausse, indistinguable après coup.

`notion-seed` ne vous en empêche pas. Il vous dit, **avant d'écrire**, combien
de lignes sont concernées. Une option de `status` retirée du YAML, deux lignes
la portent :

```
ntn 0.22.11 — workspace Example Space (33333333-3333-4333-8333-333333333333)

Plan: 0 to add, 1 to change, 0 to destroy

  ~ database.tasks  [silent rewrite]
      - option "Fait" (property "Statut") — absent from the YAML: the API replaces the whole list of options  [silent rewrite]
          → 2 rows will be reassigned to another option, without a trace.

Impact: 2 values reassigned without a trace.
```

Le chiffre est mesuré, pas déduit : la même ligne serait classée `safe`, avec
`0 rows affected`, si personne n'utilisait cette option. C'est ce qu'un refus
par principe ne savait pas voir, et pourquoi il a été remplacé par une mesure.
Ce qui n'a pas pu être compté — hors ligne, ou quand la requête échoue —
ressort en `unknown impact`, jamais en « rien à perdre ».

::: tip Vous restez aux commandes
Vous êtes responsable de votre base. notion-seed est responsable de ce que vous
savez au moment d'appuyer sur entrée. En CI, [`--fail-on`](/fr/commands#en-ci)
confie la décision au workflow.
:::

## Ce que l'API ne sait pas faire

Deux changements sont inexprimables, mesurés le 2026-09-24 :

| Changement | Ce que fait l'API |
|---|---|
| Renommer une option | Répond `200`, ne change rien |
| Changer la couleur d'une option | Répond `400`, que l'option soit désignée par son id ou par son nom, et tout le PATCH de la propriété échoue |

`notion-seed` ne les écrit donc pas, et retient la database entière tant qu'ils
sont déclarés. Les autres databases du plan s'appliquent. Voir
[Ce qu'il retient](/fr/commands#ce-qu-il-retient) pour la marche à suivre et le
nombre de lignes qu'elle nomme.

## État actuel

`apply` crée les databases déclarées et absentes de Notion, modifie celles qui
existent déjà — nom, description, icône et propriétés déclarées — et met à la
corbeille celles que le YAML ne déclare plus. Le state est mis à jour après
chaque ressource écrite. Deux changements d'option, que l'API ne sait pas
exprimer, sont retenus avec la migration à faire à la main, et `apply` sort en
code non nul tant qu'ils restent — voir
[Ce que l'API ne sait pas faire](#ce-que-l-api-ne-sait-pas-faire).

| | |
|---|---|
| `init`, `version`, `plan`, `diff`, `import` | disponibles |
| `apply` | créations, modifications et destructions — voir [Appliquer](/fr/commands#apply) |
| fichier de state | `notion-seed.state.json`, écrit par `import` et `apply` |
| `lifecycle.prevent_destroy` / `allow_data_loss` | accusés de lecture — voir [lifecycle](/fr/yaml#lifecycle-des-accuses-de-lecture) |
| `--fail-on` | le garde-fou de CI — voir [En CI](/fr/commands#en-ci) |
