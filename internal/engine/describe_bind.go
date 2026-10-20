package engine

import (
	"context"
	"slices"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/plan"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// Describe binds one statement: the single entry point behind the root
// wrappers describe, describeWithRule and describeScoped. closed marks SQL
// routine bodies whose formals are fixed; initial seeds parameter types.
func Describe(ctx context.Context, host analyzeHost, stmt parser.Statement, auth analyze.Authority, requiredRef ids.ObjectRef, input sqltype.InputEnvironment, expected analyze.Expected, admission *queryLockAdmission, rule *analyze.RuleNamespace, closed bool, initial ...[]sqltype.Usage) (binding, error) {
	b := binding{}
	binder := NewBinder(ctx, host, input, expected)
	binder.Target.RuleNamespace = rule
	binder.Params.NoParameters = rule != nil
	binder.Params.ClosedRoutineFormals = closed
	if len(initial) != 0 && initial[0] != nil {
		binder.SeedParameters(initial[0])
	}
	binder.Query.lockAdmission = admission
	binder.Query.Auth = auth
	if err := host.CaptureEnumOperationInput(auth, &binder.Input); err != nil {
		return b, err
	}
	if binder.Query.lockAdmission == nil {
		manifest, _, _, err := analyze.NewLockAdmission(stmt)
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
		binder.Consumer.materializedDefinition = materialized
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
		b.Query, err = binder.BindSelect(s, false)
		if err != nil {
			return b, err
		}
		b.QuerySources = binder.Query.Sources
		if view != nil {
			if err := analyze.BindViewOutput(b.Query, view); err != nil {
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
		if err := host.ValidateViewReadGraph(auth.ReadCatalog(), b.QuerySources); err != nil {
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
	slices.SortFunc(binder.Prepared.Temporals, func(a, b temporalConstant) int { return a.Offset - b.Offset })
	b.Prepared = binder.Prepared
	if rule == nil && !parser.ModifyingStatement(stmt) {
		if err := host.BindViewWrite(&b, auth, stmt); err != nil {
			return b, err
		}
	}
	for _, definition := range b.CTEs {
		mutation, ok := analyze.MutationBinding(definition)
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
	b.Resolvers = analyze.NewResolverInputs(binder.Input)
	return b, nil
}
