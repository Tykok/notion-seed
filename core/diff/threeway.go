// SPDX-License-Identifier: GPL-3.0-or-later

package diff

import (
	"fmt"
	"sort"
	"strings"

	"github.com/tykok/notion-seed/core/change"
	"github.com/tykok/notion-seed/core/providers/notion/resources"
	"github.com/tykok/notion-seed/core/state"
)

// Result is the comparator's output for ONE resource.
//
// The three fields answer three distinct questions, and mixing them up would
// be the mistake not to make: Changeset says what would be written, Drift
// says what someone did by hand, Unmanaged says what exists without being
// declared and will not be touched.
type Result struct {
	Changeset resources.Changeset
	Drift     []string
	Unmanaged []string

	// Target is the EXACT state the resource will have after the write, on the
	// managed surface only. It is the single source: Render derives its lines
	// from it, mapper its payload, state what it records. No path from the
	// configuration to the API goes around it, and that is what makes an
	// unannounced write impossible — rather than corrected.
	//
	// Target says WHAT GETS WRITTEN, and only makes sense for a creation and an
	// update: the two paths where a state exists after the write. For an
	// update, it is `actual` with ONLY the declared changes applied — which
	// preserves unmanaged properties, and which makes the target carry the
	// remote option ids.
	//
	// Target does NOT say whether writing is allowed: Withheld == "" allows
	// it. A destruction has no state afterwards, hence never a target; it is
	// allowed by an empty Withheld like the rest, and apply takes the identity
	// to move to the trash from the state. A nil target therefore allows no
	// conclusion about the permission.
	Target *state.Database

	// Withheld says WHY this resource will not be written, or "" if it can be.
	// Withheld == "" IS the permission to write, for all three kinds of change:
	// creation, update, destruction. `Target` says what gets written,
	// `Withheld` says whether it is allowed.
	//
	// A reason rather than a boolean: a resource skipped with no reason leaves
	// the user guessing, and notion-seed never leaves anyone guessing.
	Withheld string
}

// CompareDatabase compares the three ways of a database.
//
// A nil pointer means "absent from this way":
//   - desired nil, applied non-nil  → orphan resource, destruction planned
//   - applied nil                   → never applied, hence a creation
//   - actual nil with applied non-nil → the caller could not read it; this case
//     is handled by Compute, not here, because the reason (404, archived) comes
//     from the transport.
//
// The function is PURE: no network, no file, no clock. That is what allows
// covering it with a table of triples, and that is where all of the product's
// safety lives.
func CompareDatabase(key string, desired, applied, actual *state.Database) Result {
	res := Result{Changeset: resources.Changeset{Resource: "database." + key}}

	switch {
	case desired == nil && applied == nil:
		return res

	case desired == nil && actual == nil:
		// No longer in the config, and the actual state does not hold it — or
		// was not read. The two situations are told apart by the reason of the
		// refresh, which only Compute knows: so it decides between "stale state
		// entry" and "not compared". Concluding a destruction here would offer
		// to destroy what already no longer exists.
		return res

	case desired == nil:
		// In the state, STILL in Notion, no longer in the config: the identity
		// no longer has a declared anchor. Stricter than the rule for unmanaged
		// properties, and on purpose — keeping a "managed but not declared" id
		// would make it invisible.
		res.Changeset.Kind = resources.KindDestroy
		//
		// The class stays destructive whatever the count: it says what goes to
		// the trash with the database, not whether it goes.
		d := resources.NewDetail("-", "database."+key, change.ClassDestructive)
		d.Note = "present in the state, absent from the configuration"
		d.Measure = &resources.Measurement{AllRows: true}
		res.Changeset.Details = []resources.Detail{d}
		return res

	case actual == nil && applied != nil:
		// The state anchors it, but the actual state was not read — that is the
		// case of --skip-preflight, which makes no call. We know nothing, so we
		// say nothing: announcing a creation here would offer to re-create an
		// already imported database.
		return res

	case actual == nil:
		// Never applied: full creation, no API call took place.
		res.Changeset.Kind = resources.KindCreate
		target := *desired
		res.Target = &target
		res.Changeset.Details = createLines(&target)
		return res
	}

	// All three ways exist: fine-grained diff.
	res.Drift = driftLines(applied, actual)
	res.Changeset.Details = planLines(desired, applied, actual)
	res.Unmanaged = unmanagedLines(desired, actual)

	if len(res.Changeset.Details) > 0 {
		res.Changeset.Kind = resources.KindUpdate
		if res.Withheld = withheldReason(res.Changeset.Details); res.Withheld == "" {
			t := updateTarget(desired, applied, actual)
			res.Target = &t
		}
	}
	return res
}

