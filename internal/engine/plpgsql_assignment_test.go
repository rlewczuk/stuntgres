package engine

import (
	"context"
	"errors"
	"slices"
	"testing"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/proc"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func plUnitAssignmentTarget(t *testing.T, path string) proc.PLTarget {
	t.Helper()
	raw, err := parser.ParsePLpgSQL("BEGIN "+path+" := 1; END", parser.PLReturnPolicy{})
	if err != nil {
		t.Fatal(err)
	}
	p := &proc.PLProgram{}
	target, err := p.ResolveTarget(raw.Statements[0].Target, []*proc.PLBlock{{Bindings: map[string]int{"a": 0}}})
	if err != nil {
		t.Fatal(err)
	}
	return target
}

// A reached lexical RHS keeps the supplied value's real type and forces leaf
// conversion through the production query/admission/preflight path.
func plUnitAssignQueryValue(t *testing.T, s *StatementScope, f *proc.PLFrame, path string, rhs sqltype.Value) (sqltype.Value, error) {
	t.Helper()
	raw, err := parser.ParsePLpgSQL("BEGIN "+path+" := pa_unit_rhs; END", parser.PLReturnPolicy{})
	if err != nil {
		t.Fatal(err)
	}
	program, values, scopes := f.Program, f.Values, f.Scopes
	borrowed := *program
	borrowed.Variables = append(slices.Clone(program.Variables), proc.PLVariable{Name: "pa_unit_rhs", Usage: rhs.Type().Usage()})
	bindings := map[string]int{"pa_unit_rhs": len(values)}
	for slot, variable := range program.Variables {
		bindings[variable.Name] = slot
	}
	f.Program = &borrowed
	f.Values = append(slices.Clone(values), rhs)
	f.Scopes = append(slices.Clone(scopes), &proc.PLBlock{Bindings: bindings})
	defer func() {
		copy(values, f.Values[:len(values)])
		f.Program, f.Values, f.Scopes = program, values, scopes
	}()
	statement := raw.Statements[0]
	target, err := borrowed.ResolveTarget(statement.Target, f.Scopes)
	if err != nil {
		t.Fatal(err)
	}
	query := &parser.Select{Targets: []parser.Expr{statement.Expression}, Pos: statement.Expression.SourcePosition()}
	return s.assignPLTarget(f, target, statement.Target, query, s.engine().CatalogRead())
}

