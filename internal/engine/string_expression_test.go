package engine

import (
	"context"
	"errors"
	"reflect"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestStringCoreBinding(t *testing.T) {
	engine := New()
	binder := newTestExpressionBinder(t, context.Background(), engine, sqltype.InputEnvironment{}, nil)
	bind := func(query string) expr.Expr {
		t.Helper()
		raw := statement(t, query).(*parser.Select).Targets[0]
		node, err := binder.bind(raw, expressionPolicy{context: projectionContext})
		if err != nil {
			t.Fatal(query, err)
		}
		return node
	}
	first := bind("SELECT btrim(' x ')")
	if first.Properties().Volatility != expr.Immutable || first.ResultUsage().Type.ID() != sqltype.Text {
		t.Fatal(first)
	}
	if !binder.sameGroupExpression(first, bind("SELECT btrim(' x ')")) || binder.sameGroupExpression(first, bind("SELECT ltrim(' x ')")) || binder.sameGroupExpression(first, bind("SELECT btrim(' y ')")) {
		t.Fatal("string provider grouping identity")
	}
	for _, tc := range []struct {
		query      string
		volatility expr.Volatility
	}{
		{"SELECT 'a'||'b'", expr.Immutable}, {"SELECT 'a'||42", expr.Stable}, {"SELECT 'a'||setseed(0)", expr.Volatile},
		{"SELECT setseed(0)||'a'", expr.Volatile}, {"SELECT normalize($1)", expr.Immutable},
	} {
		node := bind(tc.query)
		if node.Properties().Volatility != tc.volatility {
			t.Fatal(tc, node.Properties())
		}
		if tc.query == "SELECT normalize($1)" && !node.Properties().ParameterDependent {
			t.Fatal("lost parameter dependence")
		}
	}
	original := bind("SELECT btrim(pg_catalog.normalize('é','NFC'))")
	folded, changed, err := preflightCoercions(&EvalContext{evalStatement: evalStatement{Context: context.Background()}}, original)
	if err != nil || !changed {
		t.Fatal(folded, changed, err)
	}
	if _, ok := original.(functions.String).Args[0].(functions.String); !ok {
		t.Fatal("preflight mutated analyzed children")
	}
	_, _, err = preflightCoercions(&EvalContext{evalStatement: evalStatement{Context: context.Background()}}, bind("SELECT btrim(NULL,pg_catalog.normalize('x','bad'))"))
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) || diagnostic.Code != "22023" || diagnostic.Position != "" {
		t.Fatal("immutable sibling before strict NULL", err)
	}
}

func TestStringCorePropertiesAndWalkers(t *testing.T) {
	var text *sqltype.Descriptor
	for _, d := range sqltype.Builtins() {
		if d.ID() == sqltype.Text {
			text = d
		}
	}
	stringPlan, err := sqltype.BindString("||", text.Usage(), text.Usage(), []sqltype.Usage{text.Usage(), text.Usage()}, [4]bool{true, true}, false)
	if err != nil {
		t.Fatal(err)
	}
	// Keep genuine owner retag/walker integration here; runtime demand is
	// exercised locally by functions.TestStringRuntimeDemand.
	node := functions.String{Info: expr.Info{Usage: text.Usage()}, Plan: stringPlan, Textual: [4]bool{true, true}, Args: []expr.Expr{
		plan.LiteralExpr{expr.Info{Usage: text.Usage()}, sqltype.String(text, "a")},
		plan.LiteralExpr{expr.Info{Usage: text.Usage()}, sqltype.String(text, "b")},
	}}
	var replacement *sqltype.Descriptor
	for _, d := range sqltype.Builtins() {
		if d.ID() == sqltype.Text {
			replacement = d
		}
	}
	rebound, changed, err := retagBoundTypes(New().CatalogRead(), node, func(d *sqltype.Descriptor) *sqltype.Descriptor {
		if d == text {
			return replacement
		}
		return d
	})
	if err != nil || !changed {
		t.Fatal(rebound, changed, err)
	}
	got, err := rebound.Eval(&EvalContext{evalStatement: evalStatement{Context: context.Background()}})
	if err != nil || got.Type() != replacement {
		t.Fatal("retained provider ownership", got, err)
	}
	s, _ := got.Text()
	if s != "ab" {
		t.Fatal(s)
	}
	if node.ResultUsage().Type != text || node.Plan.TextUsage().Type != text {
		t.Fatal("retag mutated original generation")
	}
	nested := node
	nested.Args = append([]expr.Expr(nil), node.Args...)
	nested.Args[1] = plan.ColumnExpr{Info: expr.Info{Usage: text.Usage(), Pos: parser.Position{Character: 17}, Property: expr.Properties{RowDependent: true}}, Scope: 1, Frame: 1}
	nested.Pos = parser.Position{Character: 8}
	nested.Property.RowDependent = true
	if position := firstQueryBoundFrameVariable(nested, 1); position.Character != 17 {
		t.Fatal("nested string query-bound variable", position)
	}
}

