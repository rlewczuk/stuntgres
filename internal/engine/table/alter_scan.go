package table

import (
	"slices"
	"strings"

	"stuntgres/internal/engine/catalog"
)

// PlanAlterScan decides whether ALTER TABLE must scan the rows of a table
// with physical rows, and returns the row validator for that scan. A rewrite
// already checked NOT NULL and the type-changed CHECKs while copying, so it
// scans only for added CHECKs; otherwise the type-changed and added CHECKs,
// newly valid NOT NULL constraints and NOT NULL virtual columns whose
// generation was replaced need a scan. notNull are the NOT NULL slots of the
// statement's row validator. A validator with CHECKs needs its CHECK
// snapshots captured before it runs.
func PlanAlterScan(old, next *catalog.Relation, schema string, rewrite, physicalRows bool, typeChecks, addedChecks []catalog.CheckConstraint, replacements []int32, notNull []int) (AlterRowValidator, bool) {
	scanChecks := addedChecks
	if !rewrite {
		scanChecks = append(typeChecks, addedChecks...)
	}
	virtualNotNullScan := false
	if !rewrite {
		for _, attribute := range replacements {
			if next.ColumnMeta.Generations[attribute].Kind == catalog.GenerationVirtual && slices.Contains(notNull, next.IndexForAttribute(attribute)) {
				virtualNotNullScan = true
				break
			}
		}
	}
	if !physicalRows || len(scanChecks) == 0 && (rewrite || !NeedsNotNullScan(old, next) && !virtualNotNullScan) {
		return AlterRowValidator{}, false
	}
	validator := NewAlterRowValidator(next, schema)
	if rewrite {
		validator.NotNull = nil
	}
	if len(scanChecks) != 0 {
		traversal := *next
		traversal.Constraints.Check = scanChecks
		validator.Checks = &traversal
	}
	return validator, true
}

// ResolveAlterChecks preserves the scheduled traversal while taking identity,
// compiled expression and provenance from the authentic final descriptor.
func ResolveAlterChecks(next *catalog.Relation, scheduled []catalog.CheckConstraint) ([]catalog.CheckConstraint, error) {
	if len(scheduled) == 0 {
		return scheduled, nil
	}
	resolved := make([]catalog.CheckConstraint, len(scheduled))
	for i, check := range scheduled {
		slot, found := slices.BinarySearchFunc(next.Constraints.Check, check.Name, func(candidate catalog.CheckConstraint, name string) int {
			return strings.Compare(candidate.Name, name)
		})
		if !found || next.Constraints.Check[slot].Ordinal != check.Ordinal || next.Constraints.Check[slot].OID == 0 {
			return nil, catalog.Conflict{}
		}
		resolved[i] = next.Constraints.Check[slot]
	}
	return resolved, nil
}
