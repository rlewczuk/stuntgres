package access

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

func rowSecurityAction(action parser.AlterTableActionKind) bool {
	return action >= parser.EnableRowSecurity && action <= parser.NoForceRowSecurity
}

// RowSecurityActions reports whether actions is a non-empty list of only
// ENABLE, DISABLE, FORCE and NO FORCE ROW LEVEL SECURITY.
func RowSecurityActions(actions []parser.AlterTableAction) bool {
	if len(actions) == 0 {
		return false
	}
	for _, action := range actions {
		if !rowSecurityAction(action.Action) {
			return false
		}
	}
	return true
}

// RowSecurityFlags applies the row-security actions to old. It returns old
// itself when the flags do not change, and otherwise a copy.
func RowSecurityFlags(old *catalog.Relation, actions []parser.AlterTableAction) *catalog.Relation {
	enabled, forced := old.Security.RLSEnabled, old.Security.RLSForced
	for _, action := range actions {
		switch action.Action {
		case parser.EnableRowSecurity:
			enabled = true
		case parser.DisableRowSecurity:
			enabled = false
		case parser.ForceRowSecurity:
			forced = true
		case parser.NoForceRowSecurity:
			forced = false
		}
	}
	if enabled == old.Security.RLSEnabled && forced == old.Security.RLSForced {
		return old
	}
	next := *old
	next.Security.RLSEnabled, next.Security.RLSForced = enabled, forced
	return &next
}

func rowSecurityActionLabel(action parser.AlterTableActionKind) string {
	switch action {
	case parser.EnableRowSecurity:
		return "ENABLE ROW SECURITY"
	case parser.DisableRowSecurity:
		return "DISABLE ROW SECURITY"
	case parser.ForceRowSecurity:
		return "FORCE ROW SECURITY"
	default:
		return "NO FORCE ROW SECURITY"
	}
}

// RowSecurityKind rejects row-security actions on relations other than tables
// and partitioned tables. tableKind reports the kinds without a row-security
// message of their own, given the action label.
func RowSecurityKind(d *catalog.Relation, action parser.AlterTableActionKind, tableKind func(*catalog.Relation, string) error) error {
	if d.RelationKind == catalog.TableRelation || d.RelationKind == catalog.PartitionedRelation {
		return nil
	}
	kind := ""
	switch d.RelationKind {
	case catalog.ViewRelation:
		kind = "views"
	case catalog.MaterializedViewRelation:
		kind = "materialized views"
	case catalog.SequenceRelation:
		kind = "sequences"
	case catalog.IndexRelation:
		kind = "indexes"
	case catalog.CompositeRelation:
		kind = "composite types"
	default:
		return tableKind(d, rowSecurityActionLabel(action))
	}
	return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42809", Message: fmt.Sprintf(`ALTER action %s cannot be performed on relation "%s"`, rowSecurityActionLabel(action), d.Name()), Detail: "This operation is not supported for " + kind + "."}
}