func TestStringCoreMissingOperatorOwner(t *testing.T) {
	engine := New()
	for _, tc := range []struct{ query, message, position string }{
		{"SELECT 1 || 2", "operator does not exist: integer || integer", "10"},
	} {
		binder := newTestExpressionBinder(t, context.Background(), engine, sqltype.InputEnvironment{}, nil)
		raw := statement(t, tc.query).(*parser.Select).Targets[0]
		_, err := binder.bind(raw, expressionPolicy{context: projectionContext})
		var got *sqlstate.Error
		want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42883", Message: tc.message, Position: tc.position, Hint: "No operator matches the given name and argument types. You might need to add explicit type casts."}
		if !errors.As(err, &got) || *got != want {
			t.Fatal("full missing operator diagnostic", got, err)
		}
	}
}

func TestStringCoreRegclassSnapshotManifest(t *testing.T) {
	engine := New()
	session := sessionStart(t, engine, nil)
	sessionSQL(t, session, "CREATE TABLE regclass_source(v INT)")
	sessionSQL(t, session, `CREATE TABLE regclass_store(v TEXT DEFAULT btrim(' stored '),CHECK(('x'::text||'regclass_source'::regclass) IS NORMALIZED))`)
	view := engine.CatalogRead()
	ref := catalogRef(t, engine, view, "regclass_store")
	relation := view.MustRelation(ref.Address)
	if relation.ColumnMeta.Defaults[relation.Attributes[0]].CatalogSnapshot || !relation.Constraints.Check[0].CommentSnapshot {
		t.Fatal("stored CHECK lost REGCLASS snapshot demand or ordinary default gained it")
	}
	broken := *relation
	broken.Constraints.Check = append([]catalog.CheckConstraint(nil), relation.Constraints.Check...)
	broken.Constraints.Check[0].CommentSnapshot = false
	if err := validateChecks(view, &broken, map[string]bool{}); err == nil {
		t.Fatal("invalid stored snapshot manifest accepted")
	}
	sourceRef := catalogRef(t, engine, view, "regclass_source")
	source := view.MustRelation(sourceRef.Address)
	scope := &StatementScope{inquiry: inquirySnapshot{commentCatalog: view, deparsePath: []ids.ObjectAddress{source.Namespace()}}}
	got, err := scope.FormatRegclass(uint32(sourceRef.Address.ObjectID))
	if err != nil || got != "regclass_source" {
		t.Fatal(got, err)
	}
	got, err = scope.FormatRegclass(4294967294)
	if err != nil || got != "4294967294" {
		t.Fatal("nonexistent OID presentation", got, err)
	}
	if _, err := (&StatementScope{}).FormatRegclass(uint32(sourceRef.Address.ObjectID)); err == nil {
		t.Fatal("missing snapshot silently fell back")
	}
	sessionSQL(t, session, "INSERT INTO regclass_store DEFAULT VALUES")
	sessionSQL(t, session, "ALTER TABLE regclass_store ALTER COLUMN v TYPE TEXT")
}

