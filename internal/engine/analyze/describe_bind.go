package analyze

import (
	"context"
	"slices"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/engine/proc"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// Describe binds one statement: the single entry point behind the root
// wrappers describe, describeWithRule and describeScoped. closed marks SQL
// routine bodies whose formals are fixed; initial seeds parameter types.
func Describe(ctx context.Context, host Host, stmt parser.Statement, auth Authority, requiredRef ids.ObjectRef, input sqltype.InputEnvironment, expected Expected, admission *LockAdmission, rule *RuleNamespace, closed bool, initial ...[]sqltype.Usage) (Binding, error) {
	return describe(ctx, host, stmt, auth, requiredRef, input, expected, admission, rule, closed, nil, "", sqltype.CollationRef{}, nil, nil, initial...)
}

// DescribeRoutine shares all ordinary binding with an explicit lexical frame.
func DescribeRoutine(ctx context.Context, host Host, stmt parser.Statement, auth Authority, requiredRef ids.ObjectRef, input sqltype.InputEnvironment, expected Expected, admission *LockAdmission, definition *proc.Definition, name string, inputCollation sqltype.CollationRef) (Binding, error) {
	return describe(ctx, host, stmt, auth, requiredRef, input, expected, admission, nil, true, definition, name, inputCollation, nil, nil, definition.Inputs)
}

func DescribePL(ctx context.Context, host Host, stmt parser.Statement, auth Authority, requiredRef ids.ObjectRef, input sqltype.InputEnvironment, admission *LockAdmission, frame *proc.PLFrame, target *PLAssignment) (Binding, error) {
	formals := make([]sqltype.Usage, frame.Program.FormalCount)
	for i := range formals {
		formals[i] = frame.Usage(i)
	}
	binding, err := describe(ctx, host, stmt, auth, requiredRef, input, Expected{}, admission, nil, false, nil, "", sqltype.CollationRef{}, frame, target, formals)
	if err != nil && target != nil {
		err = &plan.PLAssignmentPlanningError{Err: err}
	}
	return binding, err
}

func describe(ctx context.Context, host Host, stmt parser.Statement, auth Authority, requiredRef ids.ObjectRef, input sqltype.InputEnvironment, expected Expected, admission *LockAdmission, rule *RuleNamespace, closed bool, definition *proc.Definition, name string, inputCollation sqltype.CollationRef, lexical *proc.PLFrame, plTarget *PLAssignment, initial ...[]sqltype.Usage) (Binding, error) {
	b := Binding{}
	binder := NewBinder(ctx, host, input, expected)
	binder.PL = lexical
	binder.Target.RuleNamespace = rule
	binder.Params.NoParameters = rule != nil
	binder.Params.ClosedRoutineFormals = closed
	if definition != nil {
		binder.Params.RoutineName, binder.Params.RoutineNames = name, definition.Names
		binder.Consumer.frozenDefinition = definition.Standard
		if !definition.Standard {
			binder.Params.routineInputCollation = inputCollation
		}
		if definition.Standard && parser.ModifyingStatement(stmt) {
			// Frozen mutation scalars and their presentation project share one
			// lexical frame.
			binder.Query.Frame, binder.Query.NextQueryFrame = 1, 1
		}
	}
	if len(initial) != 0 && initial[0] != nil {
		binder.SeedParameters(initial[0])
	}
	binder.Query.lockAdmission = admission
	binder.Query.Auth = auth
	if err := host.CaptureEnumOperationInput(auth, &binder.Input); err != nil {
		return b, err
	}
	if binder.Query.lockAdmission == nil {
		manifest, _, _, err := NewLockAdmission(stmt)
		if err != nil {
			return b, err
		}
		binder.Query.lockAdmission = &manifest
	}
	cteRestriction := ""
	switch original := stmt.(type) {
	case *parser.CreateQueryTable:
		if original.Form == parser.CreateMaterializedView && parser.HasModifyingCTE(original.Query) {
			cteRestriction = "materialized views must not use data-modifying statements in WITH"
		}
	case *parser.DeclareCursor:
		if parser.HasModifyingCTE(original.Query) {
			cteRestriction = "DECLARE CURSOR must not contain data-modifying statements in WITH"
		}
	}
	var view *parser.CreateView
	materialized := false
	if s, ok := stmt.(*parser.CreateView); ok {
		if parser.LeftmostSelect(s.Query).Into != nil {
			return b, sqlError("0A000", "views must not contain SELECT INTO")
		}
		view = s
		stmt = s.Query
		binder.Params.NoParameters = true
		b.CommandQuery = true
	}
	if s, ok := stmt.(*parser.CreateQueryTable); ok {
		materialized = s.Form == parser.CreateMaterializedView
		binder.Consumer.frozenDefinition = materialized
		stmt = s.Query
		b.CommandQuery = true
	}
	if _, selecting := stmt.(*parser.Select); !selecting {
		if definitions := parser.StatementWith(stmt); len(definitions) != 0 {
			var err error
			binder.cte.statementCTEs, err = binder.bindCTEDefinitions(definitions, parser.StatementWithRecursive(stmt))
			if err != nil {
				return b, err
			}
		}
	}
	switch s := stmt.(type) {
	case *parser.Call:
		node, err := binder.Bind(s.Function, ExpressionPolicy{Context: ProjectionContext})
		if err != nil {
			return b, err
		}
		if definition != nil {
			if call, ok := node.(plan.SQLRoutineExpr); ok && len(call.Procedure.RoutineDefinition().OutputFormals) != 0 {
				return b, sqlError("0A000", "calling procedures with output arguments is not supported in SQL functions")
			}
		}
		if binder.Params.NoParameters || binder.Consumer.frozenDefinition {
			if err := binder.CaptureViewLiterals(s.Function, node, ViewTargetLiteral, 0); err != nil {
				return b, err
			}
		}
		b.Expressions = [][]expr.Expr{{node}}
		b.PLCallOutputs = binder.plCallOutputs
	case *parser.CreateTable:
		if err := bindCreateTable(host, auth, s, &b); err != nil {
			return b, err
		}
	case *parser.Insert, *parser.Update, *parser.Delete, *parser.Merge:
		var err error
		b, err = bindMutationCommand(binder, auth, requiredRef, stmt)
		if err != nil {
			return b, err
		}
	case *parser.DeclareCursor:
		var err error
		binder.Query.Auth = auth
		b.Query, err = binder.BindSelect(s.Query, false)
		if err != nil {
			return b, err
		}
		// PostgreSQL checks the analyzed query's own rowmarks before rewrite;
		// rowmarks inside derived queries or stored views remain admissible.
		if s.Hold && len(s.Query.Locking) != 0 {
			err := sqlError("0A000", "DECLARE CURSOR WITH HOLD ... "+s.Query.Locking[0].Mode+" is not supported")
			err.(*sqlstate.Error).Detail = "Holdable cursors must be READ ONLY."
			return b, err
		}
		if s.Scroll == parser.CursorScrollEnabled && len(s.Query.Locking) != 0 {
			err := sqlError("0A000", "DECLARE SCROLL CURSOR ... "+s.Query.Locking[0].Mode+" is not supported")
			err.(*sqlstate.Error).Detail = "Scrollable cursors must be READ ONLY."
			return b, err
		}
		b.CommandQuery = true
	case *parser.Select:
		var err error
		binder.Query.Auth = auth
		binder.plAssignmentDatum, binder.plAssignmentQuery = plTarget, s
		b.Query, err = binder.BindSelect(s, false)
		if err != nil {
			return b, err
		}
		b.QuerySources = binder.Query.Sources
		if view != nil {
			if err := BindViewOutput(b.Query, view); err != nil {
				return b, err
			}
		}
	default:
		return b, sqlError("0A000", "unsupported statement")
	}
	b.CTEs = binder.cte.statementCTEs
	b.CTESelectBodies = binder.cte.selectBodies
	b.QuerySources = binder.Query.Sources
	var err error
	b.Parameters, err = binder.Finish()
	if err != nil {
		return b, err
	}
	if cteRestriction != "" {
		return b, sqlError("0A000", cteRestriction)
	}
	if binder.cte.scanOrdinal != 0 {
		queries := binder.cte.executables
		if len(queries) == 0 && b.Query != nil {
			queries = []*plan.Query{b.Query}
		}
		for _, query := range queries {
			activateCTESources(query.Root, b.QuerySources)
			query.Root, err = foldCTERestrictions(host, auth.ReadCatalog(), query.Root)
			if err != nil {
				return b, err
			}
		}
	}
	if materialized {
		if b.Parameters.Count != 0 {
			return b, sqlError("0A000", "materialized views may not be defined using bound parameters")
		}
		for _, source := range binder.Query.Sources {
			if ns := auth.ReadCatalog().MustNamespace(source.Relation.Namespace()); ns.Temporary != (ids.SessionToken{}) {
				return b, sqlError("0A000", "materialized views must not use temporary tables or views")
			}
		}
	}
	if len(b.QuerySources) != 0 && view == nil {
		if err := host.ValidateViewReadGraph(auth, b.QuerySources); err != nil {
			return b, err
		}
	}
	// Virtual catalog views still undergo the existing semantic
	// analysis. Reject only after predicates, assignments, RETURNING and
	// parameters have bound, without ever requiring storage or evaluating writes.
	if b.Relation != nil && b.Relation.VirtualView {
		switch stmt.(type) {
		case *parser.Insert:
			return b, pgcatalog.VirtualViewWriteError(b.Relation, "insert", b.Targets)
		case *parser.Update:
			return b, pgcatalog.VirtualViewWriteError(b.Relation, "update", b.Targets)
		case *parser.Delete:
			return b, pgcatalog.VirtualViewWriteError(b.Relation, "delete", nil)
		}
	}
	b.UsedColumns, b.Dependencies = binder.Target.Uses, binder.Dependencies
	b.VisibilitySnapshot = binder.Consumer.VisibilitySnapshot
	b.SubqueryDemand = binder.Query.subqueryDemand
	b.Capture = binder.Capture
	slices.SortFunc(binder.Prepared.Temporals, func(a, b TemporalConstant) int { return a.Offset - b.Offset })
	b.Prepared = binder.Prepared
	if rule == nil && !parser.ModifyingStatement(stmt) {
		if err := host.BindViewWrite(&b, auth, stmt); err != nil {
			return b, err
		}
	}
	for _, definition := range b.CTEs {
		mutation, ok := MutationBinding(definition)
		if !ok {
			continue
		}
		mutation.QuerySources = b.QuerySources
		mutation.Parameters = b.Parameters
		// Auxiliary commands share this binder; borrow its completed original
		// manifest, including bindings discovered after the auxiliary body.
		mutation.Dependencies = b.Dependencies
		if mutation.Relation.VirtualView {
			switch definition.Statement.(type) {
			case *parser.Insert:
				return b, pgcatalog.VirtualViewWriteError(mutation.Relation, "insert", mutation.Targets)
			case *parser.Update:
				return b, pgcatalog.VirtualViewWriteError(mutation.Relation, "update", mutation.Targets)
			case *parser.Delete:
				return b, pgcatalog.VirtualViewWriteError(mutation.Relation, "delete", nil)
			}
		}
	}
	b.Resolvers = NewResolverInputs(binder.Input)
	return b, nil
}
