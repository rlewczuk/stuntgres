package engine

import (
	"fmt"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ddl"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/partition"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// ddlHost implements partition.Host for CREATE ... PARTITION OF and
// PARTITION BY. Like the rest of ddlHost, every method runs under Engine.mu
// and never waits.
var _ partition.Host = DDLHost{}

func (h DDLHost) BindKeyExpression(a ddl.Authority, d *catalog.Relation, raw parser.Expr, input sqltype.InputEnvironment) (*catalog.DerivedIndexKey, []int, error) {
	auth := DDLAuthorityOf(a)
	ref := h.E.Catalog.Domain.Ref(auth.Catalog.Epoch(), d.Address())
	namespace := analyze.RelationNamespace(auth.Catalog, d, ref, 0)
	binder := NewExpressionBinder(h.Op.Context(), h.E, input, h.Op, nil)
	binder.Consumer.PartitionConsumer, binder.Params.NoParameters, binder.Query.Auth = "partition key expression", true, &auth
	binder.Query.Namespace = &namespace
	binder.Query.Sources = []exec.Leaf{{Ref: ref, Relation: d}}
	node, err := binder.Bind(raw, analyze.ExpressionPolicy{Context: analyze.ProjectionContext})
	if err != nil {
		return nil, nil, err
	}
	evaluation := exec.Context{EvalStatement: exec.EvalStatement{Context: h.Op.Context(), Input: binder.Input, Catalog: auth.Catalog}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}}
	planned, _, err := exec.PreflightCoercionsWithPolicy(&evaluation, node, true)
	if err != nil {
		return nil, nil, err
	}
	return &catalog.DerivedIndexKey{Definition: node, Execution: planned, Attributes: d.Attributes}, binder.Query.Sources[0].Uses, nil
}

// partitionBoundBinder binds the bound datums of one pending definition for
// the statement that owns it.
type partitionBoundBinder struct {
	scope   *StatementScope
	pending *pendingPartitionDefinition
}

var _ partition.BoundBinder = partitionBoundBinder{}

// BindBound captures one datum's exact provider references under mu. Its
// caller evaluates the returned tree only after this method releases mu;
// transient sequence calls retain their ordinary RX/grant-time admission.
func (b partitionBoundBinder) BindBound(datum parser.PartitionDatum, key catalog.PartitionKeyEntry) (expr.Expr, error) {
	scope, p := b.scope, b.pending
	e := scope.Engine()
	e.Mu.Lock()
	defer e.Mu.Unlock()
	auth, err := e.AuthorizationLocked(scope.Op)
	if err != nil {
		return nil, err
	}
	if auth.Catalog.Epoch() != p.auth.Catalog.Epoch() {
		return nil, ids.ErrCatalogReplaced
	}
	if auth.Revision != p.stage.AuthRevision || auth.RoleRecord.Revision != p.stage.RoleRevision {
		return nil, fmt.Errorf("stuntgres: authorization changed before publication")
	}
	binder := NewExpressionBinder(scope.Op.Context(), e, scope.Input, scope.Op, nil)
	binder.Query.Auth = &p.auth
	binder.Params.NoParameters = true
	binder.Consumer.PartitionConsumer = "partition bound"
	node, err := binder.Bind(datum.Expression, analyze.ExpressionPolicy{Context: analyze.ProjectionContext, Expected: key.Usage.Type.Usage()})
	if err != nil {
		return nil, err
	}
	if node.ResultUsage() != key.Usage {
		input, annotations := analyze.DomainCoercionInput(node, key.Usage)
		info, err := analyze.StorageCoercionInfo(binder.BindingCatalog(), binder.Info(key.Usage, datum.Pos, input.Properties()), input)
		if err != nil {
			return nil, err
		}
		coercionPlan, err := sqltype.PlanCoercion(input.ResultUsage(), info.Usage, sqltype.Assignment)
		if err != nil {
			return nil, expr.ErrorAt("42804", fmt.Sprintf(`specified value cannot be cast to type %s for column "%s"`, key.Usage.Type.DiagnosticName(), key.Label), datum.Pos)
		}
		info.Property = analyze.CoercionProperties(input, coercionPlan)
		node = analyze.RestoreDomainCastCollations(annotations, plan.CoercionExpr{Info: info, Child: input, Plan: coercionPlan, Mode: sqltype.Assignment})
	}
	return analyze.StorageDestination(node, key.Usage, node.SourcePosition())
}