func TestPLpgSQLAssignmentAtomicity(t *testing.T) {
	t.Run("private_ancestors_and_source", func(t *testing.T) {
		e, s := plUnitScope(t)
		read := e.CatalogRead()
		array, _ := read.LookupType(1007)
		text, _ := read.LookupType(sqltype.Text)
		original, err := array.Input(context.Background(), "[0:2]={1,2,3}", -1)
		if err != nil {
			t.Fatal(err)
		}
		shape, err := sqltype.RecordShape([]sqltype.CompositeMember{{Attribute: 1, Name: "items", Usage: array.Usage()}, {Attribute: 2, Name: "sibling", Usage: text.Usage()}})
		if err != nil {
			t.Fatal(err)
		}
		row, err := sqltype.OwnedCompositeValue(shape, []sqltype.Value{original, sqltype.String(text, "keep")})
		if err != nil {
			t.Fatal(err)
		}
		f := &proc.PLFrame{Program: &proc.PLProgram{Variables: []proc.PLVariable{{Name: "a", Usage: shape.Usage()}}}, Values: []sqltype.Value{row}}
		defer f.Close()
		_, err = plUnitAssignQueryValue(t, s, f, "a.items[1]", sqltype.String(text, "bad"))
		domainUnitDiagnostic(t, err, "22P02", `invalid input syntax for type integer: "bad"`)
		if f.Values[0] != row || row.CompositeField(0) != original {
			t.Fatal("failed conversion changed ancestor/source")
		}
		result, err := plUnitAssignQueryValue(t, s, f, "a.items[1]", sqltype.String(text, "7"))
		if err != nil {
			t.Fatal(err)
		}
		output, err := array.Output(context.Background(), result.CompositeField(0))
		if err != nil || output != "[0:2]={1,7,3}" || result.CompositeField(1) != row.CompositeField(1) || result == row {
			t.Fatal("private reconstruction or sibling retention", output, err)
		}
		output, err = array.Output(context.Background(), original)
		if err != nil || output != "[0:2]={1,2,3}" || row.CompositeField(0) != original {
			t.Fatal("success mutated old ancestor or source", output, err)
		}
		retained := f.Values[0]
		evaluationContext, cancel := context.WithCancel(s.op.Ctx)
		cancel()
		previous := s.op.Ctx
		s.op.Ctx = evaluationContext
		_, err = plUnitAssignQueryValue(t, s, f, "a.items[1]", sqltype.String(text, "8"))
		s.op.Ctx = previous
		if !errors.Is(err, context.Canceled) || f.Values[0] != retained {
			t.Fatal("cancel published a candidate", err)
		}
	})
	for _, tc := range []struct{ name, typeName, initial, path, code string }{
		{"domain_array", "pa_unit_array", "{1,2}", "a[1]", "23514"},
		{"array_domain_elements", "_pa_unit_positive", "{1,2}", "a[1]", "23514"},
		{"array_domain_not_null", "_pa_unit_required", "{1,2}", "a[1]", "23502"},
		{"containing_composite_domain", "pa_unit_box_domain", "(\"{1,2}\",9)", "a.items[1]", "23514"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := New()
			op, done := roleOperation(t, e, "postgres")
			defer done()
			for _, ddl := range []string{
				"CREATE DOMAIN pa_unit_positive AS integer CHECK(VALUE>0)",
				"CREATE DOMAIN pa_unit_required AS integer NOT NULL",
				"CREATE DOMAIN pa_unit_array AS integer[] CHECK(VALUE[1]>0)",
				"CREATE TYPE pa_unit_box AS (items integer[], sibling integer)",
				"CREATE DOMAIN pa_unit_box_domain AS pa_unit_box CHECK((VALUE).items[1]>0)",
			} {
				domainUnitPublish(t, e, domainUnitStage(t, e, op, ddl))
			}
			s, err := e.StartStatement(op)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Finish(nil)
			e.mu.Lock()
			auth, err := e.authorizationLocked(op)
			s.routineAuth = auth
			e.mu.Unlock()
			if err != nil {
				t.Fatal(err)
			}
			read := e.CatalogRead()
			typ := domainUnitType(t, read, tc.typeName).SQLType
			text, _ := read.LookupType(sqltype.Text)
			source := sqltype.String(text, tc.initial)
			if typ.BaseType().IsComposite() {
				source, err = typ.BaseType().Input(s.op.Context(), tc.initial, -1)
				if err != nil {
					t.Fatal(err)
				}
			}
			initial, err := s.convertPLValue(source, typ.Usage())
			if err != nil {
				t.Fatal(err)
			}
			f := &proc.PLFrame{Program: &proc.PLProgram{Variables: []proc.PLVariable{{Name: "a", Usage: typ.Usage()}}}, Values: []sqltype.Value{initial}}
			defer f.Close()
			rhs := sqltype.String(text, "-1")
			if tc.code == "23502" {
				rhs = sqltype.Null(text)
			}
			_, err = plUnitAssignQueryValue(t, s, f, tc.path, rhs)
			var diagnostic *sqlstate.Error
			if !errors.As(err, &diagnostic) || diagnostic.Code != tc.code || f.Values[0] != initial {
				t.Fatal("domain rejection changed root or lost producer", err)
			}
		})
	}
	t.Run("rank_and_extent_failures", func(t *testing.T) {
		e, s := plUnitScope(t)
		read := e.CatalogRead()
		array, _ := read.LookupType(1007)
		integer, _ := read.LookupType(sqltype.Int4)
		initial, err := array.Input(s.op.Context(), "{{1,2},{3,4}}", -1)
		if err != nil {
			t.Fatal(err)
		}
		rhs, err := sqltype.Integer(integer, 7)
		if err != nil {
			t.Fatal(err)
		}
		f := &proc.PLFrame{Program: &proc.PLProgram{Variables: []proc.PLVariable{{Name: "a", Usage: array.Usage()}}}, Values: []sqltype.Value{initial}}
		defer f.Close()
		for _, tc := range []struct{ path, message string }{
			{"a[1]", "wrong number of array subscripts"},
			{"a[3][1]", "array subscript out of range"},
		} {
			_, err := plUnitAssignQueryValue(t, s, f, tc.path, rhs)
			domainUnitDiagnostic(t, err, "2202E", tc.message)
			if f.Values[0] != initial {
				t.Fatal("bound failure changed root", tc.path)
			}
		}
	})
	t.Run("query_cardinality", plUnitAssignmentCardinality)
}