// withheldReason says why a resource cannot be written, or "" if it can be.
//
// Two causes, both measured: a `migration required` line is not expressible
// in the API. An option rename returns 200 without changing anything; an
// option color returns 400 and fails the whole PATCH — and writing the first
// would record in the state a name Notion does not hold, so every following
// run would show phantom drift. The type of a title property cannot change
// either way: 400, measured on 2026-09-25.
//
// It is NOT a safety refusal: notion-seed refuses nothing on the strength of a
// class, it measures and it says. It is a limit of the API, named as such,
// with its procedure.
func withheldReason(ds []resources.Detail) string {
	var option, title bool
	for _, d := range ds {
		if d.Class != change.ClassMigration {
			continue
		}
		// Two inexpressible changes carry the class, and they are not fixed
		// the same way: a refused type change is on a property line, an option
		// migration on an option line.
		if strings.HasPrefix(d.Target, "property ") {
			title = true
		} else {
			option = true
		}
	}
	var reasons []string
	if option {
		reasons = append(reasons, "an option must be migrated by hand: the API can neither rename "+
			"an option nor change its color\n"+
			"  → create the new option in Notion, move the rows "+
			"counted above to it, remove the old one, then rerun")
	}
	if title {
		reasons = append(reasons, "the API refuses to change the type of a title property, "+
			"in either direction\n"+
			"  → add a new property of the wanted type, copy the values into it "+
			"in Notion, then remove the type change from the YAML and rerun")
	}
	return strings.Join(reasons, "\n")
}

// updateTarget resolves the exact state the database will have after the
// write: it is `actual` with ONLY the declared changes applied.
//
// Starting from `desired` — what the version that did not write did — would
// make everything the YAML does not declare disappear. Starting from `actual`
// is what gives its meaning to "undeclared property = untouched", and it is
// also what makes the target carry the remote option ids, without which the
// first PATCH would destroy every option it thinks it updates.
func updateTarget(desired, applied, actual *state.Database) state.Database {
	target := state.Database{
		ID:           actual.ID,
		DataSourceID: actual.DataSourceID,
		Name:         actual.Name,
		Description:  actual.Description,
		Icon:         actual.Icon,
		Properties:   make(map[string]state.Property, len(desired.Properties)),
	}
	// Same non-emptiness guard as in planLines: what the YAML does not declare
	// is not written, so it does not enter the target.
	if desired.Name != "" {
		target.Name = desired.Name
	}
	if desired.Description != "" {
		target.Description = desired.Description
	}
	if desired.Icon != "" {
		target.Icon = desired.Icon
	}

	for name, want := range desired.Properties {
		have, exists := actual.Properties[name]
		if !exists {
			// New property: the YAML value, all options new.
			target.Properties[name] = newProperty(want)
			continue
		}
		if want.Type != have.Type {
			// Measured on 2026-09-24: a type change re-creates the options and
			// ignores the ids sent. planLines announces these same options, all
			// as `+`, from the same newProperty: the two stay aligned. The
			// current options the YAML does not redeclare under the same name
			// are not sent: planLines announces them as `-`, with their count.
			p := newProperty(want)
			p.ID = have.ID
			target.Properties[name] = p
			continue
		}

		p := state.Property{ID: have.ID, Type: have.Type, Format: have.Format}
		if want.Format != "" {
			p.Format = want.Format
		}
		pr := pairOptions(want, have, applied.Properties[name])
		for wi, w := range want.Options {
			o := state.Option{Key: w.Key, Name: w.Name, Color: w.Color, Group: w.Group}
			if i := pr.At[wi]; i != -1 {
				remote := have.Options[i]
				o.ID = remote.ID
				// The name and color of an existing option are not writable: a
				// diverging name returns 200 with no effect, a diverging color
				// returns 400. Both cases withhold the whole resource, so the
				// target is not used — but it must stay TRUE rather than promise
				// an impossible write.
				o.Name = remote.Name
				o.Color = remote.Color
				if o.Group == "" {
					o.Group = remote.Group
				}
			}
			p.Options = append(p.Options, o)
		}
		target.Properties[name] = p
	}
	return target
}

