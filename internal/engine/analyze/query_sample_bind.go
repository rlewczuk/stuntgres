package analyze

import (
	"fmt"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func (b *Binder) bindTableSample(raw *parser.TableSample, occurrence int) (*plan.TableSample, error) {
	if raw == nil {
		return nil, nil
	}
	auth, err := b.BindingAuthorization()
	if err != nil {
		return nil, err
	}
	path, err := b.Host.LookupNamespacesSnapshot(auth, raw.Method, InstalledFunctionLookup)
	if err != nil {
		return nil, err
	}
	name := raw.Method.Parts[len(raw.Method.Parts)-1].Text
	pos := raw.Method.Parts[0].Pos
	read := auth.ReadCatalog()
	procedure := ProcedureInNamespaces(read, path, name, "2281")
	if procedure == nil {
		return nil, expr.ErrorAt("42704", fmt.Sprintf("tablesample method %s does not exist", name), pos)
	}
	if procedure.Result != 3310 {
		return nil, expr.ErrorAt("42809", fmt.Sprintf("function %s must return type tsm_handler", name), pos)
	}
	if err := extension.SystemRowsCapability(read, procedure); err != nil {
		return nil, err
	}
	if len(raw.Arguments) != 1 {
		return nil, expr.ErrorAt("2202H", fmt.Sprintf("tablesample method %s requires 1 argument, not %d", name, len(raw.Arguments)), pos)
	}
	argument := raw.Arguments[0]
	policy := ExpressionPolicy{Context: ProjectionContext}
	usage := b.Type(sqltype.Int8).Usage()
	switch value := argument.(type) {
	case parser.LiteralExpr:
		if value.Value.Kind == parser.NullValue || value.Value.Kind == parser.StringValue {
			policy.Expected = usage
		}
	case parser.ParameterExpr:
		if b.Params.Parameters[value.Number].Type == nil {
			policy.Expected = usage
		}
	}
	oldClause := b.Clause.AggregateClause
	b.Clause.AggregateClause = "TABLESAMPLE"
	count, err := b.Bind(argument, policy)
	b.Clause.AggregateClause = oldClause
	if err != nil {
		return nil, err
	}
	count, err = b.coerceAssignmentForConstruct(count, usage, "TABLESAMPLE", argument)
	if err != nil {
		return nil, err
	}
	if raw.Repeatable != nil {
		return nil, expr.ErrorAt("0A000", fmt.Sprintf("tablesample method %s does not support REPEATABLE", name), pos)
	}
	ref := b.Host.CatalogOwner().Domain.Ref(read.Epoch(), procedure.Address())
	b.dependency(ref, procedure.Revision())
	if b.Params.NoParameters || b.Consumer.frozenDefinition {
		if err := b.CaptureViewLiterals(argument, count, ViewSampleLiteral, occurrence); err != nil {
			return nil, err
		}
		if err := b.Capture.CaptureRoutineReference(ViewLiteralPath{Query: b.FrameID(), Clause: ViewSampleHandler, Target: occurrence}, ref); err != nil {
			return nil, err
		}
	}
	return &plan.TableSample{Handler: ref, Procedure: procedure, Count: count, Captures: b.queryCaptures([]expr.Expr{count}, false)}, nil
}

func sampleRelationError(name parser.ObjectName) error {
	return expr.ErrorAt("0A000", "TABLESAMPLE clause can only be applied to tables and materialized views", name.Parts[0].Pos)
}