func TestPLpgSQLAssignmentTargetUsage(t *testing.T) {
	e, s := plUnitScope(t)
	read := e.CatalogRead()
	array, _ := read.LookupType(1015)
	collation, err := sqltype.RegisteredCollation(6411, 6411)
	if err != nil {
		t.Fatal(err)
	}
	usage := array.Usage()
	usage.Typmod, usage.Collation = 7, collation // varchar(3), including its real collation capability.
	shape, err := sqltype.RecordShape([]sqltype.CompositeMember{{Attribute: 1, Name: "items", Usage: usage}})
	if err != nil {
		t.Fatal(err)
	}
	target := plUnitAssignmentTarget(t, "a.items[2]")
	e.mu.Lock()
	auth, err := e.authorizationLocked(s.op)
	b := newExpressionBinder(s.op.Context(), e, s.input, s.op, nil)
	b.Query.Auth = &auth
	bound, bindErr := b.BindAssignmentTarget(catalog.Column{Name: "a", Usage: shape.Usage()}, target.Steps, parser.Position{})
	e.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if bindErr != nil {
		t.Fatal(bindErr)
	}
	want := usage
	want.Type = array.Element()
	if bound.Container != shape.Usage() || bound.Next == nil || bound.Next.Container != usage || bound.Destination != want || analyze.TargetUsage(shape.Usage(), target.Steps) != want {
		t.Fatal("target discarded full usage", bound.Destination, want)
	}
	t.Run("local_composite_array_admission", func(t *testing.T) {
		e, s := plUnitScope(t)
		domainUnitPublish(t, e, domainUnitStage(t, e, s.op, "CREATE TYPE pa_unit_local AS (n integer)"))
		read := e.CatalogRead()
		integer, _ := read.LookupType(sqltype.Int4)
		language, err := catalog.LookupLanguage(read, "plpgsql")
		if err != nil {
			t.Fatal(err)
		}
		for _, tc := range []struct {
			annotation string
			valid      bool
		}{
			{"pa_unit_local[]", true},
			{"anyelement", false},
		} {
			source := "DECLARE a " + tc.annotation + "; BEGIN RETURN 1; END"
			raw, err := parser.ParsePLpgSQL(source, parser.PLReturnPolicy{})
			if err != nil {
				t.Fatal(err)
			}
			d := &catalog.Procedure{ObjectName: "local_array", RoutineKind: catalog.FunctionRoutine, Language: language.Address(), SQL: &proc.Definition{Kind: proc.PLpgSQLBody, Name: "local_array", Source: source, Result: integer.Usage(), PLSyntax: raw}}
			e.mu.Lock()
			auth, err := e.authorizationLocked(s.op)
			if err == nil {
				err = e.compilePLRoutineLocked(s.op, auth, d)
			}
			e.mu.Unlock()
			if !tc.valid {
				if err == nil {
					t.Fatal("nonconcrete local annotation accepted")
				}
				continue
			}
			if err != nil {
				t.Fatal("concrete composite array local rejected", err)
			}
			local := d.SQL.PL.Root.Statements[0].Block.Bindings["a"]
			usage := d.SQL.PL.Variables[local].Usage
			element := domainUnitType(t, read, "pa_unit_local").SQLType
			if !usage.Valid() || !usage.Type.IsArray() || usage.Type.Element() != element || len(d.DeclarationDependencies) == 0 {
				t.Fatal("local array lost type ownership or dependencies", usage)
			}
		}
	})
}

