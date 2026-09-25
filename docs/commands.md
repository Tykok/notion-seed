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

`import` takes `--dir`, `--rate` and `--burst`, but rejects `--skip-preflight`
— the command reads the actual state, it makes no sense offline — and
`--fail-on`, since it computes no plan.

### What can be counted, and what cannot

Counting takes a filter, and `notion-seed` can only build one for `select`,
`status` and `multi_select`.

An **option removal** is therefore always quantified: options only exist on
these three types.

A **destruction** is too, without a filter: the count covers every row of the
database's data source — the one the plan just read back —, the rows that go
to the trash with it. A database that holds several data sources takes them
all with it, but only one is counted: the line and the aggregate then say "at
least N row(s)", and how many data sources were not counted. It stays
`destructive` whatever the count, 0 included.

A **type change** is quantified only if the source column is of one of them.
Of the three dangerous pairs below, only one is — and only
`multi_select` → `select` is among the behaviors measured on the overview
([Why](/#why)):

| Pair | Quantified? |
|---|---|
| `multi_select` → `select` | yes — the source column can be filtered |
| `rich_text` → `number` | no |
| `checkbox` → `number` | no |

In the last two cases, the line carries no number but says so:

```
      ~ property "Notes" — rich_text → number  [silent rewrite]
          → actual impact unknown: notion-seed cannot count the rows of a rich_text property.
```

The class stays the one from the measurement — you know the change is
dangerous, you do not know on how many rows. And `--fail-on=unknown` catches
them.

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
out as `-`, with the number of rows it empties. Only `select` → `multi_select`
was measured; the other pairs among `select`, `multi_select` and `status`
follow the same rule, without having been measured.

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
nothing about it — see [lifecycle](/yaml#lifecycle-acknowledgements). A database
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
Renaming an option or changing its color is the only declared change
notion-seed refuses to write: the API cannot express it.
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

It is the only declared change notion-seed refuses to write — and it is not a
judgment on the cost, it is a limit of the API. Writing anyway would record in
the state a state Notion does not hold, and every following run would show
phantom drift.

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
| `destructive` | data is lost, without any wrong value being written |
| `silent-rewrite` | data is replaced by other data, without a trace |
| `unknown` | the impact could not be measured: type outside the table, failed count, or offline |
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
  the day it is not. Only exception: a destruction whose row count fails stays
  `destructive`, since the database goes to the trash whatever the count.

`--fail-on` also applies to `apply`, where it is checked before any write.
Beware of `--skip-preflight`: offline, nothing is counted, and every line that
could have cost comes out as `unknown impact` — a `--fail-on=destructive`
therefore no longer catches anything there, whereas `--fail-on=unknown` catches
them all.
