# notion-seed

[Version française](README.fr.md)

Declare the structure of a Notion workspace in YAML, and measure against the
API, in number of rows, what each change will cost your data.

## Why

Declaring a Notion workspace in files is not the hard problem. The hard problem
is knowing what the API will do to your data when the declaration changes.

Nine behaviors measured against the API, which a tool that just sends the
request does not warn you about:

| Change | What the API does |
|---|---|
| Rename an option (with its id) | Returns `200`, changes nothing |
| Remove a `select` option | The affected rows are emptied |
| Remove a `multi_select` option | The affected rows lose **this value** — they are emptied only if they held no other |
| Remove a `status` option | **Reassigns the rows to another option**, without an error |
| `multi_select` → `select` | **Keeps only one value** on rows that held several |
| `select` → `multi_select` | Recreates the options: a row keeps its value only if the YAML redeclares an option **with the same name**; the others are emptied |
| `status` → `select` | Empties every row whose option is not redeclared, **and every row that never received a status**, although it reads "Not started" |
| Any type → `status` | **Gives every row a value**, empty ones included |
| Change the type of a `title` | Refused, `400` |

Measured on 2026-09-24 against API `2025-09-03`, on populated rows — the last
four on 2026-09-25, with the 90 type changes listed in
[Type changes](#type-changes).

Removing a `status` option, `multi_select` → `select` and any type → `status`
are the ones that justify the tool: the data is not lost, it is replaced by a
plausible, wrong value, indistinguishable after the fact.

`notion-seed` does not stop you. It tells you, **before writing**, how many
rows are affected. A `status` option removed from the YAML, held by two rows:

```
ntn 0.22.11 — workspace Example Space (33333333-3333-4333-8333-333333333333)

Plan: 0 to add, 1 to change, 0 to destroy

  ~ database.tasks  [silent rewrite]
      - option "Fait" (property "Statut") — absent from the YAML: the API replaces the whole list of options  [silent rewrite]
          → 2 rows will be reassigned to another option, without a trace.

Impact: 2 values reassigned without a trace.
```

The number is measured, not inferred: the same line would be classified
`safe`, with `0 rows affected`, if nobody used that option. That is what a
refusal on principle could not see, and why it was replaced by a measurement.
What could not be counted — offline, or when the query fails — comes out as
`unknown impact`, never as "nothing to lose".

### What can be counted, and what cannot

An **option removal** is always quantified: options only exist on `select`,
`status` and `multi_select`, and each has its filter.

A **destruction** is too, without a filter: the count covers every row of the
database's data source — the one the plan just read back —, the rows that go
to the trash with it. A database that holds several data sources takes them
all with it, but only one is counted: the line and the aggregate then say "at
least N row(s)", and how many data sources were not counted. It stays
`destructive` whatever the count, 0 included.

A **type change** is quantified with the filter the 2026-09-25 campaign checked
against the rows each pair really touches, on the source column, before the
write. Not every filter is exact, and the figure says which bound it is:

| Figure | Filter | Pairs |
|---|---|---|
| `N rows` | every row | any type → `status`, when no option can keep a value |
| `N rows` | `is_not_empty` | every pair where nothing survives (`date` → `number`, `people` → `select`, `status` → `checkbox`…) |
| `N rows` | checked rows | `checkbox` → `number`, `date`, `people`, and → `select` / `multi_select` without an option `Yes` |
| `N rows` | empty rows | `select` → `status`: the empty rows receive an option |
| `N rows` | non-empty (every row toward `status`), except the declared options written as numbers | `number` → `select`, `multi_select`, `status` with declared options: only an option named with the exact decimal writing of the number, without exponent, keeps it (`7` keeps 7, `7.0` keeps nothing). From a declared number of magnitude 1e21 up, never measured, the figure becomes `at least N` |
| `at least N rows` | `is_not_empty` on `rich_text` | `rich_text` → `select`, `multi_select`, `checkbox`, `people`: text made only of spaces or line breaks is not counted, and is lost too |
| `at least N rows` | non-empty, except the declared options | `rich_text` / `url` → `select`, `multi_select`, `status` with declared options: the filter ignores case and trailing spaces (on `url`, not a trailing slash), the conversion does not — such a value is not counted, and does not survive either |
| `up to N rows` | `is_not_empty` | `url`, `select`, `multi_select` → `number` or `date`, `multi_select` → `select`: some values survive the conversion |
| `up to N rows` | every row | `multi_select` → `status`: a row holding a single declared value keeps it |
| unknown | none | `rich_text` → `number` / `date`, `status` → `rich_text`, `url`, `select`, `multi_select` |

A lower bound of zero never makes a change `safe`: it reads "no rows counted,
which does not mean none is touched". An exact or upper-bound zero does — an
empty column has nothing to lose.

A `checkbox` counts only its checked rows: an unchecked box is emptied too, but
a checkbox has no empty state, so "unchecked" carries nothing "never set" does
not. The line says so.

Where no filter is sound, the line carries no number and says why:

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

The total says "at least": the never-set rows are lost too, and nobody could
count them. From `multi_select`, the rows holding an option that is not
redeclared are counted once, on their removal line, and left out of the
property line.

The class stays the one from the measurement — you know the change is
dangerous, you do not always know on how many rows.

### Type changes

All 90 ordered pairs of managed types were measured against the API, 7 on
2026-09-24 and the 83 others on 2026-09-25. The class is the cost of the body
notion-seed sends: the options the YAML declares, by name, without an id. The
plan line says, for each pair, what survives.

| from \ to | title | rich_text | number | url | select | status | multi_select | date | checkbox | people |
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

- **The API never creates an option.** Toward `select`, `multi_select` or
  `status`, a value survives only where an option with exactly its text is
  declared in the same write; from `rich_text`, the text is cut at the first
  comma (`multi_select`: split on commas). ¹ With declared options, these pairs
  become `silent rewrite`.
- **Toward `number`**, a text keeps its leading number (`'2026-01-15'` → 2026)
  and everything else is emptied: measured `destructive`, and the line names
  the rewritten values.
- ² Between option types, each current option the YAML does not redeclare
  under the same name comes out as its own `-` line with its count. Toward
  `status` its rows are not emptied: they are rewritten to one of the declared
  options.
- ³ `checkbox` becomes `Yes` / `No`: → `select` / `multi_select` is `safe` with
  an option `Yes`, → `status` with `Yes` and `No`.
- **Toward `status`, every row gets a value**, empty ones included: one of the
  declared options — a `status` always declares them.
- **`status` as a source:** a row that never received a status reads as the
  default option, yet every conversion empties it.
- **`status` → `select` was documented as lossless until 2026-09-25.** It was
  wrong: re-measured, it empties every row with no option redeclared, and
  empties the never-set rows even with them.
- **`title`:** the API refuses both directions with `400`. notion-seed withholds
  the database — see [What the API cannot do](#what-the-api-cannot-do).

You are responsible for your database. notion-seed is responsible for what you
know when you press enter. In CI, [`--fail-on`](#in-ci) hands the decision to
the workflow.

### What the API cannot do

Three changes are not expressible, measured on 2026-09-24 and 2026-09-25:

| Change | What the API does |
|---|---|
| Rename an option | Returns `200`, changes nothing |
| Change an option's color | Returns `400`, whether the option is designated by its id or by its name, and the whole PATCH of the property fails |
| Change the type of a `title` property, or turn a property into a `title` | Returns `400`: a data source holds a single title property |

`notion-seed` therefore does not write them, and withholds the whole database
as long as they are declared. The other databases of the plan are applied. The
procedure is named, with the number of rows to migrate:

1. create the new option in Notion;
2. move the rows the plan counted to it;
3. remove the old option, then rerun.

```
  ~ database.tasks  [migration required]
      ~ option "Fait" → "Terminé" (property "Statut") — the API returns 200 without changing anything: create, migrate the rows, then remove  [migration required]
          → 2 rows hold "Fait": migrate them by hand before applying.

Withheld — migration required

  ~ database.tasks
      an option must be migrated by hand: the API can neither rename an option nor change its color
      → create the new option in Notion, move the rows counted above to it, remove the old one, then rerun
```

A `title` type change is withheld the same way, with its own procedure:

```
Withheld — migration required

  ~ database.tasks
      the API refuses to change the type of a title property, in either direction
      → add a new property of the wanted type, copy the values into it in Notion, then remove the type change from the YAML and rerun
```

These are the only declared changes notion-seed refuses to write — and it is
not a judgment on the cost, it is a limit of the API. Writing anyway would record in
the state a state Notion does not hold, and every following run would show
phantom drift.

## Current status

`apply` creates the databases that are declared and absent from Notion, updates
the ones that already exist — name, description, icon and declared properties
— and moves to the trash the ones the YAML no longer declares. The state is
updated after each resource written. Two option changes and the type change
of a `title`, which the API cannot express, are withheld with the migration to
do by hand, and `apply` exits with a non-zero code as long as they remain — see
[What the API cannot do](#what-the-api-cannot-do).

| | |
|---|---|
| `init`, `version`, `plan`, `diff`, `import` | available |
| `apply` | creations, updates and destructions — see [Applying](#applying) |
| state file | `notion-seed.state.json`, written by `import` and `apply` |
| `lifecycle.prevent_destroy` / `allow_data_loss` | acknowledgements — see [lifecycle](#lifecycle--acknowledgements) |
| `--fail-on` | the CI safeguard — see [In CI](#in-ci) |

## Requirements

`notion-seed` does not handle authentication: it delegates the calls to
[`ntn`](https://www.npmjs.com/package/ntn), which stores the token in the OS
keychain.

```sh
npm i -g ntn@0.22.11
ntn login
```

The version is pinned: `0.22.11` is the only one on which the output format of
`ntn` was measured, and `notion-seed` refuses to run below it.

## Installation

Published binary — Linux, macOS and Windows, on amd64 and arm64 — from the
[Releases](https://github.com/tykok/notion-seed/releases) page:

```sh
# macOS arm64, adapt to your platform
curl -fsSL https://github.com/tykok/notion-seed/releases/latest/download/notion-seed_<version>_darwin_arm64.tar.gz \
  | tar -xz notion-seed
```

### macOS, Linux (Homebrew)

```sh
brew install tykok/tap/notion-seed
```

### Debian, Ubuntu

apt has no per-user tap mechanism, but an apt repository is only a tree of
static files: this one is hosted on GitHub Pages and signed.

```sh
sudo install -d /etc/apt/keyrings
curl -fsSL https://tykok.github.io/notion-seed/apt/gpg.key \
  | sudo tee /etc/apt/keyrings/notion-seed.asc > /dev/null
echo "deb [signed-by=/etc/apt/keyrings/notion-seed.asc] https://tykok.github.io/notion-seed/apt stable main" \
  | sudo tee /etc/apt/sources.list.d/notion-seed.list > /dev/null
sudo apt update && sudo apt install notion-seed
```

`signed-by` restricts the key to this repository alone: without it, the key
would be trusted for every apt source on the machine.

A `.deb`, `.rpm` or `.apk` is also attached to each release, if you would
rather not add anything to your sources:

```sh
sudo apt install ./notion-seed_<version>_linux_amd64.deb
```

### From source

```sh
go install github.com/tykok/notion-seed@latest
```

The binary installed by `go install` reports `0.0.0-dev`: the version is only
injected by the release build.

## Getting started

```sh
notion-seed init    # checks that ntn is present, recent enough and authenticated
notion-seed plan    # shows the changes
```

## Configuration

One directory, one workspace file, one YAML file per database:

```
.
├── workspace.yaml
└── databases/
    ├── projects.yaml
    └── tasks.yaml
```

`workspace.yaml` holds the global sections — `version`, `workspace`,
`lifecycle`. A file in `databases/` only declares databases: without this rule,
any file could divert the write target or erase a declaration, and the last one
loaded would win.

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

A `status` property must declare its `options`, and `group` is **required** on
each of them. A `status` created without options gets populated by the API with
its own default options, which the plan will not have shown — and removing them
later silently reassigns the rows. `select` and `multi_select` do not have this
constraint: their options can be managed by hand without that risk.

`group` only accepts
`To-do`, `In progress` or `Complete`. notion-seed does not choose a group for
you: an option sent without `group` is put by the API in the first group,
without an error — hence a write the plan would not have announced. On the
other types, `group` is rejected, like `format` outside `number`.

The `key` is what anchors an option's identity across a rename: without it, an
option renamed in the YAML comes out as a removal followed by an addition —
`destructive` or `silent rewrite` depending on the type and the number of rows
affected — since it cannot be followed across the name change.

The `icon` — an emoji — is written on the database, never on its data source:
measured, writing on the database updates both, writing on the data source
makes them diverge. `notion-seed` only reads the database's icon: if the data
source's is changed separately, in Notion, it does not see it.

The full JSON schema is in [`schema/notion-seed.schema.json`](schema/notion-seed.schema.json).

### `lifecycle` — acknowledgements

`prevent_destroy` and `allow_data_loss` **no longer block anything**. Despite
its name, `prevent_destroy` does not prevent destruction: these two keys are
only acknowledgements, shown under the resource they name. A database removed
from the YAML, declared in `prevent_destroy`:

```
ntn 0.22.11 — workspace Example Space (33333333-3333-4333-8333-333333333333)

Plan: 0 to add, 0 to change, 1 to destroy

  - database.tasks  [destructive]
      - database.tasks — present in the state, absent from the configuration  [destructive]
          → 3 row(s) go to the trash with it.
      → declared in lifecycle.prevent_destroy.

Impact: 1 database(s) in the trash with 3 row(s).
```

They say "I know what this resource holds", and nothing more. `apply`
therefore moves a database declared in `prevent_destroy` to the trash exactly
like any other, showing the mention. It is spelled out because a key named
`prevent_destroy` that one would believe to be blocking would be a trap: you
would rely on it, and it would not hold you back.

What stops a command, from now on, is what you ask for in your workflow:
[`--fail-on`](#in-ci). What informs is the measurement. What decides is you.

## State

`notion-seed.state.json`, next to `workspace.yaml`, keeps the Notion identity
of each managed resource and its last applied state. **Commit it**: it holds no
secret, and it is what makes the plan reproducible across machines and in CI.

Without it, `notion-seed` has no identity anchor: every declared database comes
out as a creation, even if it already exists in Notion.

This file is what makes it possible to tell "the YAML changed" from "someone
changed Notion by hand". The second case is shown under the `Drift detected
outside notion-seed` section, before the plan that brings the actual state back
to the YAML.

Only the `import` and `apply` commands write it. `plan` and `diff` read the
actual state but never touch it: a `plan` in CI therefore cannot produce a
surprise git diff, and drift does not erase itself.

## Adopting an existing database

```sh
notion-seed import database.tasks https://www.notion.so/space/Tasks-1b2c3d4e5f60...
```

The key must be declared in `databases/`. `import` adopts the database as it
is, without requiring it to already match the YAML — it is the next `plan` that
shows the mismatch.

Option `key`s are attached at that moment, by joining on the name. An option
present in Notion and absent from the YAML is recorded without a key and
counted in the output:

```
database.tasks imported — id 1b2c3d4e-5f60-4a1b-8c2d-3e4f5a6b7c8d (8 properties, 2 options without a config key)
```

### What is not declared

A **property** present in Notion and absent from the YAML is never touched: it
appears under `Unmanaged — present in Notion, left untouched`. The API updates
properties one by one, so not declaring it is enough to leave it alone.

An **option** present in Notion and absent from the YAML, on the other hand,
will be destroyed as soon as its property is written: the API replaces the
whole list of options instead of merging it. The plan therefore shows it as a
removal, with the number of rows that hold it: `destructive` for `select` and
`multi_select`, `silent rewrite` for `status` — and `safe` if that number is
zero.

In other words, "not declared = not touched" is true for properties and false
for options. It is exactly the kind of gap this tool exists to make visible.

## Applying

```sh
notion-seed apply
```

`apply` recomputes the plan, shows it, asks for confirmation, then writes. It
takes no argument: there is no plan file to replay, hence no stale plan to
apply by mistake.

### What it writes

**Creations.** A database declared in the YAML and absent from the state is
created in the parent page, read back, then recorded in the state. The state is
saved after *each* creation: an interruption leaves an exactly true file, never
a database created without an anchor — hence never a duplicate on the next run.

The read-back is not overzealousness. It brings back the option ids, without
which the state is blind to drift, and it checks the actual state against what
was announced. If the API did not write what the plan promised, `apply` says so
— about its own write.

**Updates.** A database already anchored by the state is written in two calls,
always in this order:

1. `PATCH /v1/databases/{id}` — the name, the description and the icon, if they
   change;
2. `PATCH /v1/data_sources/{id}` — the properties that carry a line in the
   plan, and only those.

What goes out is exactly what the plan showed: a property that is declared but
identical to the actual state does not go out, nor does an undeclared
property. Existing options are sent with their id, new ones without: the API
creates one for them, which the read-back brings back to the state. Under a
type change, the options are recreated: only the YAML's go out, without an id,
and each current option the YAML does not redeclare under the same name comes
out as `-`, with the number of rows it empties — or, toward `status`, that it
rewrites to one of the declared options. Measured on 2026-09-25 for every pair
among `select`, `multi_select` and `status`.

The order is chosen for failure. If the second call fails, the name and the
icon are up to date and **no row data was touched** — the least costly failure.
`apply` says so, and records in the state what went through.

**Destructions.** A database the state anchors, that the YAML no longer
declares and that Notion still holds is moved to the trash by a single call,
`PATCH /v1/databases/{id}` with `{"in_trash":true}`, then its entry is removed
from the state. The entry is removed only if the API response confirms the
trash: otherwise it is kept, and `apply` reports it as a mismatch. `plan` and
`apply` say beforehand how many rows go with it; if the count fails, the line
says that number is not measured, never 0. `lifecycle.prevent_destroy` changes
nothing about it — see [lifecycle](#lifecycle--acknowledgements). A database
moved to the trash is restored from the Notion trash; for notion-seed to manage
it again, redeclare it then run `notion-seed import`.

Renaming a database's `key` in the YAML is not a rename for notion-seed, which
only has the key to anchor the identity: the old key comes out orphaned and
goes to the trash, the new one is created empty, and `plan` shows both
separately. To keep the database, keep its `key` — `name` can change freely —
or, if the key has already changed, reattach the existing database to the new
key with `notion-seed import` before running `apply`.

It also removes **stale state entries**: a resource the YAML no longer declares
and that has already been deleted in Notion. It is a local cleanup, nothing is
written to Notion — not to be confused with a destruction, which writes, and
which the confirmation announces on its own line.

### What it withholds

What the API cannot express comes out under `Withheld — migration required`:
it is not a limit of this version, and waiting will change nothing — see
[What the API cannot do](#what-the-api-cannot-do). `apply` exits with a
non-zero code as long as a withheld resource remains — an apply that does not
converge must be loud in CI.

A withheld resource is not touched at all: no call goes out for it. A written
resource is written in full, with one exception, which is never silent: an
update can stop between its two calls — see [On failure](#on-failure).

### Confirmation

The word `apply`, typed in full, after the plan and what is going to happen, by
nature: creations, updates, moves to the trash, and stale state entries to
remove — each on its own line, since the last one writes nothing to Notion. The
`Impact` line `apply` shows only counts the resources it is going to write: a
withheld resource is excluded from it, since `apply` will not cause what it
would cost. Without a withheld resource, it is identical to `plan`'s.

```
Impact: 1 database(s) in the trash with 3 row(s).

1 database(s) will be moved to the trash in Notion.
Type "apply" to confirm:
```

It clears none of what stops the command: a blocked plan — a managed resource
Notion no longer knows — or a class rejected by `--fail-on` never reaches the
prompt.

Outside a terminal, `apply` requires `--auto-approve` rather than running
because nobody answered.

In a terminal, an end of input at the prompt — `Ctrl-D` — declines too, with
its own message: `confirmation interrupted (end of input): nothing was
applied`. An input wired to `/dev/null` is not a terminal, and asks for
`--auto-approve`.

| Flag | Default | Role |
|---|---|---|
| `--auto-approve` | `false` | applies without asking for confirmation (CI mode) |

`apply` shares `--dir`, `--rate`, `--burst` and `--fail-on` with `plan`, and
rejects `--skip-preflight`: writing offline makes no sense. `--fail-on` is
checked there **before** the confirmation and before any write.

### On failure

No rollback: archiving what was just created would be a destruction nobody
asked for. What was created, updated or moved to the trash stays written, and
the state reflects it.

An update can stop **between its two calls**: the name, the description or the
icon went through, the properties did not, and no row data was touched.
`apply` names what went through, records it in the state, and running
`notion-seed plan` again shows what is left.

A database with an ancestor page in the trash reads as live, but rejects any
write. `apply` diagnoses it and asks to restore the parent page, rather than
relaying the API's `404`, which wrongly blames sharing with the integration.

When it is the `workspace.parent_page_id` page itself that is in the trash,
`plan` and `apply` stop at the initial check, before any plan and hence before
any write: restore it, or point `parent_page_id` to a live page. It is also true
when a page above it is in the trash: Notion reports it on the parent page.
`--skip-preflight` skips this check like the others.

A move to the trash that fails — API rejection, `404`, unknown outcome — leaves
the state entry **in place**: `apply` stops and points to `notion-seed plan`,
which reads the actual state back. A database already gone comes out there as a
stale state entry, which a following `apply` removes without writing anything; a
database still there comes out as a destruction. Dropping the identity on the
strength of a failure would make a possibly still live database invisible.

Under an ancestor page already in the trash, Notion also rejects the move to
the trash, and the database goes there anyway with its page. `apply` offers
both outcomes: restore the parent page then rerun `apply`, or permanently
delete the parent page from the Notion trash, then run `notion-seed plan`
again: if Notion no longer knows the database, its entry comes out there as a
stale state entry, which a following `apply` removes without writing anything.

If the outcome of a creation is **unknown** — a timeout does not say whether the
server applied the mutation — `apply` stops short without chaining, and names
the database, the parent page and the steps to follow: check in Notion, then
`notion-seed import` if it exists. For an update, the identity is already in
the state: `notion-seed plan` is enough to see what Notion holds.

## Output

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

Plain text, no color: the output must stay readable in a pipe and in CI. The
plan goes to stdout, the retry waits to stderr — stdout carries only the plan,
so that it stays identical between two runs.

A blocked plan exits with a non-zero code — a managed resource Notion no longer
knows, for example. The measured cost, on the other hand, only makes it exit
with an error if you asked for it with [`--fail-on`](#in-ci).

## `plan` and `diff` flags

| Flag | Default | Role |
|---|---|---|
| `--dir` | `.` | configuration directory |
| `--skip-preflight` | `false` | fully offline mode: neither the `ntn` check nor the parent page check. Validates the configuration and renders the plan without any network call — resources already imported are not compared to the actual state in this mode, and come out under `Not compared` rather than under `No changes` |
| `--rate` | `5` | ceiling of API calls per second |
| `--burst` | `10` | calls tolerated in a burst |
| `--fail-on` | empty | change classes that make it exit with a non-zero code — see [In CI](#in-ci). Empty: nothing makes it fail |

`import` takes `--dir`, `--rate` and `--burst`, but rejects `--skip-preflight`
— the command reads the actual state, it makes no sense offline — and
`--fail-on`, since it computes no plan.

`diff` is identical to `plan` today, since `plan` does not write any state yet.
Both stay distinct so that CI usage is stable the day `plan` touches it.

## In CI

By default, a costly plan is shown and exits with `0`: notion-seed measures, it
does not decide. `--fail-on` lists the classes on which *your* workflow wants
to stop.

```sh
notion-seed diff --fail-on=silent-rewrite,destructive
```

| Value | What it catches |
|---|---|
| `destructive` | data is lost: values are emptied. Toward `number`, some texts also keep only their leading number — the line says so |
| `silent-rewrite` | data is replaced by other data, without a trace |
| `unknown` | the impact could not be measured: failed count, or offline |
| `migration` | the API accepts the request and changes nothing: the rows must be migrated by hand |

Five things to know:

- The list is **enumerated, not a threshold**. `safe`, `destructive` and
  `silent rewrite` do form a scale, but `unknown impact` has no place in it: an
  unmeasured change can turn out harmless as well as catastrophic. Asking for
  `destructive` therefore does not ask for "everything at least as serious" —
  name each class you want to catch.
- An unknown value — a typo in a class name — is rejected **before any network
  call**, and the message lists the accepted values. Otherwise a misconfigured
  CI would go green believing it is protected, which is the worst possible
  failure mode for this flag.
- The plan is **rendered anyway** before exiting with an error: the exit code
  says there is something to look at, the output says what.
- The trigger works on the detail lines, with their measured class. An option
  nobody uses is classified `safe` and catches nothing, even on a `status`
  property — that is the whole point of having counted.
- A **failed** count switches its line to `unknown impact`. A `403`, a `429`
  that ran out of patience, a response notion-seed does not recognize: the line
  is then no longer classified `destructive` nor `silent rewrite`, so a
  `--fail-on=destructive,silent-rewrite` no longer catches it and exits with
  `0`. Add `unknown` to your list if you want the safeguard to hold even when
  the API refuses to count — otherwise a CI believes it is protected precisely
  the day it is not. Two exceptions: a destruction whose row count fails stays
  `destructive`, since the database goes to the trash whatever the count; and
  a type change keeps its measured class, since its nature is known — only its
  extent is not.

`--fail-on` also applies to `apply`, where it is checked before any write.
Beware of `--skip-preflight`: offline, nothing is counted, and every line that
could have cost comes out as `unknown impact` — a `--fail-on=destructive`
therefore no longer catches anything there, whereas `--fail-on=unknown` catches
them all.

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

## License

GPL-3.0-or-later. See [`LICENSE`](LICENSE).
