# YAML

## Layout

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

## workspace.yaml

```yaml
# workspace.yaml
version: 1
workspace:
  parent_page_id: 33333333-3333-4333-8333-333333333333
lifecycle:
  acknowledge_destroy:
    - database.projects
```

| Field | Required | Role |
|---|---|---|
| `version` | yes | format version of the configuration — `1` |
| `workspace.parent_page_id` | yes | the Notion page under which databases are created |
| `lifecycle.acknowledge_destroy` | no | list of `database.<key>`: acknowledgement, shown in the plan — blocks nothing |
| `lifecycle.acknowledge_data_loss` | no | list of `database.<key>`: acknowledgement, shown in the plan — blocks nothing |

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

| Field | Required | Role |
|---|---|---|
| `key` | no — derived from `name` when absent | stable identity: it anchors the database across a rename and is what the state and `import` use to identify it |
| `name` | yes | display name shown in Notion |
| `description` | no | shown under the database's name in Notion |
| `icon` | no | emoji shown in Notion |
| `properties` | yes | each property, by name — see [Property types](#property-types) |

::: warning A key derived from `name` moves when you rename
Without an explicit `key`, notion-seed derives one from `name`. Rename the
database in the YAML without setting `key` first, and the derived key changes
with it: notion-seed cannot tell the result from a new database — the old key
comes out orphaned and goes to the trash, the new one is created empty. Set
`key` explicitly before renaming a database you want to keep.
:::

## Property types

Ten types are managed today:

| Type | Extra fields |
|---|---|
| `title` | — (exactly one per database) |
| `rich_text` | — |
| `number` | `format` |
| `url` | — |
| `select` | `options` |
| `multi_select` | `options` |
| `status` | `options`, each with a required `group` |
| `date` | — |
| `checkbox` | — |
| `people` | — |

A property of any other type present in Notion is left untouched — see
[What is not declared](#what-is-not-declared).

## Options

| Field | Required | Role |
|---|---|---|
| `key` | no | anchors the option's identity across a rename — see below |
| `name` | yes | display name shown in Notion |
| `color` | no | sets the option's color on creation, one of `default`, `gray`, `brown`, `orange`, `yellow`, `green`, `blue`, `purple`, `pink`, `red` — changing it on an existing option is withheld, see [What it withholds](/commands#what-it-withholds) |
| `group` | status only, required there | one of `To-do`, `In progress`, `Complete` — see below |

::: warning Removing a status option rewrites rows
The API reassigns the rows to another option, without an error. The plan counts
them before you apply.
:::

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

## Icon

The `icon` — an emoji — is written on the database, never on its data source:
measured, writing on the database updates both, writing on the data source
makes them diverge. `notion-seed` only reads the database's icon: if the data
source's is changed separately, in Notion, it does not see it.

## lifecycle — acknowledgements {#lifecycle-acknowledgements}

::: danger acknowledge_destroy blocks nothing
A database listed in `acknowledge_destroy` goes to the trash like any other
when it leaves the YAML. The key only adds a mention to the plan.
:::

`acknowledge_destroy` and `acknowledge_data_loss` **block nothing**. These two
keys are only acknowledgements of reading, shown under the resource they name.
A database removed from the YAML, declared in `acknowledge_destroy`:

```
ntn 0.22.11 — workspace Example Space (33333333-3333-4333-8333-333333333333)

Plan: 0 to add, 0 to change, 1 to destroy

  - database.tasks  [destructive]
      - database.tasks — present in the state, absent from the configuration  [destructive]
          → 3 row(s) go to the trash with it.
      → declared in lifecycle.acknowledge_destroy.

Impact: 1 database(s) in the trash with 3 row(s).
```

They say "I know what this resource holds", and nothing more. `apply`
therefore moves a database declared in `acknowledge_destroy` to the trash
exactly like any other, showing the mention.

What stops a command, from now on, is what you ask for in your workflow:
[`--fail-on`](/commands#in-ci). What informs is the measurement. What decides
is you.

### Renamed keys

`acknowledge_destroy` was called `prevent_destroy`, and `acknowledge_data_loss`
was called `allow_data_loss`: the old names promised a block that no longer
exists, a trap for whoever relied on them. The old names are **still read for
one version**, with a warning on stderr on every command that loads the
configuration, and will be removed in the next one:

```
warning: workspace.yaml: `lifecycle.prevent_destroy` is deprecated, it is now `lifecycle.acknowledge_destroy` — the old name is still read in this version only
  → rename `prevent_destroy` to `acknowledge_destroy` in workspace.yaml
```

Until then, the plan line names the key as you wrote it, so that it matches
your YAML. An old and a new name side by side have their entries merged, with a
warning saying to move the old key's entries into the new one.

## What is not declared

::: warning Undeclared options are destroyed
An undeclared property is never touched. An undeclared option is removed as
soon as its property is written: the API replaces the whole list.
:::

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

## JSON schema

The full schema is
[`schema/notion-seed.schema.json`](https://github.com/tykok/notion-seed/blob/main/schema/notion-seed.schema.json).
