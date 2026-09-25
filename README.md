# notion-seed

[Version française](README.fr.md) · **[Documentation](https://tykok.github.io/notion-seed/)**

Notion ends up running your day: projects, tasks, docs, rituals. Managing a
workspace by hand is already hard. Managing its databases is harder — rename an
option, remove a status, and rows quietly change value. notion-seed is a small
tool born from that: declare your databases in YAML, and before anything is
written, it tells you — in rows — what the change will cost your data.

```
ntn 0.22.11 — workspace Example Space (33333333-3333-4333-8333-333333333333)

Plan: 0 to add, 1 to change, 0 to destroy

  ~ database.tasks  [silent rewrite]
      - option "Fait" (property "Statut") — absent from the YAML: the API replaces the whole list of options  [silent rewrite]
          → 2 rows will be reassigned to another option, without a trace.

Impact: 2 values reassigned without a trace.
```

## Install

notion-seed delegates authentication to [`ntn`](https://www.npmjs.com/package/ntn):

```sh
npm i -g ntn@0.22.11
ntn login
brew install tykok/tap/notion-seed
```

Binaries, apt, `.deb`/`.rpm`/`.apk` and `go install`: see
[Installation](https://tykok.github.io/notion-seed/installation).

## Getting started

```sh
notion-seed init    # checks that ntn is present, recent enough and authenticated
notion-seed plan    # shows the changes
```

Commands, YAML reference and CI usage are in the
[documentation](https://tykok.github.io/notion-seed/).

## Development

```sh
go test ./...
gofmt -l .
go vet ./...
./scripts/check-spdx.sh
```

All four run in CI on every push and every pull request.

Dependency updates come through Dependabot, grouped once a week. They are
merged automatically as soon as CI passes, without review: the four commands
above act as the review. A red CI leaves the PR open.

The documentation site lives in [`docs/`](docs/):

```sh
cd docs && npm ci && npm run dev
```

## License

GPL-3.0-or-later. See [`LICENSE`](LICENSE).