// newProperty returns the target value of a property none of whose options can
// inherit a remote identity: a new property, or a property whose type changes.
// No option carries an id.
func newProperty(want state.Property) state.Property {
	out := state.Property{Type: want.Type, Format: want.Format}
	for _, o := range want.Options {
		out.Options = append(out.Options, state.Option{
			Key: o.Key, Name: o.Name, Color: o.Color, Group: o.Group,
		})
	}
	return out
}

// createLines details a creation from the resolved target: properties AND
// options, with their color and group.
//
// The options are listed because the creation WRITES them. Omitting them —
// what the previous version did — let apply set colors and groups the plan had
// never shown. It is the same flaw, at creation, as the group substituted by
// the mapper.
func createLines(target *state.Database) []resources.Detail {
	var out []resources.Detail

	// The name, description and icon ALSO go in the creation payload.
	// Omitting them here would be exactly the flaw the options had: written,
	// never shown. The same non-emptiness guard as elsewhere applies — what the
	// YAML does not declare is not written, so it is not announced.
	for _, f := range []struct{ field, value string }{
		{"name", target.Name},
		{"description", target.Description},
		{"icon", target.Icon},
	} {
		if f.value == "" {
			continue
		}
		d := resources.NewDetail("+",
			fmt.Sprintf("%s %q", f.field, f.value), change.ClassSafe)
		d.Field = f.field
		out = append(out, d)
	}

	for _, name := range sortedPropNames(target.Properties) {
		p := target.Properties[name]
		d := resources.NewDetail("+",
			fmt.Sprintf("property %q (%s)", name, p.Type), change.ClassSafe)
		d.Property = name
		out = append(out, d)
		out = append(out, newOptionLines(name, p.Options)...)
	}
	return out
}

// newOptionLines announces the options of a property written with no remote
// identity at all: at creation, under a new property, under a type change, or
// when a declared option has no pairing. All are sent, with their color and
// group: hiding them would be writing what the plan never showed.
//
// The order of the options is the YAML's: it is visible in Notion, sorting it
// would make it wrong.
func newOptionLines(propName string, opts []state.Option) []resources.Detail {
	var out []resources.Detail
	for _, o := range opts {
		d := resources.NewDetail("+",
			fmt.Sprintf("option %q (property %q)", o.Name, propName), change.ClassSafe)
		d.Property = propName
		d.Note = optionAttrNote(o)
		out = append(out, d)
	}
	return out
}

// optionAttrNote returns the declared attributes of an option, in a fixed
// order. Empty if the YAML declares none: an empty note is better than a note
// announcing a value notion-seed will not write.
func optionAttrNote(o state.Option) string {
	var parts []string
	if o.Color != "" {
		parts = append(parts, "color "+o.Color)
	}
	if o.Group != "" {
		parts = append(parts, "group "+o.Group)
	}
	return strings.Join(parts, ", ")
}

