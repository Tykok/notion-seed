---
layout: home

hero:
  name: notion-seed
  text: The bill before the write.
  tagline: Declare your Notion databases in YAML. Before anything is written, see — in rows — what each change will cost your data.
  actions:
    - theme: brand
      text: Get started
      link: /installation
    - theme: alt
      text: GitHub
      link: https://github.com/tykok/notion-seed

features:
  - title: Measured before written
    details: Every line of the plan says how many rows will lose their value — and how many will get a plausible, wrong one.
  - title: Drift detection
    details: The state tells "the YAML changed" from "someone edited Notion by hand", and shows the second before the plan.
  - title: A CI safeguard you choose
    details: "plan --fail-on=<classes> stops the workflow on the changes you name, and nothing else."
  - title: No refusal on principle
    details: It informs, you decide. Only what the API cannot express is withheld — with the manual steps to do it.
---

## Why

Notion ends up running your day: projects, tasks, docs, rituals. Managing a
workspace by hand is already hard. Managing its databases is harder — rename an
option, remove a status, and rows quietly change value. notion-seed is a small
tool born from that.

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
[Type changes](/commands#type-changes).

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

::: tip You stay in charge
You are responsible for your database. notion-seed is responsible for what you
know when you press enter. In CI, [`--fail-on`](/commands#in-ci) hands the
decision to the workflow.
:::

## What the API cannot do

Three changes are not expressible, measured on 2026-09-24 and 2026-09-25:

| Change | What the API does |
|---|---|
| Rename an option | Returns `200`, changes nothing |
| Change an option's color | Returns `400`, whether the option is designated by its id or by its name, and the whole PATCH of the property fails |
| Change the type of a `title` property, or turn a property into a `title` | Returns `400`: a data source holds a single title property |

`notion-seed` therefore does not write them, and withholds the whole database
as long as they are declared. The other databases of the plan are applied. See
[What it withholds](/commands#what-it-withholds) for the migration procedure
and the number of rows it names.

## Current status

`apply` creates the databases that are declared and absent from Notion, updates
the ones that already exist — name, description, icon and declared properties
— and moves to the trash the ones the YAML no longer declares. The state is
updated after each resource written. Two option changes and the type change of
a `title`, which the API cannot express, are withheld with the migration to do
by hand, and `apply` exits with a non-zero code as long as they remain — see
[What the API cannot do](#what-the-api-cannot-do).

| | |
|---|---|
| `init`, `version`, `plan`, `diff`, `import` | available |
| `apply` | creations, updates and destructions — see [Applying](/commands#apply) |
| state file | `notion-seed.state.json`, written by `import` and `apply` |
| `lifecycle.acknowledge_destroy` / `acknowledge_data_loss` | acknowledgements — see [lifecycle](/yaml#lifecycle-acknowledgements) |
| `--fail-on` | the CI safeguard — see [In CI](/commands#in-ci) |
