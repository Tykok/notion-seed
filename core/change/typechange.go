// SPDX-License-Identifier: GPL-3.0-or-later

package change

// TypeChange is what notion-seed knows about a property type change, before
// any count.
type TypeChange struct {
	// Class is the cost of the change with the body notion-seed sends: the
	// options the YAML declares, by name, without an id.
	Class Class
	// Refused says the API rejects the change outright (400): apply must never
	// send it. It goes with ClassMigration, which withholds the resource.
	Refused bool
	// Note says, in a few words, what survives the conversion. It is the
	// measured behavior, not a guess, and the plan shows it on the line.
	Note string
}

// entry is one measured pair.
//
// class is the cost with the body notion-seed sends when the YAML declares no
// option on the target. withOptions, when set, is the cost once at least one
// option is declared: toward select, multi_select or status, the API never
// creates an option, and a value survives only where an option with its exact
// text goes in the same PATCH — so the declared options change the nature of
// the change, not only its extent.
type entry struct {
	class       Class
	withOptions *Class
	refused     bool
	measured    string
	survives    string
}

func cls(c Class) *Class { return &c }

// Notes shared by several pairs. Each one is the measured behavior of the
// 2026-09-25 campaign (API 2025-09-03, ntn 0.22.11), stated once.
const (
	titleFrom = "the API refuses to change the type of a title property (400 " +
		"validation_error)"
	titleTo = "a data source holds a single title property: the API refuses (400 " +
		"validation_error)"
	byText = "the API creates no option: values survive only where an option " +
		"with the same text is declared"
	byTextCut   = byText + ", cut at the first comma"
	byTextSplit = byText + ", split on commas"
	toStatus    = "every row, empty ones included, gets the default option; " +
		"with declared options, a value survives only where an option with the " +
		"same text is declared, and the others get the first one"
	toStatusByName = "a value survives only where an option with the same name " +
		"is declared; the others and empty rows get the first declared option, " +
		"or the API's default one"
	toStatusAll = "every row, empty ones included, gets the first declared " +
		"option or the API's default one, even with an option of the same text"
	nothing        = "nothing survives"
	unchecked      = "nothing survives: every row becomes unchecked"
	uncheckedEmpty = "every row is emptied, checked or not"
	leadingNumber  = "the leading number is kept ('42 text' → 42, '2026-01-15' → " +
		"2026, a false value); everything else is emptied"
	isoDate           = "only a value that reads as an ISO date survives; everything else is emptied"
	asText            = "everything survives, as text"
	byName            = "a value survives only where an option with the same name is declared"
	statusImplicit    = "explicit values are kept; a row that never received a status is emptied"
	statusByName      = byName + "; a row that never received a status is emptied"
	checkboxByOption  = "a checked row survives only as an option \"Yes\"; unchecked rows are emptied without an option \"No\""
	checkboxToStatus  = "rows survive only as options \"Yes\" and \"No\"; the others get the first declared option, or the API's default one"
	measuredFirst     = "2026-09-24"
	measuredCampaign  = "2026-09-25"
	measuredRemeasure = "2026-09-24, re-measured 2026-09-25"
)