// planLines produces what would be written to bring `actual` to `desired`.
func planLines(desired, applied, actual *state.Database) []resources.Detail {
	var out []resources.Detail

	if desired.Name != "" && desired.Name != actual.Name {
		d := resources.NewDetail("~", "name", change.ClassSafe)
		d.Field = "name"
		d.Note = fmt.Sprintf("%q → %q", actual.Name, desired.Name)
		out = append(out, d)
	}
	if desired.Description != "" && desired.Description != actual.Description {
		d := resources.NewDetail("~", "description", change.ClassSafe)
		d.Field = "description"
		d.Note = fmt.Sprintf("%q → %q", actual.Description, desired.Description)
		out = append(out, d)
	}
	if desired.Icon != "" && desired.Icon != actual.Icon {
		d := resources.NewDetail("~", "icon", change.ClassSafe)
		d.Field = "icon"
		d.Note = fmt.Sprintf("%q → %q", actual.Icon, desired.Icon)
		out = append(out, d)
	}

	for _, name := range sortedPropNames(desired.Properties) {
		want := desired.Properties[name]
		have, exists := actual.Properties[name]

		if !exists {
			note := ""
			// A name gone from the YAML while another of the same type appears:
			// most likely a rename. Nothing is DECIDED on that basis (an
			// unmanaged property is never touched), the user is warned, because
			// they think they renamed and will find an empty column.
			if old := renamedFrom(name, want, desired, applied, actual); old != "" {
				note = fmt.Sprintf(
					"%q is not renamed, it stays unmanaged with its data", old)
			}
			d := resources.NewDetail("+",
				fmt.Sprintf("property %q (%s)", name, want.Type), change.ClassSafe)
			d.Property = name
			d.Note = note
			out = append(out, d)
			// Same options as the ones the target writes: newProperty is the
			// single source of both.
			out = append(out, newOptionLines(name, newProperty(want).Options)...)
			continue
		}

		if want.Type != have.Type {
			out = append(out, typeChangeLines(name, want, have)...)
			continue
		}
		if want.Type == "number" && want.Format != "" && want.Format != have.Format {
			d := resources.NewDetail("~", fmt.Sprintf("property %q", name), change.ClassSafe)
			d.Property = name
			d.Note = fmt.Sprintf("format %s → %s", have.Format, want.Format)
			out = append(out, d)
		}
		out = append(out, optionLines(name, want, have, applied.Properties[name])...)
	}
	return out
}

// typeChangeLines announces a property type change: the line itself, with
// what the measured table says survives, then the options the write re-creates
// and those it drops.
func typeChangeLines(name string, want, have state.Property) []resources.Detail {
	var declared []string
	for _, o := range want.Options {
		declared = append(declared, o.Name)
	}
	tc := change.TypeChangeOf(have.Type, want.Type, declared)
	if have.Type == "multi_select" && hasOptions(want.Type) {
		tc = tc.WithoutRowsHolding(undeclaredOptions(want, have))
	}
	d := resources.NewDetail("~", fmt.Sprintf("property %q", name), tc.Class)
	d.Property = name
	d.FromType = have.Type
	d.Note = fmt.Sprintf("%s → %s", have.Type, want.Type)
	if tc.Note != "" {
		d.Note += ": " + tc.Note
	}
	// Refused by the API (title, measured on 2026-09-25): nothing will be
	// written, so there is nothing to count and no option to announce. The
	// migration class withholds the resource; withheldReason names the way
	// out.
	if tc.Refused {
		return []resources.Detail{d}
	}
	// A pair the table says is SAFE requires no measurement: the count would
	// change neither its class nor the decision, and notion-seed would pay an
	// API call for a number that says nothing. Not paying for calls for
	// nothing is a property of the product, not an optimization.
	//
	// Safe for the values whose name comes back, not for the others: those
	// are announced and measured one by one by retypedRemovalLines, one
	// removal line per option.
	if tc.Class != change.ClassSafe {
		d.Measure = &resources.Measurement{
			Property:     name,
			PropertyType: have.Type,
			TargetType:   want.Type,
			Count:        tc.Count,
			Bound:        tc.Bound,
			Except:       tc.Except,
			Caveat:       tc.Caveat,
		}
	}
	out := []resources.Detail{d}
	// A type change re-creates the options: all the YAML's go out new, with
	// no id, exactly as under a new property.
	out = append(out, newOptionLines(name, newProperty(want).Options)...)
	return append(out, retypedRemovalLines(name, want, have)...)
}

