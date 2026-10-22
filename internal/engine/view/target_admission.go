package view

import (
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/parser"
)

// TargetAdmission is the table.TargetEntry of the rule, policy and view
// statements: CREATE, ALTER and DROP RULE and POLICY on their table, CREATE
// OR REPLACE VIEW and ALTER VIEW. None targets a relation while preparing.
func TargetAdmission(stmt parser.Statement, execute bool) (table.Target, bool, error) {
	if !execute {
		return table.Target{}, false, nil
	}
	switch v := stmt.(type) {
	case *parser.CreateRule:
		return table.Target{Name: v.Table, Mode: lock.AccessExclusive, OwnerOnly: true, Rename: true}, true, nil
	case *parser.AlterRule:
		return table.Target{Name: v.Table, Mode: lock.AccessExclusive, OwnerOnly: true, Rename: true}, true, nil
	case *parser.DropRule:
		return table.Target{Name: v.Table, Mode: lock.AccessExclusive, Rename: true, IfExists: v.IfExists}, true, nil
	case *parser.CreatePolicy:
		return table.Target{Name: v.Table, Mode: lock.AccessExclusive, OwnerOnly: true, Rename: true}, true, nil
	case *parser.AlterPolicy:
		return table.Target{Name: v.Table, Mode: lock.AccessExclusive, OwnerOnly: true, Rename: true}, true, nil
	case *parser.DropPolicy:
		return table.Target{Name: v.Table, Mode: lock.AccessExclusive, Rename: true, IfExists: v.IfExists}, true, nil
	case *parser.CreateView:
		if !v.Replace {
			return table.Target{}, false, nil
		}
		return table.Target{Name: v.Name, Mode: lock.AccessExclusive, OwnerOnly: true, Rename: true, ViewReplacement: true}, true, nil
	case *parser.AlterView:
		return table.Target{Name: v.Name, Mode: lock.AccessExclusive, OwnerOnly: true, Rename: true, IfExists: v.IfExists, TableRename: v.Action == "rename"}, true, nil
	}
	return table.Target{}, false, nil
}
