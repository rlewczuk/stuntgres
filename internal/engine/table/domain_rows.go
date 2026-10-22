package table

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/typedef"
	"stuntgres/internal/engine/visibility"
)

// ValidateSelectedDomainRows checks the live rows of one domain use for
// ALTER DOMAIN SET NOT NULL or VALIDATE CONSTRAINT: the committed versions
// visible at frontier that the statement did not replace, then its own
// writes. The first violation is the column's historical diagnostic.
func ValidateSelectedDomainRows(c *exec.Context, view catalog.Read, selected *catalog.Type, use typedef.DomainValidationUse, frontier ids.CommitSeq, writes []storage.RowVersion, deletes *visibility.PrivateDeletes, validation typedef.DomainValidation) error {
	violations := make([]error, len(use.Columns))
	for i, column := range use.Columns {
		violations[i] = typedef.DomainHistoricalViolation(view, use.Relation, column, validation)
	}
	var replaced map[ids.RowID]bool
	for _, row := range writes {
		if row.Base != 0 {
			if replaced == nil {
				replaced = make(map[ids.RowID]bool)
			}
			replaced[row.Row] = true
		}
	}
	scan := func(rows []storage.RowVersion, committed bool) error {
		for _, row := range rows {
			if err := c.Context.Err(); err != nil {
				return err
			}
			if committed && replaced[row.Row] || !visibility.LiveVersion(row, frontier, use.Ends, deletes) {
				continue
			}
			for i, column := range use.Columns {
				if validation == typedef.DomainValidateNulls {
					if row.Values[column].IsNull() {
						return violations[i]
					}
				} else if err := c.EvaluateSelectedDomainCheck(view, selected, row.Values[column], violations[i]); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if err := scan(use.Versions, true); err != nil {
		return err
	}
	return scan(writes, false)
}