func TestPLpgSQLAssignmentBoundsOrder(t *testing.T) {
	for _, tc := range []struct{ path, input, code, message string }{
		{"a[NULL::integer]", "{1,2}", "22004", "array subscript in assignment must not be null"},
		{"a[1:NULL::integer]", "{1,2}", "22004", "array subscript in assignment must not be null"},
		{"a[2147483648::bigint]", "{1,2}", "22003", "integer out of range"},
		{"a[1]", "{1,2}", "22P02", `invalid input syntax for type integer: "bad"`},
	} {
		t.Run(tc.path, func(t *testing.T) {
			e, s := plUnitScope(t)
			read := e.CatalogRead()
			array, _ := read.LookupType(1007)
			text, _ := read.LookupType(sqltype.Text)
			initial, err := array.Input(context.Background(), tc.input, -1)
			if err != nil {
				t.Fatal(err)
			}
			f := &proc.PLFrame{Program: &proc.PLProgram{Variables: []proc.PLVariable{{Name: "a", Usage: array.Usage()}}}, Values: []sqltype.Value{initial}}
			defer f.Close()
			_, err = plUnitAssignQueryValue(t, s, f, tc.path, sqltype.String(text, "bad"))
			domainUnitDiagnostic(t, err, tc.code, tc.message)
			if f.Values[0] != initial {
				t.Fatal("failure published target")
			}
		})
	}
}

func TestPLpgSQLAssignmentPlanningOrigin(t *testing.T) {
	e, s := plUnitScope(t)
	read := e.CatalogRead()
	integer, _ := read.LookupType(sqltype.Int4)
	for _, tc := range []struct {
		sql      string
		target   sqltype.Usage
		planning bool
	}{
		{"SELECT 'bad'::text", integer.Usage(), true},
		{"SELECT ARRAY['bad']::text[]", integer.Usage(), false},
	} {
		t.Run(tc.sql, func(t *testing.T) {
			e.mu.Lock()
			auth, err := e.authorizationLocked(s.op)
			b := newExpressionBinder(s.op.Context(), e, s.input, s.op, nil)
			b.Query.Auth = &auth
			child, bindErr := b.Bind(statement(t, tc.sql).(*parser.Select).Targets[0], analyze.ExpressionPolicy{Context: analyze.ProjectionContext})
			var node expr.Expr
			if bindErr == nil {
				node, bindErr = b.PLAssignmentExpression(child, tc.target)
			}
			e.mu.Unlock()
			if err != nil {
				t.Fatal(err)
			}
			if bindErr != nil {
				t.Fatal(bindErr)
			}
			if !tc.planning && exec.PlannedVolatility(node) != expr.Stable {
				t.Fatal("array IO lost captured volatility")
			}
			evaluation := exec.Context{EvalStatement: exec.EvalStatement{Context: s.op.Context(), Host: hostOf(s), Catalog: auth.Catalog, Input: b.Input, Output: s.output}}
			prepared, _, preflightErr := exec.PreflightCoercions(&evaluation, node)
			var preflightPlanning *plan.PLAssignmentPlanningError
			if tc.planning {
				if !errors.As(preflightErr, &preflightPlanning) {
					t.Fatal("public preflight lost SQL-constant origin", preflightErr)
				}
			} else {
				if preflightErr != nil {
					t.Fatal("public preflight executed stable IO", preflightErr)
				}
				_, runtimeErr := prepared.Eval(&evaluation)
				if runtimeErr == nil || errors.As(runtimeErr, &preflightPlanning) {
					t.Fatal("public preflight changed runtime IO origin", runtimeErr)
				}
			}
			folded, _, err := exec.SimplifyConditional(&evaluation, node, false)
			var planning *plan.PLAssignmentPlanningError
			if tc.planning {
				if !errors.As(err, &planning) {
					t.Fatal("immutable constant lost planning provenance", err)
				}
			} else {
				if err != nil {
					t.Fatal("stable array IO planned early", err)
				}
				_, err = folded.Eval(&evaluation)
				if err == nil || errors.As(err, &planning) {
					t.Fatal("runtime IO misclassified", err)
				}
			}
		})
	}
	t.Run("dynamic_local", func(t *testing.T) {
		text, _ := read.LookupType(sqltype.Text)
		e.mu.Lock()
		auth, err := e.authorizationLocked(s.op)
		b := newExpressionBinder(s.op.Context(), e, s.input, s.op, nil)
		b.Query.Auth = &auth
		local := plan.PLLocalExpr{Info: expr.Info{Usage: text.Usage(), Property: expr.Properties{ParameterDependent: true}}, Value: sqltype.String(text, "bad")}
		node, bindErr := b.PLAssignmentExpression(local, integer.Usage())
		e.mu.Unlock()
		if err != nil {
			t.Fatal(err)
		}
		if bindErr != nil {
			t.Fatal(bindErr)
		}
		evaluation := exec.Context{EvalStatement: exec.EvalStatement{Context: s.op.Context(), Host: hostOf(s), Catalog: auth.Catalog, Input: b.Input, Output: s.output}}
		_, _, preflightErr := exec.PreflightCoercions(&evaluation, node)
		var preflightPlanning *plan.PLAssignmentPlanningError
		if errors.As(preflightErr, &preflightPlanning) {
			t.Fatal("public preflight classified dynamic local as SQL constant", preflightErr)
		}
		folded, _, err := exec.SimplifyConditional(&evaluation, node, false)
		if err != nil || !folded.Properties().ParameterDependent {
			t.Fatal("dynamic local planned as SQL constant", err)
		}
		_, err = folded.Eval(&evaluation)
		var planning *plan.PLAssignmentPlanningError
		if err == nil || errors.As(err, &planning) {
			t.Fatal("dynamic conversion lost runtime origin", err)
		}
	})
	t.Run("reached_queries", plUnitAssignmentQueryOrigins)
}

