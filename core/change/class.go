// SPDX-License-Identifier: GPL-3.0-or-later

// Package change holds the classification of changes: what is safe, what
// requires a migration, what destroys, and what silently rewrites.
//
// It is a leaf package, with no internal dependency, precisely so that
// `resources` can carry a Class on each change line without creating a cycle
// with `diff`, which imports `resources`.
package change

// Class is the category of a change. It describes what it COSTS — safe,
// migration required, destructive, silent rewrite, or unknown impact — it no
// longer decides whether it goes through: notion-seed no longer refuses
// anything on the strength of a class, it measures it and says it.
type Class int

const (
	// ClassSafe: adding a database, a property, a new option.
	ClassSafe Class = iota

	// ClassMigration: same option key, different name. The API cannot rename
	// an option — it accepts the request and returns 200 without changing
	// anything. The option must be created, the rows migrated, then the old
	// one removed.
	ClassMigration

	// ClassDestructive: removing a select or multi_select option, deleting a
	// property, changing a type. The data is lost, but no false value is
	// written.
	//
	// select and multi_select share the class, NOT the behaviour, and the
	// rendering tells them apart: measured on 2026-09-24, a removed select
	// empties the cell, a multi_select only loses that value — ['Un','Deux']
	// minus 'Un' gives ['Deux']. The loss is real on both sides, its extent is
	// not the same.
	ClassDestructive

	// ClassSilentRewrite: removing a status option. The rows that held it are
	// reassigned to ANOTHER option, with no error or warning from the API. The
	// data is not merely lost: it is replaced by a false value,
	// indistinguishable after the fact.
	//
	// "another" and not "the default option": the 2026-09-24 measurement gives
	// À faire → Fait, which is not the group's default option. Which one the
	// API picks has not been measured, so it is not asserted.
	ClassSilentRewrite

	// ClassUnknownImpact: we don't know what this change costs. A pair of
	// types outside the measured table, or a measurement that could not be
	// made (--skip-preflight, failed request).
	//
	// Last in the enumeration DELIBERATELY: WorstClass takes the maximum, and
	// an impact that cannot be named must dominate a resource's header. Not
	// knowing deserves more attention than knowing it is safe.
	ClassUnknownImpact
)

func (c Class) String() string {
	switch c {
	case ClassSafe:
		return "safe"
	case ClassMigration:
		return "migration required"
	case ClassDestructive:
		return "destructive"
	case ClassSilentRewrite:
		return "silent rewrite"
	case ClassUnknownImpact:
		return "unknown impact"
	default:
		return "unknown"
	}
}

// ClassifyOptionRemoval gives the cost of removing an option, according to the
// property's type AND the number of rows that hold it.
//
// count < 0 means "not measured": under --skip-preflight, or when the count
// query failed.
//
// Measured on 2026-09-24 against API 2025-09-03: removing a select option
// empties the row; removing a status option REASSIGNS the row to another
// option, with no error or warning. The first case loses data, the second
// replaces it with a plausible, false value.
//
// The count changes everything: an option nobody uses can be removed at no
// cost, whatever its type. That is what blocking on principle could not see,
// and why it was replaced by a measurement.
func ClassifyOptionRemoval(propertyType string, count int) Class {
	return classifyLoss(count, propertyType == "status")
}

// ClassifyRetypedOptionRemoval gives the cost of an option that disappears
// with a type change, because the YAML does not redeclare it under the same
// name. targetType is the NEW type.
//
// Measured on 2026-09-25 against the API: a row keeps its value only if an
// option with the same name goes in the payload. Otherwise, toward select or
// multi_select it is emptied — a loss, whatever the old type —, and toward
// status it gets the first declared option — a false value.
func ClassifyRetypedOptionRemoval(targetType string, count int) Class {
	return classifyLoss(count, targetType == "status")
}

// classifyLoss is the rule shared by both removals: the count first, the fate
// of the rows second.
func classifyLoss(count int, reassigns bool) Class {
	switch {
	case count < 0:
		return ClassUnknownImpact
	case count == 0:
		return ClassSafe
	case reassigns:
		return ClassSilentRewrite
	default:
		return ClassDestructive
	}
}
