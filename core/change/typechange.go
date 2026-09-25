// SPDX-License-Identifier: GPL-3.0-or-later

package change

import "strconv"

// Count names the rows a type change touches, as a filter on the SOURCE
// column — counting happens before the write.
//
// The zero value is CountNonEmpty, the historical request: the non-empty
// values of the column.
type Count int

const (
	// CountNonEmpty: `is_not_empty` on the source; with Except, the rows whose
	// value is not one of those names.
	CountNonEmpty Count = iota
	// CountChecked: `checkbox equals true`.
	CountChecked
	// CountUnchecked: `checkbox equals false`.
	CountUnchecked
	// CountEmpty: `is_empty` on the source — the rows that will RECEIVE a
	// value they never had.
	CountEmpty
	// CountEveryRow: every row, empty ones included; with Except, every row
	// whose value is not one of those names.
	CountEveryRow
	// CountUnsound: rows are affected, and no filter isolates them. Nothing
	// is sent: a made-up filter would pass a guess off as a measurement.
	CountUnsound
)

// Bound says how a count relates to the rows the change really touches.
type Bound int

const (
	// BoundExact: the filter counts exactly the rows touched.
	BoundExact Bound = iota
	// BoundAtLeast: the filter can miss rows that are touched. A zero count
	// proves nothing, and must never make the change safe.
	BoundAtLeast
	// BoundAtMost: the filter can count rows that survive the conversion.
	BoundAtMost
)

// maxExcept caps the option names a filter excludes. Beyond, the compound
// filter grows past what was ever sent to the API.
const maxExcept = 50

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

	// Count, Bound, Except and Caveat say how to count the rows the change
	// touches. They mean nothing on a safe or refused change, which is not
	// counted.
	Count Count
	Bound Bound
	// Except lists the declared option names whose rows survive: they are
	// excluded from the count.
	Except []string
	// Caveat says why the count is a bound, or why it cannot be taken. The
	// plan shows it next to the figure.
	Caveat string
}

// Caveats, stated once.
const (
	caveatBlank = "text made only of spaces or line breaks is not counted, " +
		"and is lost too"
	caveatCase = "a value that differs from a declared option only by case " +
		"may not be counted"
	caveatParse     = "some values survive the conversion"
	caveatUnchecked = "unchecked rows are emptied too, which loses nothing they held"
	caveatEveryRow  = "every row, empty ones included"
	caveatStatus    = "a row that never received a status reads as the default " +
		"option, is emptied too, and no filter isolates it"
	caveatTextParse = "no filter separates the text that survives the " +
		"conversion from the rest"
	caveatEmpty = "empty rows receive an option; the values whose option is " +
		"not redeclared are counted on their own lines"
	caveatManyOptions = "too many options are declared to build the filter"
)

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
		tc.Count, tc.Caveat = CountChecked, caveatUnchecked
		if to == "status" {
			switch {
			case yes:
				tc.Count, tc.Caveat = CountUnchecked, ""
			case no:
				tc.Count, tc.Caveat = CountChecked, ""
			default:
				tc.Count, tc.Caveat = CountEveryRow, caveatEveryRow
			}
		}
		return tc
	}
	countFor(&tc, from, to, declared)
	return tc
}

// countFor fills in how to count a type change, from the 2026-09-25
// campaign's filters. See the design note: a filter is used only where it is
// sound, with the bound it really gives.
func countFor(tc *TypeChange, from, to string, declared []string) {
	except := exceptFor(from, declared)
	// rich_text: `is_not_empty` misses text made only of spaces or line
	// breaks, which is lost too. Any such count is a lower bound.
	blank := from == "rich_text"

	switch {
	case to == "status":
		switch from {
		case "select":
			tc.Count, tc.Caveat = CountEmpty, caveatEmpty
		case "multi_select":
			tc.Count, tc.Bound, tc.Caveat = CountEveryRow, BoundAtMost,
				"a row holding a single value whose option is declared keeps it"
		case "rich_text", "url", "number":
			tc.Count, tc.Except, tc.Caveat = CountEveryRow, except, caveatEveryRow
			// is_empty counts blank text: only case can escape the filter.
			if len(except) > 0 && from != "number" {
				tc.Bound, tc.Caveat = BoundAtLeast, caveatEveryRow+"; "+caveatCase
			}
		default:
			tc.Count, tc.Caveat = CountEveryRow, caveatEveryRow
		}
	case from == "status" && to != "number" && to != "date" && to != "checkbox" && to != "people":
		tc.Count, tc.Caveat = CountUnsound, caveatStatus
	case from == "rich_text" && (to == "number" || to == "date"):
		tc.Count, tc.Caveat = CountUnsound, caveatTextParse
	case to == "number" || to == "date":
		if from == "url" || from == "select" || from == "multi_select" {
			tc.Bound, tc.Caveat = BoundAtMost, caveatParse
		}
	case to == "select" || to == "multi_select":
		switch from {
		case "multi_select":
			tc.Bound, tc.Caveat = BoundAtMost, "rows holding a single value keep it"
		case "rich_text", "url", "number":
			tc.Except = except
			if len(except) > 0 && from != "number" {
				tc.Bound, tc.Caveat = BoundAtLeast, caveatCase
			}
		}
	}
	if blank && tc.Count == CountNonEmpty {
		tc.Bound = BoundAtLeast
		if tc.Caveat == "" {
			tc.Caveat = caveatBlank
		} else {
			tc.Caveat = caveatBlank + "; " + tc.Caveat
		}
	}
	if len(tc.Except) > maxExcept {
		tc.Count, tc.Except, tc.Caveat = CountUnsound, nil, caveatManyOptions
	}
}

// exceptFor keeps the declared names a value of the source type can match.
// A number converts to its shortest decimal writing ('7', '-3.5', '1000000'),
// so only a name written that way can hold it.
func exceptFor(from string, declared []string) []string {
	if from != "number" {
		return declared
	}
	var out []string
	for _, n := range declared {
		if v, err := strconv.ParseFloat(n, 64); err == nil &&
			strconv.FormatFloat(v, 'f', -1, 64) == n {
			out = append(out, n)
		}
	}
	return out
}

func contains(names []string, name string) bool {
	for _, n := range names {
		if n == name {
			return true
		}
	}
	return false
}