func plUnitAssignmentQueryOrigins(t *testing.T) {
	e, s := plUnitScope(t)
	for _, definition := range []string{
		"CREATE FUNCTION pa_origin_quoted() RETURNS integer LANGUAGE SQL AS 'SELECT 1/0'",
		"CREATE FUNCTION pa_origin_standard() RETURNS integer LANGUAGE SQL RETURN 1/0",
		"CREATE FUNCTION pa_origin_dynamic(text) RETURNS integer LANGUAGE SQL AS 'SELECT $1::integer'",
		"CREATE FUNCTION pa_origin_standard_dynamic(x text) RETURNS integer LANGUAGE SQL RETURN x::integer",
	} {
		raw := statement(t, definition).(*parser.CreateFunction)
		e.mu.Lock()
		auth, err := e.authorizationLocked(s.op)
		var stage *CatalogStage
		if err == nil {
			stage = e.BeginCatalogStage(auth.Catalog)
			err = routine.StageCreateFunction(ddlHost{e: e, op: s.op, stage: stage}, auth, raw, sqltype.InputEnvironment{})
		}
		e.mu.Unlock()
		if err != nil {
			t.Fatal("private routine fixture", err)
		}
		domainUnitPublish(t, e, stage)
	}
	domainUnitPublish(t, e, domainUnitStage(t, e, s.op, "CREATE DOMAIN pa_origin_domain AS integer CHECK(pa_origin_quoted()>0)"))
	read := e.CatalogRead()
	integer, _ := read.LookupType(sqltype.Int4)
	frame := &proc.PLFrame{Program: &proc.PLProgram{}}
	defer frame.Close()
	previous, depth := s.sqlRoutine, s.activeSQLRoutineDepth
	for _, tc := range []struct {
		query    string
		planning bool
	}{
		{"SELECT 'bad'::text", true},
		{"SELECT pa_origin_quoted()", true},
		{"SELECT pa_origin_standard()", true},
		{"SELECT pa_origin_dynamic('bad'::text)", false},
		{"SELECT pa_origin_standard_dynamic('bad'::text)", false},
		{"SELECT 1::pa_origin_domain", false},
	} {
		raw := statement(t, tc.query).(*parser.Select)
		_, _, _, err := s.evaluatePLExpression(frame, raw, read, &analyze.PLAssignment{Usage: integer.Usage()})
		var planning *plan.PLAssignmentPlanningError
		if err == nil || errors.As(err, &planning) != tc.planning {
			t.Fatal("reached query lost planning/runtime origin", tc.query, err)
		}
		if s.sqlRoutine != previous || s.activeSQLRoutineDepth != depth {
			t.Fatal("failed assignment query leaked borrowed demand/depth", tc.query)
		}
	}
	value, _, present, err := s.evaluatePLExpression(frame, statement(t, "SELECT pa_origin_dynamic('7'::text)").(*parser.Select), read, &analyze.PLAssignment{Usage: integer.Usage()})
	if err != nil || !present {
		t.Fatal("recovery after planning/runtime failures", err)
	}
	n, err := value.Integer()
	if err != nil || n != 7 || s.sqlRoutine != previous || s.activeSQLRoutineDepth != depth {
		t.Fatal("successful assignment query leaked state", value, err)
	}

	e.mu.Lock()
	auth, err := e.authorizationLocked(s.op)
	b := newExpressionBinder(s.op.Context(), e, s.input, s.op, nil)
	b.Query.Auth = &auth
	call, bindErr := b.Bind(statement(t, "SELECT pa_origin_quoted()").(*parser.Select).Targets[0], analyze.ExpressionPolicy{Context: analyze.ProjectionContext})
	e.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if bindErr != nil {
		t.Fatal(bindErr)
	}
	root := &plan.ProjectNode{QuerySchema: []catalog.Column{{Name: "v", Usage: integer.Usage()}}, Expressions: []expr.Expr{call}}
	decorated, err := plAssignmentProjection(root)
	if err != nil {
		t.Fatal(err)
	}
	private := decorated.(*plan.ProjectNode)
	_, originalCall := root.Expressions[0].(plan.SQLRoutineExpr)
	if private == root || !originalCall || &private.Expressions[0] == &root.Expressions[0] || private.Schema()[0] != root.Schema()[0] || private.Expressions[0].ResultUsage() != call.ResultUsage() {
		t.Fatal("borrowed origin mutated analyzed projection or Usage")
	}
	evaluation := exec.Context{EvalStatement: exec.EvalStatement{Context: s.op.Context(), Host: hostOf(s), Catalog: auth.Catalog, Input: b.Input, Output: s.output}}
	for _, domain := range []bool{false, true} {
		evaluation.DomainValueActive = domain
		_, err := private.Expressions[0].Eval(&evaluation)
		var planning *plan.PLAssignmentPlanningError
		if err == nil || errors.As(err, &planning) == domain || evaluation.PLAssignmentPlanning {
			t.Fatal("host origin propagation/domain suppression/restore", domain, err)
		}
	}
	if s.sqlRoutine != previous || s.activeSQLRoutineDepth != depth {
		t.Fatal("nested host invocation leaked caller demand")
	}
	t.Run("domain_preflight_barrier", func(t *testing.T) {
		e.mu.Lock()
		auth, err := e.authorizationLocked(s.op)
		if err != nil {
			e.mu.Unlock()
			t.Fatal(err)
		}
		previousAuth := s.routineAuth
		s.routineAuth = auth
		defer func() { s.routineAuth = previousAuth }()
		b := newExpressionBinder(s.op.Context(), e, s.input, s.op, nil)
		b.Query.Auth = &auth
		child, err := b.Bind(statement(t, "SELECT 1::pa_origin_domain").(*parser.Select).Targets[0], analyze.ExpressionPolicy{Context: analyze.ProjectionContext})
		var node expr.Expr
		if err == nil {
			node, err = b.PLAssignmentExpression(child, integer.Usage())
		}
		e.mu.Unlock()
		if err != nil {
			t.Fatal(err)
		}
		evaluation := exec.Context{EvalStatement: exec.EvalStatement{Context: s.op.Context(), Host: hostOf(s), Catalog: auth.Catalog, Input: b.Input, Output: s.output}}
		evaluation.Input.DomainChecks = &evaluation
		prepared, _, err := exec.PreflightCoercions(&evaluation, node)
		if err != nil {
			t.Fatal("public preflight executed domain truth", err)
		}
		_, err = prepared.Eval(&evaluation)
		var planning *plan.PLAssignmentPlanningError
		if err == nil || errors.As(err, &planning) || evaluation.PLAssignmentPlanning {
			t.Fatal("domain CHECK lost runtime origin or leaked demand", err)
		}
	})
	leaf := &sqlstate.Error{Code: "22012", Message: "division by zero"}
	for _, marked := range []bool{false, true} {
		var input error = leaf
		if marked {
			input = &plan.PLAssignmentPlanningError{Err: input}
		}
		annotated := exec.SQLRoutineStatementContext(input, "origin", 1)
		var planning *plan.PLAssignmentPlanningError
		var diagnostic *sqlstate.Error
		if errors.As(annotated, &planning) != marked || !errors.As(annotated, &diagnostic) || diagnostic.Context != `SQL function "origin" statement 1` || leaf.Context != "" {
			t.Fatal("SQL diagnostic cloning lost phase or mutated producer", annotated)
		}
	}
}

