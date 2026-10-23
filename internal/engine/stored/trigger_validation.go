package stored

import (
	"context"
	"fmt"
	"slices"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

func validateTriggerWhen(read catalog.Read, owner *catalog.Relation, p TriggerParts) error {
	for _, attribute := range p.UpdateColumns {
		address := owner.Address()
		address.SubID = attribute
		if !slices.Contains(p.References, address) {
			return fmt.Errorf("stuntgres: missing trigger UPDATE dependency")
		}
	}
	if p.Parent.ObjectID != 0 {
		if p.Parent.ClassID != 2620 || p.ParentRevision == 0 || owner.Membership == nil {
			return fmt.Errorf("stuntgres: invalid trigger clone provenance")
		}
		parent, ok := read.Relation(owner.Membership.Parent)
		if !ok {
			return fmt.Errorf("stuntgres: missing trigger clone parent")
		}
		found := false
		_ = parent.VisitTriggers(func(t catalog.TriggerFact) error { found = found || t.OID == p.Parent.ObjectID; return nil })
		if !found {
			return fmt.Errorf("stuntgres: stale trigger clone parent")
		}
	}
	if p.When == nil {
		return nil
	}
	if p.Binding.Parameters.Count != 0 || p.Binding.Query == nil {
		return fmt.Errorf("stuntgres: invalid trigger WHEN binding")
	}
	var invalid error
	plan.WalkExpression(p.When, func(node expr.Expr) {
		if invalid != nil {
			return
		}
		if !ValidExpressionUsage(read, node.ResultUsage()) {
			invalid = fmt.Errorf("stuntgres: invalid trigger WHEN type")
			return
		}
		if err := ValidateUsageCollation(read, node.ResultUsage()); err != nil {
			invalid = err
			return
		}
		if procedure, ok := expr.InfoOf(node).RoutineReference.(*catalog.Procedure); ok {
			if _, err := exec.ValidateStoredRoutineDescriptor(read, procedure); err != nil {
				invalid = err
				return
			}
		}
		switch n := node.(type) {
		case plan.PLLocalExpr, plan.ParameterExpr, plan.SubqueryExpr:
			invalid = fmt.Errorf("stuntgres: invocation value or subquery in stored trigger WHEN")
		case plan.ColumnExpr:
			address := n.Origin.Address
			address.SubID = 0
			if n.Scope != analyze.RulePseudoScope || n.Frame != 1 && n.Frame != 2 || address != owner.Address() || !ReferenceEpochValid(read, owner, n.Origin) || n.Slot < 0 || n.Slot >= len(owner.Columns) || owner.Attributes[n.Slot] != n.Origin.Address.SubID || n.ResultUsage() != owner.Columns[n.Slot].Usage {
				invalid = fmt.Errorf("stuntgres: invalid trigger WHEN row reference")
				return
			}
			if n.Frame == 1 && p.Events&TriggerInsert != 0 || n.Frame == 2 && p.Events&TriggerDelete != 0 {
				invalid = fmt.Errorf("stuntgres: unavailable trigger WHEN row")
				return
			}
			if n.Frame == 2 {
				if _, generated := owner.ColumnMeta.Generations[n.Origin.Address.SubID]; generated {
					invalid = fmt.Errorf("stuntgres: generated NEW in BEFORE trigger WHEN")
					return
				}
			}
			if !slices.Contains(p.References, n.Origin.Address) {
				invalid = fmt.Errorf("stuntgres: missing trigger WHEN attribute dependency")
			}
		}
	})
	if invalid != nil {
		return invalid
	}
	// A valid executable carrier must also retain every semantic DROP edge.
	// Use the same pinned-provider filtering as declaration capture and policy
	// validation, rather than trusting a separately supplied reference list.
	references, err := ExpressionDependRefs(context.Background(), read, p.When)
	if err != nil {
		return err
	}
	for _, address := range references {
		if !slices.Contains(p.References, address) {
			return fmt.Errorf("stuntgres: missing trigger WHEN expression dependency")
		}
	}
	identity := func(d *sqltype.Descriptor) *sqltype.Descriptor { return d }
	_, _, err = RetagTypes(read, p.When, identity)
	if err != nil {
		return err
	}
	if err := p.Presentation.validateTriggerLiterals(read, owner, p.Events); err != nil {
		return err
	}
	if err := ValidateViewLeaves(read, p.Presentation); err != nil {
		return err
	}
	if err := p.Presentation.validateNamespaces(); err != nil {
		return err
	}
	for _, dependency := range p.Binding.Dependencies {
		if !ReferenceEpochValid(read, owner, dependency.Ref) {
			return fmt.Errorf("stuntgres: invalid trigger WHEN admission dependency")
		}
		address := dependency.Ref.Address
		address.SubID = 0
		if read.Get(address) == nil {
			return fmt.Errorf("stuntgres: missing trigger WHEN admission provider")
		}
	}
	return nil
}
