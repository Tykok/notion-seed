// SPDX-License-Identifier: GPL-3.0-or-later

package change

// typeChangeImpact is what the Notion API does to a column when its type
// changes. MEASURED on 2026-09-24 against API 2025-09-03 (ntn 0.22.11), on a
// filled row, in a throwaway database in a personal workspace.
//
// 7 pairs out of 90 possible. Everything else is unknown and must stay so:
// writing "safe" on a pair never tried would be precisely the flaw this tool
// exists to make visible.
//
//	from         to            row before       row after
//	select    → multi_select   "Alpha"          ["Alpha"]        lossless
//	number    → rich_text      7                "7"              lossless
//	status    → select         "Ouvert"         (empty)          DESTRUCTIVE
//	date      → rich_text      2026-01-15       "2026-01-15"     lossless
//	multi_select → select      ["Un","Deux"]    "Un"             REWRITE
//	rich_text → number         "42 texte"       42               REWRITE
//	checkbox  → number         true             (empty)          DESTRUCTIVE
//
// status → select was first read as lossless on 2026-09-24. Re-measured on
// 2026-09-25, a PATCH with `{}` empties every row (5/5), and even with the
// options redeclared by name, a row that never received a status — read back
// as "Not started" — is emptied too. No filter isolates such a row.
//
// multi_select → select deserves attention: the row held two values, it holds
// one, and nothing says the second one ever existed. That is the exact
// definition of a silent rewrite, so it was not specific to status.
var typeChangeImpact = map[[2]string]Class{
	{"select", "multi_select"}: ClassSafe,
	{"number", "rich_text"}:    ClassSafe,
	{"date", "rich_text"}:      ClassSafe,

	{"multi_select", "select"}: ClassSilentRewrite,
	{"rich_text", "number"}:    ClassSilentRewrite,

	{"checkbox", "number"}: ClassDestructive,
	{"status", "select"}:   ClassDestructive,
}

// ClassifyTypeChange says what going from one type to another costs.
//
// A pair missing from the table returns ClassUnknownImpact: the honest answer
// when nobody has tried.
func ClassifyTypeChange(from, to string) Class {
	if from == to {
		return ClassSafe
	}
	if c, measured := typeChangeImpact[[2]string{from, to}]; measured {
		return c
	}
	return ClassUnknownImpact
}
