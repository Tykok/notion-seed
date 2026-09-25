# Commands

Most commands read the configuration directory (`--dir`, default `.`) and talk
to Notion through `ntn`. `init` takes no `--dir` — it only checks `ntn` — and
`version` does neither: it just prints itself. Only `import` and `apply`
write — to Notion and to the state.

| Command | Writes to Notion | Writes the state |
|---|---|---|
| `init` | no | no |
| `plan` | no | no |
| `diff` | no | no |
| `apply` | yes | yes |
| `import` | no | yes |
| `version` | no | no |

## init

```sh
notion-seed init    # checks that ntn is present, recent enough and authenticated
```

It writes nothing: it only checks that `ntn` is present, recent enough and
authenticated, before any other command tries to reach Notion.

## plan

`plan` computes and shows the changes, without applying anything.

### Output

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

### Flags

| Flag | Default | Role |
|---|---|---|
| `--dir` | `.` | configuration directory |
| `--skip-preflight` | `false` | fully offline mode: neither the `ntn` check nor the parent page check. Validates the configuration and renders the plan without any network call — resources already imported are not compared to the actual state in this mode, and come out under `Not compared` rather than under `No changes` |
| `--rate` | `5` | ceiling of API calls per second |
| `--burst` | `10` | calls tolerated in a burst |
| `--fail-on` | empty | change classes that make it exit with a non-zero code — see [In CI](#in-ci). Empty: nothing makes it fail |
| `--out` | empty | also writes the plan to this file, for `notion-seed apply <file>` — see [A reviewed plan](#a-reviewed-plan). Changes neither the output nor the exit code; refuses `--skip-preflight` |

`import` takes `--dir`, `--rate` and `--burst`, but rejects `--skip-preflight`
— the command reads the actual state, it makes no sense offline — and
`--fail-on`, since it computes no plan.

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
  the database — see [What the API cannot do](/#what-the-api-cannot-do).

You are responsible for your database. notion-seed is responsible for what you
know when you press enter. In CI, [`--fail-on`](#in-ci) hands the decision to
the workflow.

## diff

`diff` is the read-only equivalent of plan, without touching the state.

`diff` is identical to `plan` today, since `plan` does not write any state yet.
Both stay distinct so that CI usage is stable the day `plan` touches it.

## apply

```sh
notion-seed apply
notion-seed apply plan.out   # a plan reviewed with plan --out
```

`apply` recomputes the plan, shows it, asks for confirmation, then writes. It
never replays a plan: given a file written by `plan --out`, it still
recomputes, and holds the new plan to the reviewed one — see [A reviewed
plan](#a-reviewed-plan).

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
says that number is not measured, never 0. `lifecycle.acknowledge_destroy`
changes nothing about it — see [lifecycle](/yaml#lifecycle-acknowledgements). A database
moved to the trash is restored from the Notion trash; for notion-seed to manage
it again, redeclare it then run `notion-seed import`.

Renaming a database's `key` in the YAML is not a rename for notion-seed, which
only has the key to anchor the identity: the old key comes out orphaned and
goes to the trash, the new one is created empty, and `plan` shows both
separately. To keep the database, keep its `key` — with an explicit `key`,
`name` can change freely; without one, the key is derived from `name` and a
rename moves it (see [databases/*.yaml](/yaml#databases-yaml)) —
or, if the key has already changed, reattach the existing database to the new
key with `notion-seed import` before running `apply`.

It also removes **stale state entries**: a resource the YAML no longer declares
and that has already been deleted in Notion. It is a local cleanup, nothing is
written to Notion — not to be confused with a destruction, which writes, and
which the confirmation announces on its own line.

### What it withholds

::: warning A limit of the API, not a judgment
Renaming an option, changing its color, or changing the type of a `title`
property are the only declared changes notion-seed refuses to write: the API
cannot express them.
:::

What the API cannot express comes out under `Withheld — migration required`:
it is not a limit of this version, and waiting will change nothing — see
[What the API cannot do](/#what-the-api-cannot-do). `apply` exits with a
non-zero code as long as a withheld resource remains — an apply that does not
converge must be loud in CI.

A withheld resource is not touched at all: no call goes out for it. A written
resource is written in full, with one exception, which is never silent: an
update can stop between its two calls — see [On failure](#on-failure).

The migration is done by hand, with the number of rows `plan` counted:

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
not a judgment on the cost, it is a limit of the API. Writing anyway would
record in the state a state Notion does not hold, and every following run
would show phantom drift.

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

### A reviewed plan

`notion-seed plan --out plan.out` writes the plan to a file as well as printing
it. `notion-seed apply plan.out` then applies **the plan that was reviewed, or
writes nothing and says why**.

`apply` does not replay the file. It recomputes the plan exactly as it does
without one — same checks, same read-back, same counts — then, before printing
it, before the confirmation and before any write, holds it to the reviewed one:

- the same notion-seed version: another version can classify or count
  differently — an unreleased build always reports itself as `0.0.0-dev`, so
  this only tells released versions apart, not one dev build from another;
- the same workspace, the same configuration and the same state. The
  configuration is compared as notion-seed understands it: a comment or a
  reindentation does not make a plan stale, an option `key` or a `lifecycle`
  entry does;
- the same resources, of the same kind, withheld for the same reason, with the
  same lines — operation, target, class — and the same stale state entries;
- on each counted line, no more rows than reviewed, and a figure that is no
  vaguer.

| Reviewed \ now | exact M | up to M | at least M, more than M | not measured |
|---|---|---|---|---|
| exact n | M ≤ n | refused | refused | refused |
| up to n | M ≤ n | M ≤ n | refused | refused |
| at least n, more than n | M ≤ n | M ≤ n | M ≤ n | refused |
| not measured | accepted | accepted | accepted | accepted |

A lower impact is covered by what was accepted: on a live database, fewer rows
than reviewed is the common case, and an identical plan would rarely survive
the hours between a review and a merge. A line reviewed without a figure was
accepted without one — whatever class it turns out to have — so any figure and
any class now stay within it. A count that falls to zero makes its line
`safe`, which is not a change of class that refuses.

When the plan passes, `apply` prints the **recomputed** plan — its counts are
the ones that go out — and goes on as usual: `--fail-on`, announcement,
confirmation or `--auto-approve`, write. Otherwise it names each difference on
its own line, and writes nothing:

```
error: the plan recomputed now is not the one reviewed in plan.out, nothing was applied
  state changed since the plan: another apply went through in between
  database.tasks: option "High" (property "Prio") — 12 rows reviewed, 15 rows now
  → rerun `notion-seed plan --out` and have the new plan reviewed
```

The other refusals read `configuration changed since the plan` or `workspace
changed since the plan`, begin with `change added`, `change gone`, `line
added` or `line gone`, or name the class that changed. A plan file written by
another version of notion-seed, or of an unknown format, is refused before
anything is compared. When the only difference is a count that failed now — a
`403`, a `429` that ran out of patience —, the last line says to simply rerun
the same command to apply `plan.out`: the reviewed plan may still hold.

A reviewed plan that was blocked is never applied. An empty plan is written
too: applying it writes nothing and exits with `0`. A withheld resource stays
withheld, and `apply` exits with a non-zero code as it does without a file.

`plan --out` and `diff --out` change neither the output nor the exit code. The
file is written after the plan is printed and after `--fail-on`, so a CI that
fails on `--fail-on` still has the plan to review; the write is atomic, like
the state's. `--out` refuses `--skip-preflight`: an offline plan measured
nothing `apply` could be held to.

The file is JSON, format `1`. It carries what the plan shows and nothing more —
no payload, no token, no row content — so it can live in a CI artifact. Its
`rendered` field is the plan as printed, ready to post as a pull request
comment without running notion-seed again.

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

`bound` is `exact`, `at_most`, `at_least`, `more_than` or `unmeasured`. A line
that costs nothing carries neither `count` nor `bound`; an `unmeasured` line
carries no `count`. A blocked plan also carries `"blocked": true`.

## import

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

See [what is not declared](/yaml#what-is-not-declared).

## version

Print the notion-seed version.

The binary installed by `go install` reports `0.0.0-dev`: the version is only
injected by the release build.

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

### Review in the pull request, apply after the merge

```sh
# in the pull request: the plan to review, frozen
notion-seed plan --out plan.out --fail-on=destructive,silent-rewrite,unknown
# keep plan.out as an artifact, and post its `rendered` field as a comment

# after the merge, with the same plan.out
notion-seed apply plan.out --auto-approve
git add notion-seed.state.json   # then commit and push it
```

The plan must be computed on what will be merged: if `main` received other
configuration changes in between, `apply` refuses with `configuration changed
since the plan` — rebase the pull request on `main` and plan again.

The apply job **must commit** `notion-seed.state.json`: notion-seed does not do
it, since that depends on each CI. A plan computed before that commit is then
refused with `state changed since the plan`, which is what you want. A plan
computed on a state that was never committed is **not** caught: the plan and
the apply both read the same stale file, which no longer knows what the last
`apply` created — those databases would come out as creations again.