// typeChangeTable is what the Notion API does to a column when its type
// changes, for the 90 ordered pairs of managed types. MEASURED against API
// 2025-09-03 (ntn 0.22.11), on filled rows, in throwaway databases of a
// personal workspace: 7 pairs on 2026-09-24, the 83 others on 2026-09-25.
//
// Nothing here is inferred. A pair outside this table — an unmanaged type —
// stays ClassUnknownImpact: writing "safe" on a pair never tried would be
// precisely the flaw this tool exists to make visible.
//
// Three lessons of the campaign shape the table:
//
//   - status → select, first read as lossless, empties every row with `{}`,
//     and even with the options redeclared a row that never received a
//     status (read back as "Not started") is emptied. No filter isolates it.
//   - Toward status, EVERY row gets a value, empty ones included: a silent
//     rewrite of the whole column.
//   - A pair that rewrites some values and empties others is classed as a
//     silent rewrite: a false value is worse than a missing one.
var typeChangeTable = map[[2]string]entry{
	// title: the API refuses both directions.
	{"title", "rich_text"}:    {class: ClassMigration, refused: true, measured: measuredCampaign, survives: titleFrom},
	{"title", "number"}:       {class: ClassMigration, refused: true, measured: measuredCampaign, survives: titleFrom},
	{"title", "url"}:          {class: ClassMigration, refused: true, measured: measuredCampaign, survives: titleFrom},
	{"title", "select"}:       {class: ClassMigration, refused: true, measured: measuredCampaign, survives: titleFrom},
	{"title", "status"}:       {class: ClassMigration, refused: true, measured: measuredCampaign, survives: titleFrom},
	{"title", "multi_select"}: {class: ClassMigration, refused: true, measured: measuredCampaign, survives: titleFrom},
	{"title", "date"}:         {class: ClassMigration, refused: true, measured: measuredCampaign, survives: titleFrom},
	{"title", "checkbox"}:     {class: ClassMigration, refused: true, measured: measuredCampaign, survives: titleFrom},
	{"title", "people"}:       {class: ClassMigration, refused: true, measured: measuredCampaign, survives: titleFrom},
	{"rich_text", "title"}:    {class: ClassMigration, refused: true, measured: measuredCampaign, survives: titleTo},
	{"number", "title"}:       {class: ClassMigration, refused: true, measured: measuredCampaign, survives: titleTo},
	{"url", "title"}:          {class: ClassMigration, refused: true, measured: measuredCampaign, survives: titleTo},
	{"select", "title"}:       {class: ClassMigration, refused: true, measured: measuredCampaign, survives: titleTo},
	{"status", "title"}:       {class: ClassMigration, refused: true, measured: measuredCampaign, survives: titleTo},
	{"multi_select", "title"}: {class: ClassMigration, refused: true, measured: measuredCampaign, survives: titleTo},
	{"date", "title"}:         {class: ClassMigration, refused: true, measured: measuredCampaign, survives: titleTo},
	{"checkbox", "title"}:     {class: ClassMigration, refused: true, measured: measuredCampaign, survives: titleTo},
	{"people", "title"}:       {class: ClassMigration, refused: true, measured: measuredCampaign, survives: titleTo},

	// rich_text.
	{"rich_text", "number"}:       {class: ClassSilentRewrite, measured: measuredRemeasure, survives: leadingNumber},
	{"rich_text", "url"}:          {class: ClassSafe, measured: measuredCampaign, survives: "everything survives, as is, even text that is not a URL"},
	{"rich_text", "select"}:       {class: ClassDestructive, withOptions: cls(ClassSilentRewrite), measured: measuredCampaign, survives: byTextCut},
	{"rich_text", "status"}:       {class: ClassSilentRewrite, measured: measuredCampaign, survives: toStatus + ", cut at the first comma"},
	{"rich_text", "multi_select"}: {class: ClassDestructive, withOptions: cls(ClassSilentRewrite), measured: measuredCampaign, survives: byTextSplit},
	{"rich_text", "date"}:         {class: ClassDestructive, measured: measuredCampaign, survives: "only ISO and DD/MM/YYYY dates survive; everything else is emptied"},
	{"rich_text", "checkbox"}:     {class: ClassDestructive, measured: measuredCampaign, survives: "nothing survives: every row becomes unchecked, 'true' included"},
	{"rich_text", "people"}:       {class: ClassDestructive, measured: measuredCampaign, survives: "nothing survives: neither a name nor an e-mail resolves to a member"},

	// number.
	{"number", "rich_text"}:    {class: ClassSafe, measured: measuredFirst, survives: asText},
	{"number", "url"}:          {class: ClassSafe, measured: measuredCampaign, survives: asText},
	{"number", "select"}:       {class: ClassDestructive, measured: measuredCampaign, survives: byText},
	{"number", "status"}:       {class: ClassSilentRewrite, measured: measuredCampaign, survives: toStatus},
	{"number", "multi_select"}: {class: ClassDestructive, measured: measuredCampaign, survives: byText},
	{"number", "date"}:         {class: ClassDestructive, measured: measuredCampaign, survives: nothing},
	{"number", "checkbox"}:     {class: ClassDestructive, measured: measuredCampaign, survives: unchecked},
	{"number", "people"}:       {class: ClassDestructive, measured: measuredCampaign, survives: nothing},

	// url.
	{"url", "rich_text"}:    {class: ClassSafe, measured: measuredCampaign, survives: asText},
	{"url", "number"}:       {class: ClassSilentRewrite, measured: measuredCampaign, survives: leadingNumber},
	{"url", "select"}:       {class: ClassDestructive, withOptions: cls(ClassSilentRewrite), measured: measuredCampaign, survives: byTextCut},
	{"url", "status"}:       {class: ClassSilentRewrite, measured: measuredCampaign, survives: toStatus},
	{"url", "multi_select"}: {class: ClassDestructive, withOptions: cls(ClassSilentRewrite), measured: measuredCampaign, survives: byTextSplit},
	{"url", "date"}:         {class: ClassDestructive, measured: measuredCampaign, survives: isoDate},
	{"url", "checkbox"}:     {class: ClassDestructive, measured: measuredCampaign, survives: unchecked},
	{"url", "people"}:       {class: ClassDestructive, measured: measuredCampaign, survives: "nothing survives, member e-mails included"},

	// select. Toward an option type, the removal lines of retypedRemovalLines
	// count the values whose option is not redeclared.
	{"select", "rich_text"}:    {class: ClassSafe, measured: measuredCampaign, survives: "the option name survives, as text"},
	{"select", "number"}:       {class: ClassSilentRewrite, measured: measuredCampaign, survives: leadingNumber},
	{"select", "url"}:          {class: ClassSafe, measured: measuredCampaign, survives: "the option name survives, as text"},
	{"select", "status"}:       {class: ClassSilentRewrite, measured: measuredCampaign, survives: toStatusByName},
	{"select", "multi_select"}: {class: ClassSafe, measured: measuredRemeasure, survives: byName},
	{"select", "date"}:         {class: ClassDestructive, measured: measuredCampaign, survives: isoDate},
	{"select", "checkbox"}:     {class: ClassDestructive, measured: measuredCampaign, survives: unchecked},
	{"select", "people"}:       {class: ClassDestructive, measured: measuredCampaign, survives: nothing},

	// status. A row that never received a status reads as the default option,
	// yet every conversion empties it, and no filter isolates it.
	{"status", "rich_text"}:    {class: ClassDestructive, measured: measuredCampaign, survives: statusImplicit},
	{"status", "number"}:       {class: ClassDestructive, measured: measuredCampaign, survives: nothing},
	{"status", "url"}:          {class: ClassDestructive, measured: measuredCampaign, survives: statusImplicit},
	{"status", "select"}:       {class: ClassDestructive, measured: measuredRemeasure, survives: statusByName},
	{"status", "multi_select"}: {class: ClassDestructive, measured: measuredCampaign, survives: statusByName},
	{"status", "date"}:         {class: ClassDestructive, measured: measuredCampaign, survives: nothing},
	{"status", "checkbox"}:     {class: ClassDestructive, measured: measuredCampaign, survives: unchecked},
	{"status", "people"}:       {class: ClassDestructive, measured: measuredCampaign, survives: nothing},

	// multi_select.
	{"multi_select", "rich_text"}: {class: ClassSafe, measured: measuredCampaign, survives: "the values survive, joined by ','"},
	{"multi_select", "number"}:    {class: ClassSilentRewrite, measured: measuredCampaign, survives: "a lone leading number is kept (['2026-01-15'] → 2026, a false value); everything else is emptied"},
	{"multi_select", "url"}:       {class: ClassSafe, measured: measuredCampaign, survives: "the values survive, joined by ','"},
	{"multi_select", "select"}:    {class: ClassSilentRewrite, measured: measuredFirst, survives: "only the first value is kept, and nothing says the others existed"},
	{"multi_select", "status"}:    {class: ClassSilentRewrite, measured: measuredCampaign, survives: "only the first value is kept; " + toStatusByName},
	{"multi_select", "date"}:      {class: ClassDestructive, measured: measuredCampaign, survives: "only a lone ISO date survives; everything else is emptied"},
	{"multi_select", "checkbox"}:  {class: ClassDestructive, measured: measuredCampaign, survives: unchecked},
	{"multi_select", "people"}:    {class: ClassDestructive, measured: measuredCampaign, survives: nothing},

	// date.
	{"date", "rich_text"}:    {class: ClassSafe, measured: measuredRemeasure, survives: "the date survives as text, time and range included"},
	{"date", "number"}:       {class: ClassDestructive, measured: measuredCampaign, survives: nothing},
	{"date", "url"}:          {class: ClassDestructive, measured: measuredCampaign, survives: "nothing survives, unlike date → rich_text"},
	{"date", "select"}:       {class: ClassDestructive, measured: measuredCampaign, survives: "nothing survives, even with an option of the same text"},
	{"date", "status"}:       {class: ClassSilentRewrite, measured: measuredCampaign, survives: toStatusAll},
	{"date", "multi_select"}: {class: ClassDestructive, measured: measuredCampaign, survives: "nothing survives, even with an option of the same text"},
	{"date", "checkbox"}:     {class: ClassDestructive, measured: measuredCampaign, survives: unchecked},
	{"date", "people"}:       {class: ClassDestructive, measured: measuredCampaign, survives: nothing},

	// checkbox: true → "Yes", false → "No". An unchecked box carries no
	// information distinct from "never set" — a checkbox has no empty state —
	// so only checked rows can lose one. The declared options are read by
	// TypeChangeOf, not here.
	{"checkbox", "rich_text"}:    {class: ClassSafe, measured: measuredCampaign, survives: "checked becomes 'Yes', unchecked 'No'"},
	{"checkbox", "number"}:       {class: ClassDestructive, measured: measuredRemeasure, survives: uncheckedEmpty},
	{"checkbox", "url"}:          {class: ClassSafe, measured: measuredCampaign, survives: "checked becomes 'Yes', unchecked 'No'"},
	{"checkbox", "select"}:       {class: ClassDestructive, measured: measuredCampaign, survives: checkboxByOption},
	{"checkbox", "status"}:       {class: ClassSilentRewrite, measured: measuredCampaign, survives: checkboxToStatus},
	{"checkbox", "multi_select"}: {class: ClassDestructive, measured: measuredCampaign, survives: checkboxByOption},
	{"checkbox", "date"}:         {class: ClassDestructive, measured: measuredCampaign, survives: uncheckedEmpty},
	{"checkbox", "people"}:       {class: ClassDestructive, measured: measuredCampaign, survives: uncheckedEmpty},

	// people.
	{"people", "rich_text"}:    {class: ClassSafe, measured: measuredCampaign, survives: "a mention of the member survives"},
	{"people", "number"}:       {class: ClassDestructive, measured: measuredCampaign, survives: nothing},
	{"people", "url"}:          {class: ClassDestructive, measured: measuredCampaign, survives: nothing},
	{"people", "select"}:       {class: ClassDestructive, measured: measuredCampaign, survives: "nothing survives, even with an option named after the member"},
	{"people", "status"}:       {class: ClassSilentRewrite, measured: measuredCampaign, survives: toStatusAll},
	{"people", "multi_select"}: {class: ClassDestructive, measured: measuredCampaign, survives: "nothing survives, even with an option named after the member"},
	{"people", "date"}:         {class: ClassDestructive, measured: measuredCampaign, survives: nothing},
	{"people", "checkbox"}:     {class: ClassDestructive, measured: measuredCampaign, survives: unchecked},
}

// TypeChangeOf says what going from one type to another costs, given the
// option names the YAML declares on the new type.
//
// A pair missing from the table returns ClassUnknownImpact: the honest answer
// when nobody has tried.
func TypeChangeOf(from, to string, declared []string) TypeChange {
	if from == to {
		return TypeChange{Class: ClassSafe}
	}
	e, measured := typeChangeTable[[2]string{from, to}]
	if !measured {
		return TypeChange{Class: ClassUnknownImpact}
	}
	tc := TypeChange{Class: e.class, Refused: e.refused, Note: e.survives}
	if e.withOptions != nil && len(declared) > 0 {
		tc.Class = *e.withOptions
	}
	if from == "checkbox" {
		yes, no := contains(declared, "Yes"), contains(declared, "No")
		switch {
		case (to == "select" || to == "multi_select") && yes:
			// Checked rows survive as "Yes"; unchecked rows lose nothing
			// they held.
			tc.Class = ClassSafe
		case to == "status" && yes && no:
			tc.Class = ClassSafe
		}
	}
	return tc
}

func contains(names []string, name string) bool {
	for _, n := range names {
		if n == name {
			return true
		}
	}
	return false
}