func plUnitAssignmentCardinality(t *testing.T) {
	e, s := plUnitScope(t)
	read := e.CatalogRead()
	array, _ := read.LookupType(1007)
	text, _ := read.LookupType(sqltype.Text)
	initial, err := array.Input(s.op.Context(), "{1,2}", -1)
	if err != nil {
		t.Fatal(err)
	}
	f := &proc.PLFrame{Program: &proc.PLProgram{Variables: []proc.PLVariable{{Name: "a", Usage: array.Usage(), NotNull: true}}}, Values: []sqltype.Value{initial}}
	defer f.Close()
	run := func(frame *proc.PLFrame, path, rhs string) (sqltype.Value, error) {
		raw, err := parser.ParsePLpgSQL("BEGIN "+path+" := "+rhs+"; END", parser.PLReturnPolicy{})
		if err != nil {
			t.Fatal(err)
		}
		statement := raw.Statements[0]
		query := &parser.Select{Targets: []parser.Expr{statement.Expression}, Pos: statement.Expression.SourcePosition()}
		return s.assignPLTarget(frame, plUnitAssignmentTarget(t, path), statement.Target, query, read)
	}
	_, err = run(f, "a[1]", "generate_series(1,0)")
	domainUnitDiagnostic(t, err, "22004", `null value cannot be assigned to variable "a" declared NOT NULL`)
	if f.Values[0] != initial {
		t.Fatal("empty result published through NOT NULL guard")
	}
	_, err = run(f, "a[1]", "generate_series(7,8)")
	domainUnitDiagnostic(t, err, "21000", "query returned more than one row")
	if f.Values[0] != initial {
		t.Fatal("multirow query published an intermediate result")
	}
	value, err := run(f, "a[1]", "generate_series(7,7)")
	if err != nil {
		t.Fatal("same-frame recovery", err)
	}
	output, err := array.Output(s.op.Context(), value)
	if err != nil || output != "{7,2}" {
		t.Fatal("recovery did not use old datum", output, err)
	}
	output, err = array.Output(s.op.Context(), initial)
	if err != nil || output != "{1,2}" {
		t.Fatal("cardinality failure/recovery mutated source", output, err)
	}
	f.Program.Variables[0].NotNull = false
	f.Values[0] = initial
	value, err = run(f, "a[NULL::integer]", "generate_series(1,0)")
	if err != nil || !value.IsNull() || value.Type() != array {
		t.Fatal("empty set evaluated bounds or lost typed root NULL", value, err)
	}
	f.Values[0] = initial
	value, err = run(f, "a[1]", "NULL::integer")
	if err != nil {
		t.Fatal(err)
	}
	output, err = array.Output(s.op.Context(), value)
	if err != nil || output != "{NULL,2}" {
		t.Fatal("actual NULL confused with empty result", output, err)
	}

	shape, err := sqltype.RecordShape([]sqltype.CompositeMember{{Attribute: 1, Name: "items", Usage: array.Usage()}, {Attribute: 2, Name: "sibling", Usage: text.Usage()}})
	if err != nil {
		t.Fatal(err)
	}
	row, err := sqltype.OwnedCompositeValue(shape, []sqltype.Value{initial, sqltype.String(text, "keep")})
	if err != nil {
		t.Fatal(err)
	}
	record := &proc.PLFrame{Program: &proc.PLProgram{Variables: []proc.PLVariable{{Name: "a", Usage: shape.Usage(), NotNull: true}}}, Values: []sqltype.Value{row}}
	defer record.Close()
	_, err = run(record, "a.items[1]", "generate_series(7,8)")
	domainUnitDiagnostic(t, err, "21000", "query returned more than one row")
	if record.Values[0] != row || row.CompositeField(0) != initial {
		t.Fatal("failed field publication changed retained ancestor")
	}
	value, err = run(record, "a.items[1]", "generate_series(1,0)")
	if err != nil || value.IsNull() || !value.CompositeField(0).IsNull() || value.CompositeField(1) != row.CompositeField(1) || row.CompositeField(0) != initial {
		t.Fatal("empty field datum nulled slot or changed sibling/source", value, err)
	}
	value, err = run(record, "a.items[1]", "generate_series(4,4)")
	if err != nil {
		t.Fatal("field recovery after empty datum", err)
	}
	output, err = array.Output(s.op.Context(), value.CompositeField(0))
	if err != nil || output != "{4}" || value.CompositeField(1) != row.CompositeField(1) {
		t.Fatal("field recovery/source retention", output, err)
	}

	for _, ddl := range []string{
		"CREATE DOMAIN pa_unit_card_root AS integer[] NOT NULL",
		"CREATE DOMAIN pa_unit_card_element AS integer NOT NULL",
	} {
		domainUnitPublish(t, e, domainUnitStage(t, e, s.op, ddl))
	}
	read = e.CatalogRead()
	rootDomain := domainUnitType(t, read, "pa_unit_card_root").SQLType
	domainInitial, err := s.convertPLValue(initial, rootDomain.Usage())
	if err != nil {
		t.Fatal(err)
	}
	domainFrame := &proc.PLFrame{Program: &proc.PLProgram{Variables: []proc.PLVariable{{Name: "a", Usage: rootDomain.Usage()}}}, Values: []sqltype.Value{domainInitial}}
	defer domainFrame.Close()
	value, err = run(domainFrame, "a[1]", "generate_series(1,0)")
	if err != nil || !value.IsNull() || value.Type() != rootDomain {
		t.Fatal("empty result re-input exact domain NULL", value, err)
	}
	elementArray := domainUnitType(t, read, "_pa_unit_card_element").SQLType
	elementInitial, err := s.convertPLValue(initial, elementArray.Usage())
	if err != nil {
		t.Fatal(err)
	}
	elementFrame := &proc.PLFrame{Program: &proc.PLProgram{Variables: []proc.PLVariable{{Name: "a", Usage: elementArray.Usage()}}}, Values: []sqltype.Value{elementInitial}}
	defer elementFrame.Close()
	_, err = run(elementFrame, "a[1]", "NULL::integer")
	domainUnitDiagnostic(t, err, "23502", "domain pa_unit_card_element does not allow null values")
	if elementFrame.Values[0] != elementInitial {
		t.Fatal("failed leaf domain input published root")
	}
	value, err = run(elementFrame, "a[1]", "generate_series(1,0)")
	if err != nil || !value.IsNull() || value.Type() != elementArray {
		t.Fatal("empty set evaluated leaf domain input", value, err)
	}

	query := statement(t, "SELECT generate_series(1,0)").(*parser.Select)
	target := plUnitAssignmentTarget(t, "a[1]")
	value, usage, present, err := s.evaluatePLExpression(f, query, read, &analyze.PLAssignment{Usage: array.Usage(), Current: initial, Steps: target.Steps, Name: "a"})
	if err != nil || present || usage != array.Usage() || !value.IsNull() || value.Type() != array {
		t.Fatal("query outcome lost cardinality/root Usage", value, usage, present, err)
	}
}