// pairing is the result of pairing the declared options with the remote
// options. Only one copy of this rule exists: optionLines derives the plan
// lines from it, updateTarget the written target. Two diverging identity rules
// would write an option the plan would have shown elsewhere.
type pairing struct {
	// At gives, for each declared option, the position of the paired remote
	// option, or -1.
	At []int
	// ViaKey says the pairing went through the config key. It separates the
	// two passes of optionLines, whose output order is visible.
	ViaKey []bool
	// Claimed says which remote positions were taken. The others will be
	// destroyed by the write: the API replaces the whole list.
	Claimed []bool
}

// pairOptions follows the identity chain: applied ↔ actual by id, desired ↔
// applied by key, failing that by name.
//
// Claiming is done by POSITION in have.Options, not by name: a rename frees
// its old name, and claiming by name would think that name still taken. The
// index is also robust to an empty id, which a hand-written state can carry.
func pairOptions(want, have, applied state.Property) pairing {
	p := pairing{
		At:      make([]int, len(want.Options)),
		ViaKey:  make([]bool, len(want.Options)),
		Claimed: make([]bool, len(have.Options)),
	}
	for i := range p.At {
		p.At[i] = -1
	}

	idxByID := make(map[string]int, len(have.Options))
	idxByName := make(map[string]int, len(have.Options))
	for i, o := range have.Options {
		if o.ID != "" {
			idxByID[o.ID] = i
		}
		if _, dup := idxByName[o.Name]; !dup {
			idxByName[o.Name] = i
		}
	}

	idxByKey := make(map[string]int)
	for _, o := range applied.Options {
		if o.Key == "" || o.ID == "" {
			continue
		}
		if i, ok := idxByID[o.ID]; ok {
			idxByKey[o.Key] = i
		}
	}

	// Pass 1: the options with a key, which have a stable identity.
	for wi, w := range want.Options {
		if w.Key == "" {
			continue
		}
		i, ok := idxByKey[w.Key]
		if !ok {
			continue
		}
		p.At[wi], p.ViaKey[wi], p.Claimed[i] = i, true, true
	}

	// Pass 2: the others, paired by name — but only on a position pass 1 has not
	// already taken.
	for wi, w := range want.Options {
		if p.At[wi] != -1 {
			continue
		}
		if i, ok := idxByName[w.Name]; ok && !p.Claimed[i] {
			p.At[wi], p.Claimed[i] = i, true
		}
	}
	return p
}