func TestStringCoreOrdinaryDeparseDoesNotChangeBoundArguments(t *testing.T) {
	e := New()
	run(t, e, "CREATE TABLE deparse_args(v CHAR(4),s CHAR(4),e TEXT,CHECK(btrim(v,s)=e))")
	ref := roleTableRef(t, e, "deparse_args")
	relation := e.catalog.state.MustRelation(ref.Address)
	node := relation.Constraints.Check[0].Expression.(plan.ComparisonExpr).Left.(functions.String)
	args := append([]expr.Expr(nil), node.Args...)
	stringPlan := node.Plan
	for _, pretty := range []bool{false, true} {
		renderer := pgRenderer{view: e.CatalogRead(), relation: relation, pretty: pretty}
		got, err := renderer.sql(context.Background(), node, pgRenderParent{})
		want := "btrim((v)::text, (s)::text)"
		if pretty {
			want = "btrim(v::text, s::text)"
		}
		if err != nil || got != want {
			t.Fatal(pretty, got, err)
		}
		// These row-dependent arguments have no evaluation frame. Rendering
		// must only present the provider's conversion, never evaluate or wrap it.
		if node.Plan != stringPlan || !reflect.DeepEqual(node.Args, args) {
			t.Fatal("rendering changed the retained provider or arguments")
		}
		for _, arg := range node.Args {
			if _, ok := arg.(plan.ColumnExpr); !ok || arg.ResultUsage().Type.ID() != sqltype.BPChar {
				t.Fatal("renderer installed a runtime conversion", arg)
			}
		}
	}
}

func TestStringCoreBinaryTrimSyntaxCopies(t *testing.T) {
	e := New()
	run(t, e, "CREATE TABLE trim_copies(v BYTEA,s BYTEA,e BYTEA,CHECK(TRIM(LEADING s FROM v)=e))")
	ref := roleTableRef(t, e, "trim_copies")
	relation := e.catalog.state.MustRelation(ref.Address)
	node := relation.Constraints.Check[0].Expression.(plan.ComparisonExpr).Left.(functions.Binary)
	if node.Syntax != parser.SQLFunctionSyntax {
		t.Fatal("binding lost special binary TRIM")
	}
	binder := newTestExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
	ordinary := node
	ordinary.Syntax = parser.OrdinaryFunctionSyntax
	if !binder.sameGroupExpression(node, ordinary) {
		t.Fatal("presentation syntax changed binary semantic identity")
	}
	grouped, err := binder.rewriteGroup(node, node.Args)
	if err != nil || grouped.(functions.Binary).Syntax != parser.SQLFunctionSyntax {
		t.Fatal("aggregate rewrite lost syntax", grouped, err)
	}
	remapped, err := remapBoundExpression(node, func(ref ids.ObjectRef) ids.ObjectRef { return ref }, map[int32]int{1: 1, 2: 0})
	if err != nil || remapped.(functions.Binary).Syntax != parser.SQLFunctionSyntax ||
		remapped.(functions.Binary).Args[0].(plan.ColumnExpr).Slot != 1 || node.Args[0].(plan.ColumnExpr).Slot != 0 {
		t.Fatal("remap lost syntax or mutated original children", remapped, err)
	}
	var replacement *sqltype.Descriptor
	for _, d := range sqltype.Builtins() {
		if d.ID() == sqltype.Bytea {
			replacement = d
		}
	}
	rebound, changed, err := retagBoundTypes(New().CatalogRead(), node, func(d *sqltype.Descriptor) *sqltype.Descriptor {
		if d.ID() == sqltype.Bytea {
			return replacement
		}
		return d
	})
	if err != nil || !changed || rebound.(functions.Binary).Syntax != parser.SQLFunctionSyntax || rebound.ResultUsage().Type != replacement {
		t.Fatal("retag lost syntax or provider ownership", rebound, changed, err)
	}
	folded, _, err := preflightCoercions(&EvalContext{evalStatement: evalStatement{Context: context.Background()}}, node)
	if err != nil || folded.(functions.Binary).Syntax != parser.SQLFunctionSyntax {
		t.Fatal("preflight lost syntax", folded, err)
	}
	got, err := (scalarRenderer{owner: relation}).sql(context.Background(), e.CatalogRead(), node)
	if err != nil || got != `TRIM(LEADING "s" FROM "v")` {
		t.Fatal("canonical replay lost special binary TRIM", got, err)
	}
	invalid := node
	invalid.Syntax = parser.FunctionSyntax(255)
	if _, err := (scalarRenderer{owner: relation}).sql(context.Background(), e.CatalogRead(), invalid); err == nil {
		t.Fatal("stored binary node accepted unrelated special syntax")
	}
}
