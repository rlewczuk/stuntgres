package stored

import (
	"context"
	"fmt"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

// TriggerCarrier retains the actual element of the immutable owner payload,
// including its checked WHEN, presentation and binding, across replacement.
// Its structural output is native logical structure, not nodeToString bytes.
type TriggerCarrier struct { definition *Trigger }

func TriggerTree(ctx context.Context, read catalog.Read, owner *catalog.Relation, typ *sqltype.Descriptor, definition *Trigger) (sqltype.Value, error) {
	if err := ctx.Err(); err != nil { return sqltype.Value{}, err }
	if read.Owner() == nil || read.State() == nil || owner == nil || read.Get(owner.Address()) != owner {
		return sqltype.Value{}, fmt.Errorf("stuntgres: foreign trigger owner")
	}
	captured, ok := read.LookupType(sqltype.PgNodeTree)
	if !ok || captured != typ { return sqltype.Value{}, fmt.Errorf("stuntgres: foreign trigger tree type") }
	list, ok := owner.Triggers.(TriggerList)
	if !ok { return sqltype.Value{}, fmt.Errorf("stuntgres: invalid trigger payload") }
	found := false
	for i := range list { if &list[i] == definition { found = true; break } }
	if !found { return sqltype.Value{}, fmt.Errorf("stuntgres: detached trigger payload") }
	if err := list.Validate(read, owner); err != nil { return sqltype.Value{}, err }
	return triggerTree(ctx, typ, definition)
}

// The visitor already validated the complete actual payload once.
func triggerTree(ctx context.Context, typ *sqltype.Descriptor, definition *Trigger) (sqltype.Value, error) {
	if err := ctx.Err(); err != nil { return sqltype.Value{}, err }
	if definition.parts.When == nil { return sqltype.Null(typ), nil }
	return sqltype.Tree(typ, TriggerCarrier{definition: definition})
}

func (t TriggerCarrier) StructuralOutput(ctx context.Context) (string, error) {
	if t.definition == nil || t.definition.parts.When == nil { return "", fmt.Errorf("stuntgres: missing trigger carrier") }
	return (plan.BoundTree{Expression: t.definition.parts.When}).StructuralOutput(ctx)
}

// Preflight checks pseudo namespaces before the consumer opens its context
// relation. NEW is varno 2, even when OLD never occurs in the expression.
func (t TriggerCarrier) Preflight(ctx context.Context, relation ids.OID) error {
	if err := ctx.Err(); err != nil { return err }
	if t.definition == nil || t.definition.parts.When == nil { return fmt.Errorf("stuntgres: missing trigger carrier") }
	var old, other bool
	plan.WalkExpression(t.definition.parts.When, func(node expr.Expr) {
		switch n := node.(type) {
		case plan.ColumnExpr:
			if n.Scope == analyze.RulePseudoScope { old = old || n.Frame == 1; other = other || n.Frame != 1 }
		case plan.RowExpr:
			old = old || n.RuleSide == 1
			other = other || n.RuleSide != 0 && n.RuleSide != 1
		}
	})
	if err := ctx.Err(); err != nil { return err }
	if relation == 0 && (old || other) { return sqlError("22023", "expression contains variables") }
	if relation != 0 && other { return sqlError("22023", "expression contains variables of more than one relation") }
	return nil
}

func TriggerDisplay(ctx context.Context, carrier TriggerCarrier, renderer Deparser) (string, error) {
	var id ids.OID
	if renderer.Relation != nil { id = renderer.Relation.Address().ObjectID }
	if err := carrier.Preflight(ctx, id); err != nil { return "", err }
	renderer.NodeSQL = func(node expr.Expr) (string, bool, error) {
		if err := ctx.Err(); err != nil { return "", true, err }
		if row, ok := node.(plan.RowExpr); ok && row.RuleSide == 1 {
			if renderer.Relation == nil { return "", true, sqlError("22023", "expression contains variables") }
			return renderer.Identifier(renderer.Relation.Name()) + ".*", true, nil
		}
		return "", false, nil
	}
	return renderer.SQL(ctx, carrier.definition.parts.When, DeparseParent{})
}