// optionLines compares the options of a property.
//
// The pairing follows the identity chain: applied ↔ actual by Notion id,
// desired ↔ applied by key if declared, otherwise by name. It is what makes a
// rename visible rather than passing it off as a removal followed by an
// addition — and removing a status option silently reassigns the rows.
func optionLines(propName string, want, have, applied state.Property) []resources.Detail {
	if len(want.Options) == 0 && len(have.Options) == 0 {
		return nil
	}
	p := pairOptions(want, have, applied)

	var out []resources.Detail

	// Pass 1: the options whose key resolved.
	for wi, w := range want.Options {
		if !p.ViaKey[wi] {
			continue
		}
		current := have.Options[p.At[wi]]
		if current.Name != w.Name {
			d := resources.NewDetail("~",
				fmt.Sprintf("option %q → %q (property %q)", current.Name, w.Name, propName),
				change.ClassMigration)
			d.Property = propName
			d.Note = "the API returns 200 without changing anything: create, migrate the rows, then remove"
			// The fix goes through removing the old option: its cost is the
			// number of rows holding it, and it is the CURRENT name that can
			// filter them.
			d.Measure = &resources.Measurement{
				Property: propName, PropertyType: have.Type, Option: current.Name,
			}
			out = append(out, d)
			continue
		}
		// The name matches: no migration pending on this line, so there is room
		// to compare color and group. On a migration, the line already carries
		// its own change; stacking a second mismatch on it would sow confusion
		// without adding anything.
		out = append(out, optionAttrLines(propName, have.Type, w, current)...)
	}

	// Pass 2: the options without a key, or whose key did not resolve.
	for wi, w := range want.Options {
		if p.ViaKey[wi] {
			continue
		}
		if i := p.At[wi]; i != -1 {
			out = append(out, optionAttrLines(propName, have.Type, w, have.Options[i])...)
			continue
		}
		// An option with no pairing goes out new, with its color and group: the
		// same line as at creation, from the same function.
		out = append(out, newOptionLines(propName, []state.Option{w})...)
	}

	// Pass 3: what the YAML does not claim WILL be destroyed as soon as this
	// property is written — the API replaces the whole list instead of merging
	// it.
	for i, o := range have.Options {
		if p.Claimed[i] {
			continue
		}
		out = append(out, removalLine(propName, have.Type, o.Name, false))
	}
	return out
}

// retypedRemovalLines announces the options a type change makes disappear.
// Measured on 2026-09-25 on select → multi_select: the API re-creates the
// options, and a row keeps its value only if an option with the SAME NAME goes
// in the payload. The others disappear from the schema, and their rows are
// emptied. The pairing is therefore by name alone: neither key nor id
// survives the re-creation.
//
// The same rule holds for every pair between option types (status ↔ select,
// status ↔ multi_select, multi_select → select), without having been measured
// there. Towards a type without options, there is no name to find: the count
// of non-empty values on the property line alone says what is at stake.
func retypedRemovalLines(propName string, want, have state.Property) []resources.Detail {
	if !hasOptions(want.Type) {
		return nil
	}
	var out []resources.Detail
	for _, name := range undeclaredOptions(want, have) {
		d := removalLine(propName, have.Type, name, true)
		d.Measure.TargetType = want.Type
		// From multi_select toward a single value, only the FIRST value is
		// kept (measured on 2026-09-24): a row [B, A] with B not redeclared is
		// counted here, and loses A too, which no line counts. The count is
		// exact for B, a lower bound of what the row loses — and the total
		// must say so.
		if have.Type == "multi_select" && want.Type != "multi_select" {
			d.Measure.Bound = change.BoundAtLeast
		}
		out = append(out, d)
	}
	return out
}

// undeclaredOptions lists the current options the YAML does not redeclare
// under the same name, in their current order. Under a type change they
// disappear: the pairing is by name alone.
func undeclaredOptions(want, have state.Property) []string {
	declared := make(map[string]bool, len(want.Options))
	for _, o := range want.Options {
		declared[o.Name] = true
	}
	var out []string
	for _, o := range have.Options {
		if !declared[o.Name] {
			out = append(out, o.Name)
		}
	}
	return out
}

// removalLine announces an option the write will destroy. propType is the
// property's CURRENT type: it is what filters the rows to count.
func removalLine(propName, propType, option string, retyped bool) resources.Detail {
	// Class and count come from the measurement. Before it, we don't know: -1
	// says "not measured", and the classification turns it into an unknown
	// impact rather than "safe".
	class := change.ClassifyOptionRemoval(propType, -1)
	note := "absent from the YAML: the API replaces the whole list of options"
	if retyped {
		// Not measured yet: -1 gives unknown impact whatever the new type.
		class = change.ClassifyRetypedOptionRemoval("", -1)
		// A key kept under another name saves nothing here: "absent from the
		// YAML" would be wrong, it is the name that is missing.
		note = "not redeclared under this name: the type change re-creates the options"
	}
	return resources.Detail{
		Op:       "-",
		Target:   fmt.Sprintf("option %q (property %q)", option, propName),
		Property: propName,
		Note:     note,
		Class:    class,
		Count:    -1,
		Measure: &resources.Measurement{
			Property: propName, PropertyType: propType, Option: option, Retyped: retyped,
		},
	}
}

