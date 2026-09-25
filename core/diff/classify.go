// SPDX-License-Identifier: GPL-3.0-or-later

// Package diff computes the change plan and renders it as text.
package diff

import "github.com/tykok/notion-seed/core/change"

// The classification lives in core/change, a leaf package shared with
// resources. These aliases keep `diff.ClassSafe` and friends valid: callers do
// not need to know where the type moved.
type Class = change.Class

const (
	ClassSafe          = change.ClassSafe
	ClassMigration     = change.ClassMigration
	ClassDestructive   = change.ClassDestructive
	ClassSilentRewrite = change.ClassSilentRewrite
	ClassUnknownImpact = change.ClassUnknownImpact
)
