package table

import (
	"slices"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// One replay decision over an already-bound, validated definition, not DEFAULT
// admission or volatility policy. Captured references and retained standard SQL
// routines may install after their providers are created, without evaluation.
func DefaultReplayDeferrable(definition catalog.ColumnDefault) bool {
	node := definition.Expression
	if node == nil {
		return false
	}
	if definition.CatalogAliasReferences {
		return true
	}
	// A standard SQL body can read this default's own table. CREATE TABLE
	// must precede that routine; the routine must precede SET DEFAULT.
	// Moving the DDL does not change the callable's actual volatility.
	standardRoutine := false
	plan.WalkExpression(node, func(child expr.Expr) {
		call, ok := child.(plan.SQLRoutineExpr)
		if ok && call.Procedure != nil && call.Procedure.SQL != nil &&
			call.Procedure.SQL.Standard && slices.Contains(definition.Dependencies, call.Ref) &&
			call.ValidateStoredProvider() == nil {
			standardRoutine = true
		}
	})
	if standardRoutine {
		return true
	}
	if analyze.ClosedSequenceDefault(node) {
		eligible, captured := true, false
		plan.WalkExpression(node, func(child expr.Expr) {
			if call, ok := child.(functions.SequenceCall); ok {
				captured = true
				if !slices.Contains(definition.Dependencies, call.Ref) {
					eligible = false
				}
			}
		})
		if captured {
			return eligible
		}
	}
	if node.Properties() != (expr.Properties{}) {
		return false
	}
	// The selected stored regclass result subset also includes opaque arrays,
	// domains and numeric coercion trees, without manufacturing DROP providers.
	if node.ResultUsage().Type.ContainsRegclass() && analyze.ClosedRegclassDefault(node) {
		return true
	}
	captured := false
	plan.WalkExpression(node, func(child expr.Expr) {
		literal, ok := child.(plan.LiteralExpr)
		if ok && literal.RegclassRef != (ids.ObjectRef{}) &&
			literal.RegclassRef.Address.ClassID == ids.RelationClassID && literal.RegclassRef.Address.SubID == 0 &&
			literal.ResultUsage().Type.ID() == sqltype.Regclass && !literal.Value.IsNull() &&
			slices.Contains(definition.Dependencies, literal.RegclassRef) {
			captured = true
		}
	})
	return captured
}

// A CREATE may contain defaults from incompatible cast epochs. Their retained
// checked IR can replay as independent ALTERs without evaluating the default or
// changing global deferral. This is admission only; the dump consumer walker
// remains the sole cast-requirement collector.
func retainedCastDefault(definition catalog.ColumnDefault) bool {
	if definition.Expression == nil {
		return false
	}
	selected := false
	plan.WalkExpression(definition.Expression, func(node expr.Expr) {
		switch n := node.(type) {
		case plan.DatumCastExpr:
			selected = selected || n.Method != ""
		case plan.ArrayCastExpr:
			selected = selected || n.Context != ""
		case plan.CaseExpr:
			for _, arm := range n.Arms {
				selected = selected || arm.TestContext != ""
			}
		}
	})
	return selected
}

// ValidateRestoredDefault admits the replayed DEFAULT s on ref only when it
// is inherited replay, or the column's bound default is one Dump defers: a
// validated captured reference, selected regclass or retained standard routine.
// Selected cast defaults can also split incompatible epochs on ordinary or foreign tables.
func ValidateRestoredDefault(view catalog.Read, ref ids.ObjectRef, s *parser.AlterTableDefault) error {
	d := view.MustRelation(ref.Address)
	if InheritedDefaultReplay(s, d) {
		return nil
	}
	slot, ok := d.Index[s.Column.Text]
	if !ok || !(DefaultReplayDeferrable(d.ColumnMeta.Defaults[d.Attributes[slot]]) ||
		retainedCastDefault(d.ColumnMeta.Defaults[d.Attributes[slot]])) {
		return sqlError("0A000", "restore accepts only deferred captured-reference or regclass defaults")
	}
	return nil
}
