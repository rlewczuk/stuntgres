package table

import (
	"slices"
	"strings"

	"stuntgres/internal/engine/catalog"
)

// PlanAlterScan decides whether ALTER TABLE must scan the rows of a table
// with physical rows, and returns the row validator for that scan. A rewrite
// already checked ordinary NOT NULL and type-changed CHECKs while copying.
// Explicit validation remains selected even after a rewrite; other scans cover
// added/type-changed CHECKs, newly valid NOT NULL and replaced nonnullable virtual
// generations. notNull are the independently required row-validator slots.
// A validator with CHECKs needs its CHECK snapshots captured before it runs.
func PlanAlterScan(old, next *catalog.Relation, schema string, rewrite, physicalRows bool, typeChecks, addedChecks, validatedChecks []catalog.CheckConstraint, validatedNotNull []int32, replacements []int32, notNull []int) (AlterRowValidator, bool) {
	scanChecks := addedChecks
	if !rewrite {
		scanChecks = append(typeChecks, addedChecks...)
	}
	if len(validatedChecks) != 0 {
		scanChecks = append(slices.Clone(scanChecks), validatedChecks...)
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
	if !physicalRows || len(scanChecks) == 0 && len(validatedNotNull) == 0 && (rewrite || !NeedsNotNullScan(old, next) && !virtualNotNullScan) {
		return AlterRowValidator{}, false
	}
	validator := NewAlterRowValidator(next, schema)
	validator.NotNull = nil
	if !rewrite {
		for _, slot := range notNull {
			attribute := next.Attributes[slot]
			previouslyValid := slices.ContainsFunc(old.Constraints.NotNull, func(c catalog.NotNullConstraint) bool { return c.Attribute == attribute && !c.NotValid })
			if !previouslyValid || slices.Contains(replacements, attribute) {
				validator.NotNull = append(validator.NotNull, slot)
			}
		}
	}
	for _, attribute := range validatedNotNull {
		slot := next.IndexForAttribute(attribute)
		if slot >= 0 && !slices.Contains(validator.NotNull, slot) {
			validator.NotNull = append(validator.NotNull, slot)
		}
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
		if !found || next.Constraints.Check[slot].Ordinal != check.Ordinal || next.Constraints.Check[slot].OID == 0 || check.OID != 0 && next.Constraints.Check[slot].OID != check.OID {
			return nil, catalog.Conflict{}
		}
		resolved[i] = next.Constraints.Check[slot]
	}
	return resolved, nil
}
