package dml

import (
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/parser"
)

// TargetAdmission is the table.TargetEntry of the row statements: the FROM
// table of a single-table SELECT (row-share when it locks rows), the COPY
// relation and the INSERT, MERGE, DELETE and UPDATE target. They target their
// relation while preparing too.
func TargetAdmission(stmt parser.Statement, _ bool) (table.Target, bool, error) {
	switch v := stmt.(type) {
	case *parser.Select:
		if v.From == nil {
			return table.Target{}, false, nil
		}
		target := table.Target{Name: v.From.(*parser.TableReference).Name, Mode: lock.AccessShare}
		if v.Locking != nil {
			target.Mode = lock.RowShare
		}
		return target, true, nil
	case *parser.Copy:
		target := table.Target{Name: v.Name, Mode: lock.AccessShare}
		if v.From {
			target.Mode = lock.RowExclusive
		}
		return target, true, nil
	case *parser.Insert:
		return table.Target{Name: v.Name, Mode: lock.RowExclusive}, true, nil
	case *parser.Merge:
		return table.Target{Name: v.Name, Mode: lock.RowExclusive}, true, nil
	case *parser.Delete:
		return table.Target{Name: v.Name, Mode: lock.RowExclusive}, true, nil
	case *parser.Update:
		return table.Target{Name: v.Name, Mode: lock.RowExclusive}, true, nil
	}
	return table.Target{}, false, nil
}
