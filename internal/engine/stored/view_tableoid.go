package stored

import (
	"fmt"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// The source occurrence belongs to the captured lexical frame. It is never an
// ordinary output slot, including for zero-column relations and outer joins.
func (v *View) tableOIDSource(read catalog.Read, column analyze.ViewColumn) (*plan.SourceNode, error) {
	if !column.TableOID || column.Scope != 1 || column.Frame == 0 || column.Occurrence <= 0 || column.Slot != 0 || column.RuleSide != 0 || column.RuleRef != (ids.ObjectRef{}) || column.Owner.Address.SubID != 0 {
		return nil, fmt.Errorf("stuntgres: invalid frozen tableoid coordinates")
	}
	owner := v.frames[column.Frame]
	if owner == nil {
		return nil, fmt.Errorf("stuntgres: missing frozen tableoid frame")
	}
	source := viewSourceAt(analyze.ViewFromRoot(owner), column.Occurrence-1)
	if source == nil || source.Occurrence >= len(v.sources) || source.Ref != column.Owner {
		return nil, fmt.Errorf("stuntgres: mismatched frozen tableoid occurrence")
	}
	leaf := v.sources[source.Occurrence]
	relation, ok := read.Relation(column.Owner.Address)
	var consumer *catalog.Relation
	if v.consumer != (ids.ObjectAddress{}) {
		consumer, _ = read.Relation(v.consumer)
	}
	if !ok || leaf.Ref != column.Owner || !leaf.TableOIDRead || !relation.HasSystemAttributes() || !ViewReferenceValid(read, consumer, column.Owner) {
		return nil, fmt.Errorf("stuntgres: invalid frozen tableoid owner")
	}
	return source, nil
}

func (v *viewLiteralValidator) validateTableOIDOccurrence(path analyze.ViewLiteralPath, pos parser.Position) error {
	column := v.columns[path]
	if _, err := v.tableOIDSource(v.read, column); err != nil {
		return err
	}
	found := false
	matches := func(n plan.TableOIDExpr) bool {
		return n.Owner == column.Owner && n.Scope == column.Scope && n.Frame == column.Frame && n.QueryOccurrence == column.Occurrence && n.ResultUsage().Type.ID() == sqltype.Oid
	}
	var grouped func(plan.Node, int) bool
	grouped = func(root plan.Node, slot int) bool {
		if root == nil {
			return false
		}
		if project, ok := root.(*plan.ProjectNode); ok && project.Frame != column.Frame {
			return false
		}
		if aggregate, ok := root.(*plan.AggregateNode); ok && slot >= 0 && slot < len(aggregate.Keys) {
			if n, ok := aggregate.Keys[slot].(plan.TableOIDExpr); ok && matches(n) {
				return true
			}
		}
		left, right := plan.QueryInputs(root)
		return grouped(left, slot) || grouped(right, slot)
	}
	plan.VisitCTEQuery(v.query.Root, func(plan.Node) {}, func(bound expr.Expr) {
		plan.WalkExpression(bound, func(node expr.Expr) {
			if n, ok := node.(plan.TableOIDExpr); ok && n.SourcePosition() == pos && matches(n) {
				found = true
			}
			if n, ok := node.(plan.GroupExpr); ok && n.Frame == column.Frame && n.SourcePosition() == pos && grouped(v.frames[column.Frame], n.Slot) {
				found = true
			}
		})
	}, true)
	if !found {
		return fmt.Errorf("stuntgres: mismatched frozen tableoid scalar capture")
	}
	return nil
}

func (v *QueryRenderer) setSourceQualifier(node plan.Node, qualifier string) {
	source, ok := node.(*plan.SourceNode)
	if !ok || source.Occurrence < 0 || source.Occurrence >= len(v.sources) || !v.sources[source.Occurrence].TableOIDRead {
		return
	}
	if v.sourceQualifiers == nil {
		v.sourceQualifiers = make(map[*plan.SourceNode]string)
	}
	v.sourceQualifiers[source] = qualifier
}

func (v *QueryRenderer) tableOIDSQL(read catalog.Read, column analyze.ViewColumn) (string, error) {
	source, err := v.tableOIDSource(read, column)
	if err != nil {
		return "", err
	}
	qualifier := v.sourceQualifiers[source]
	if qualifier == "" {
		return "", fmt.Errorf("stuntgres: missing frozen tableoid source qualifier")
	}
	return qualifier + ".tableoid", nil
}

// Function argument captures use the same lexical occurrence as their scalar
// leaves. Publication rejects omitted, substituted or extra identity captures.
func validateTableOIDFunctionCaptures(function *plan.FunctionFromNode) error {
	expected := 0
	for _, capture := range function.Captures {
		if capture.QueryOccurrence == 0 {
			continue
		}
		if capture.QueryOccurrence < 0 || capture.Frame != function.Frame || capture.Slot != 0 {
			return fmt.Errorf("stuntgres: invalid frozen function identity capture")
		}
		expected++
	}
	actual := 0
	var failure error
	for _, call := range function.Calls {
		analyze.WalkQueryArgumentExpression(call, func(node expr.Expr) {
			system, ok := node.(plan.TableOIDExpr)
			if !ok || system.Scope != 1 || system.Frame != function.Frame {
				return
			}
			actual++
			matches := 0
			for _, capture := range function.Captures {
				if capture.QueryOccurrence == system.QueryOccurrence && capture.Frame == system.Frame && capture.Pos == system.Pos {
					matches++
				}
			}
			if matches != 1 {
				failure = fmt.Errorf("stuntgres: mismatched frozen function identity capture")
			}
		})
	}
	if failure != nil {
		return failure
	}
	if actual != expected || actual != 0 && !function.Correlated {
		return fmt.Errorf("stuntgres: incomplete frozen function identity capture")
	}
	return nil
}
