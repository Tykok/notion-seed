# notion-seed

[English version](README.md) · **[Documentation](https://tykok.github.io/notion-seed/fr/)**

Notion finit par faire tourner le quotidien : projets, tâches, docs, rituels.
Gérer un espace à la main est déjà compliqué. Gérer ses bases l'est encore
plus — une option renommée, un statut retiré, et des lignes changent de valeur
sans bruit. notion-seed est un petit outil né de là : vous déclarez vos bases
en YAML et, avant toute écriture, il vous dit — en nombre de lignes — ce que le
changement coûtera à vos données.

```
ntn 0.22.11 — workspace Example Space (33333333-3333-4333-8333-333333333333)

Plan: 0 to add, 1 to change, 0 to destroy

  ~ database.tasks  [silent rewrite]
      - option "Fait" (property "Statut") — absent from the YAML: the API replaces the whole list of options  [silent rewrite]
          → 2 rows will be reassigned to another option, without a trace.

Impact: 2 values reassigned without a trace.
```

## Installation

notion-seed délègue l'authentification à [`ntn`](https://www.npmjs.com/package/ntn) :

```sh
npm i -g ntn@0.22.11
ntn login
brew install tykok/tap/notion-seed
```

Les binaires, apt, `.deb`/`.rpm`/`.apk` et `go install` : voir
[Installation](https://tykok.github.io/notion-seed/fr/installation).

## Démarrage

```sh
notion-seed init    # vérifie que ntn est présent, assez récent et authentifié
notion-seed plan    # affiche les changements
```

Les commandes, la référence YAML et l'usage en CI sont dans la
[documentation](https://tykok.github.io/notion-seed/fr/).

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

Le site de documentation vit dans [`docs/`](docs/) :

```sh
cd docs && npm ci && npm run dev
```

## Licence

GPL-3.0-or-later. Voir [`LICENSE`](LICENSE).