// hasOptions says whether a property type holds a list of options.
func hasOptions(t string) bool {
	return t == "select" || t == "status" || t == "multi_select"
}

// optionAttrLines compares color and group of a paired option whose name
// already matches. The two attributes do NOT behave the same, and it is
// measured:
//
//   - color is IMMUTABLE. The API returns 400 — "Cannot update color of select
//     with id" — by id as by name, and the failure covers the property's whole
//     PATCH. The change is therefore not expressible: an option must be
//     created, the rows migrated, the old one removed. Hence ClassMigration,
//     and a measurement, since this fix costs as many rows as the option holds.
//   - group is MUTABLE by id, and survives even when omitted. Safe class, like
//     the number format.
//
// Compares only what the YAML declares: a color or group absent from the YAML
// must never produce a phantom change.
func optionAttrLines(propName, propType string, want, have state.Option) []resources.Detail {
	var out []resources.Detail
	target := fmt.Sprintf("option %q (property %q)", want.Name, propName)
	if want.Color != "" && want.Color != have.Color {
		d := resources.NewDetail("~", target, change.ClassMigration)
		d.Property = propName
		d.Note = fmt.Sprintf(
			"color %s → %s: an option's color is immutable, the API returns 400. "+
				"Create an option, migrate the rows, then remove the old one",
			have.Color, want.Color)
		d.Measure = &resources.Measurement{
			Property:     propName,
			PropertyType: propType,
			Option:       have.Name,
		}
		out = append(out, d)
	}
	if want.Group != "" && want.Group != have.Group {
		d := resources.NewDetail("~", target, change.ClassSafe)
		d.Property = propName
		d.Note = fmt.Sprintf("group %s → %s", have.Group, want.Group)
		out = append(out, d)
	}
	return out
}

