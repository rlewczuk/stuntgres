package view

import (
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
)

// PolicyClauseImport is a policy clause grafted onto its consumer: the
// expression, its binding, the nested-view source closure and the relations
// to acquire. Query sources run as the policy's user and have no storage yet.
type PolicyClauseImport struct {
	Node    expr.Expr
	Binding analyze.Binding
	Closure SourceClosure
	Refs    []ids.ObjectRef
}

// ImportPolicyClause grafts policy's stored expression onto consumer over
// read. It reads only the immutable catalog and never waits.
func ImportPolicyClause(read catalog.Read, domain *ids.Domain, consumer *catalog.Relation, policy *stored.Policy, principal authorization.PrincipalRef) (out PolicyClauseImport, err error) {
	if err := policy.Validate(read, consumer); err != nil {
		return out, err
	}
	definition := policy.Presentation()
	binding := &out.Binding
	var importer StoredQueryImporter
	out.Node, _, err = stored.RewriteViewExpression(read, policy.Expression(), nil, func(n expr.Expr) (expr.Expr, bool, error) {
		switch demand := n.(type) {
		case functions.Binary:
			binding.VisibilitySnapshot = binding.VisibilitySnapshot || !demand.Plan.Immutable()
		case expr.SnapshotDemander:
			_, catalogText, visibility := demand.SnapshotDemand(exec.RandomLiteralNull)
			binding.Prepared.RegclassConcat = binding.Prepared.RegclassConcat || catalogText
			binding.VisibilitySnapshot = binding.VisibilitySnapshot || visibility
		}
		column, ok := n.(plan.ColumnExpr)
		if !ok || column.Frame != 1 || column.Origin.Address.ClassID != ids.RelationClassID || column.Origin.Address.ObjectID != consumer.Address().ObjectID {
			return n, false, nil
		}
		slot := -1
		attribute := column.Origin.Address.SubID
		if attribute == 0 && column.Slot >= 0 && column.Slot < len(definition.Sources()[0].Attributes) {
			attribute = definition.Sources()[0].Attributes[column.Slot]
		}
		for i, current := range consumer.Attributes {
			if current == attribute {
				slot = i
				break
			}
		}
		if slot < 0 {
			return nil, false, ids.ErrCatalogReplaced
		}
		use, err := stored.ViewUseReference(read, consumer, column.Origin)
		if err != nil {
			return nil, false, err
		}
		column.Slot, column.QueryOccurrence, column.Origin = slot, 0, use
		return column, true, nil
	}, func(root plan.Node, rewrite func(expr.Expr) (expr.Expr, bool, error)) (plan.Node, error) {
		return importer.GraftQuery(binding, read, root, definition, consumer, rewrite)
	})
	if err != nil {
		return out, err
	}
	for i := range binding.QuerySources {
		binding.QuerySources[i].Principal = principal
	}
	// Closure treats its root leaves as already admitted, so the caller
	// explicitly acquires those captured refs in addition to the nested-view
	// closure and providers.
	if len(binding.QuerySources) != 0 {
		out.Closure, err = (SourceWalk{Catalog: read, Domain: domain, Epoch: read.Epoch(), Leaves: binding.QuerySources}).Closure()
		if err != nil {
			return out, err
		}
	}
	refs := append([]ids.ObjectRef(nil), out.Closure.Refs...)
	add := func(ref ids.ObjectRef) {
		ref.Address.SubID = 0
		for _, prior := range refs {
			if prior == ref {
				return
			}
		}
		refs = append(refs, ref)
	}
	for _, leaf := range binding.QuerySources {
		add(leaf.Ref)
	}
	for _, dep := range definition.Dependencies() {
		if dep.Ref.Address.ClassID != ids.RelationClassID || dep.Ref.Address.ObjectID == consumer.Address().ObjectID {
			continue
		}
		ref, err := stored.ViewUseReference(read, consumer, dep.Ref)
		if err != nil {
			return out, err
		}
		add(ref)
	}
	out.Refs = refs
	return out, nil
}

// CheckPolicyAuthority checks the policy's admitted sources against a's
// current catalog and rights, with a's current user as the policy's user.
func CheckPolicyAuthority(a access.View, env access.Environment, consumer *catalog.Relation, policy *stored.Policy) error {
	current, ok := a.Catalog.Relation(consumer.Address())
	if !ok || current != consumer {
		return ids.ErrCatalogReplaced
	}
	if err := policy.Validate(a.Catalog, consumer); err != nil {
		return err
	}
	for _, leaf := range policy.Presentation().Sources()[1:] {
		if leaf.AnalysisOnly {
			continue
		}
		relation, ok := a.Catalog.Relation(leaf.Ref.Address)
		if !ok {
			return ids.ErrCatalogReplaced
		}
		indices, err := analyze.QuerySourceReadUses(leaf.Reads, relation.Attributes)
		if err != nil {
			return err
		}
		ref, err := stored.ViewUseReference(a.Catalog, consumer, leaf.Ref)
		if err != nil {
			return err
		}
		if leaf.TableOIDRead {
			if err := a.Check(env, access.Requirement{Object: ref, Privileges: authorization.SelectPrivilege}); err != nil {
				return err
			}
		}
		if err := a.CheckColumns(env, ref, relation, authorization.SelectPrivilege, indices); err != nil {
			return err
		}
		if exec.EffectiveQueryRowMark(relation, leaf.Mark).Mode != 0 {
			if err := a.CheckColumns(env, ref, relation, authorization.UpdatePrivilege, nil); err != nil {
				return err
			}
		}
		if relation.RelationKind == catalog.ViewRelation && (leaf.Mark.Mode != 0 || stored.ViewHasRowMarks(relation, a.Catalog)) {
			if err := CheckRowMarks(a, env, relation, leaf.Mark, a.State.CurrentUser); err != nil {
				return err
			}
		}
	}
	return nil
}