// driftLines says what moved in Notion since the last apply. It is an
// observation, never an action: the reconciliation towards the YAML is
// computed separately by planLines.
func driftLines(applied, actual *state.Database) []string {
	var out []string
	if applied.Name != "" && applied.Name != actual.Name {
		out = append(out, fmt.Sprintf("~ name %q → %q", applied.Name, actual.Name))
	}
	// Same non-emptiness guard as for the name: a state that never captured
	// the description must not pass its actual value off as drift on every
	// run.
	if applied.Description != "" && applied.Description != actual.Description {
		out = append(out, fmt.Sprintf(
			"~ description %q → %q outside notion-seed", applied.Description, actual.Description))
	}
	// Same non-emptiness guard: a state that never captured the icon must not
	// pass its actual value off as drift on every run.
	if applied.Icon != "" && applied.Icon != actual.Icon {
		out = append(out, fmt.Sprintf(
			"~ icon %q → %q outside notion-seed", applied.Icon, actual.Icon))
	}

	for _, name := range sortedPropNames(applied.Properties) {
		was := applied.Properties[name]
		is, exists := actual.Properties[name]
		if !exists {
			out = append(out, fmt.Sprintf("- property %q deleted outside notion-seed", name))
			continue
		}
		if was.Type != is.Type {
			out = append(out, fmt.Sprintf(
				"~ property %q: type %s → %s outside notion-seed", name, was.Type, is.Type))
		}
		if was.Type == "number" && was.Format != "" && was.Format != is.Format {
			out = append(out, fmt.Sprintf(
				"~ property %q: format %s → %s outside notion-seed", name, was.Format, is.Format))
		}

		// An option without an id cannot be tracked by id: neither removal, nor
		// rename, nor attribute change can be asserted for it, so no drift line
		// is returned about it.
		byID := map[string]state.Option{}
		for _, o := range is.Options {
			if o.ID == "" {
				continue
			}
			byID[o.ID] = o
		}
		wasIDs := map[string]bool{}
		for _, o := range was.Options {
			if o.ID == "" {
				continue
			}
			wasIDs[o.ID] = true
			current, ok := byID[o.ID]
			if !ok {
				out = append(out, fmt.Sprintf(
					"- option %q of property %q removed outside notion-seed", o.Name, name))
				continue
			}
			if current.Name != o.Name {
				out = append(out, fmt.Sprintf(
					"~ option %q of property %q renamed to %q outside notion-seed",
					o.Name, name, current.Name))
			}
			// Same non-emptiness guard as elsewhere: a color or group the state
			// never captured must not pass its actual value off as drift.
			if o.Color != "" && current.Color != o.Color {
				out = append(out, fmt.Sprintf(
					"~ option %q of property %q: color %s → %s outside notion-seed",
					o.Name, name, o.Color, current.Color))
			}
			if o.Group != "" && current.Group != o.Group {
				out = append(out, fmt.Sprintf(
					"~ option %q of property %q: group %s → %s outside notion-seed",
					o.Name, name, o.Group, current.Group))
			}
		}

		// Reverse walk: what was added by hand appears in no option of
		// `applied`.
		for _, o := range is.Options {
			if o.ID == "" || wasIDs[o.ID] {
				continue
			}
			out = append(out, fmt.Sprintf(
				"+ option %q of property %q added outside notion-seed", o.Name, name))
		}
	}

	// Reverse walk at the property level: what was added by hand does not
	// appear in `applied`.
	for _, name := range sortedPropNames(actual.Properties) {
		if _, declared := applied.Properties[name]; declared {
			continue
		}
		out = append(out, fmt.Sprintf("+ property %q added outside notion-seed", name))
	}

	return out
}

// unmanagedLines lists what exists in Notion without being declared. Left
// untouched: the API updates properties one by one, so not declaring them is
// enough not to touch them. (This reasoning does NOT hold for options: see
// optionLines.)
func unmanagedLines(desired, actual *state.Database) []string {
	var out []string
	for _, name := range sortedPropNames(actual.Properties) {
		if _, declared := desired.Properties[name]; declared {
			continue
		}
		out = append(out, fmt.Sprintf(
			"property %q (%s)", name, actual.Properties[name].Type))
	}
	return out
}

// renamedFrom looks for the state's property, of the same type, that the YAML
// no longer declares but that still exists in the actual state: the sign of a
// rename. A DISPLAY heuristic, never a write decision. If several candidates
// exist, it names none: a heuristic that picks the wrong column is worse than
// no heuristic.
func renamedFrom(newName string, want state.Property, desired, applied, actual *state.Database) string {
	found := ""
	for _, old := range sortedPropNames(applied.Properties) {
		if old == newName {
			continue
		}
		if _, stillDeclared := desired.Properties[old]; stillDeclared {
			continue
		}
		if _, stillInActual := actual.Properties[old]; !stillInActual {
			continue
		}
		if applied.Properties[old].Type != want.Type {
			continue
		}
		if found != "" {
			return ""
		}
		found = old
	}
	return found
}

func sortedPropNames(m map[string]state.Property) []string {
	out := make([]string, 0, len(m))
	for name := range m {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// WorstClass returns the most severe class of a set of details. The
// enumeration goes from safest to most severe, so the maximum is enough.
//
// Exported because the measurement pass, which reclassifies the details AFTER
// Compute, must recompute a resource's header class from another package. A
// single copy: duplicating it would let two severity rules diverge.
func WorstClass(ds []resources.Detail) change.Class {
	worst := change.ClassSafe
	for _, d := range ds {
		if d.Class > worst {
			worst = d.Class
		}
	}
	return worst
}
