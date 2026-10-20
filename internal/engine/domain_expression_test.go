package engine

import (
	"context"
	"strconv"
	"strings"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestDomainExpressionLogicalAndSignatureIdentity(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	for _, q := range []string{"CREATE DOMAIN public.a AS integer", "CREATE DOMAIN public.b AS integer", "CREATE DOMAIN public.label AS text", "CREATE DOMAIN public.flag AS boolean"} {
		domainUnitPublish(t, e, domainUnitStage(t, e, op, q))
	}
	domainUnitTable(t, e, op, "CREATE TABLE public.one(id integer)")
	a := domainUnitType(t, e.CatalogRead(), "a")
	label := domainUnitType(t, e.CatalogRead(), "label")
	for _, tc := range []struct {
		expr      string
		id        sqltype.ID
		isNull    bool
		parameter sqltype.ID
	}{
		{"7::public.a", a.SQLType.ID(), false, 0},
		{"(7::public.a)+1", sqltype.Int4, false, 0},
		{"((7::public.a)+1)::public.a", a.SQLType.ID(), false, 0},
		{"btrim(' hi '::public.label)", sqltype.Text, false, 0},
		{"COALESCE(NULL::public.a,7::public.a)", a.SQLType.ID(), false, 0},
		{"COALESCE(NULL::public.a,7)", sqltype.Int4, false, 0},
		{"COALESCE(NULL::public.a,NULL::public.b)", sqltype.Int4, true, 0},
		{"CASE WHEN TRUE THEN NULL::public.a ELSE NULL::public.a END", a.SQLType.ID(), true, 0},
		{"7::public.a=$1", sqltype.Bool, false, sqltype.Int4},
		{"TRUE::public.flag AND TRUE", sqltype.Bool, false, 0},
	} {
		b := newTestExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
		raw := statement(t, "SELECT "+tc.expr+" FROM public.one").(*parser.Select).Targets[0]
		node, err := b.bind(raw, expressionPolicy{context: projectionContext})
		if err != nil {
			t.Fatal(tc.expr, err)
		}
		if node.ResultUsage().Type.ID() != tc.id {
			t.Fatal(tc.expr, node.ResultUsage())
		}
		parameters, err := b.finish()
		if err != nil {
			t.Fatal(err)
		}
		if tc.parameter != 0 {
			if parameters.Count != 1 || parameters.Types[0].Type.ID() != tc.parameter {
				t.Fatal(tc.expr, parameters)
			}
		} else {
			value, err := node.Eval(&EvalContext{evalStatement: evalStatement{Context: context.Background()}})
			if err != nil || value.Type() != node.ResultUsage().Type || value.IsNull() != tc.isNull {
				t.Fatal(tc.expr, value, err)
			}
			if strings.HasPrefix(tc.expr, "btrim") {
				text, err := value.Text()
				if err != nil || text != "hi" {
					t.Fatal(text, err)
				}
			}
		}
		required := a
		if strings.Contains(tc.expr, "public.label") {
			required = label
		}
		if strings.Contains(tc.expr, "public.a") || strings.Contains(tc.expr, "public.label") {
			found := false
			for _, dependency := range b.dependencies {
				if dependency.Ref.Address == required.Address() && dependency.Revision == required.Revision() {
					found = true
				}
			}
			if !found {
				t.Fatal("coercion erased logical type source dependency", tc.expr, b.dependencies)
			}
		}
	}
	query := domainUnitBindSelect(t, e, op, "SELECT bool_and(TRUE::public.flag) FROM public.one")
	project, ok := query.Root.(*projectNode)
	if !ok {
		t.Fatal("aggregate query lacks projection", query.Root)
	}
	aggregate, ok := project.child.(*aggregateNode)
	if !ok || len(aggregate.calls) != 1 || query.Root.Schema()[0].Usage.Type.ID() != sqltype.Bool || aggregate.calls[0].argument.ResultUsage().Type.ID() != sqltype.Bool {
		t.Fatal("aggregate did not downcast signature", project.child, query.Root.Schema())
	}
	argument, err := aggregate.calls[0].argument.Eval(&EvalContext{evalStatement: evalStatement{Context: context.Background()}})
	if err != nil {
		t.Fatal(err)
	}
	provider := aggregate.calls[0].provider
	state, err := provider.Init(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err = provider.Advance(context.Background(), state, argument, sqltype.Value{}); err != nil {
		t.Fatal(err)
	}
	value, err := provider.Finalize(context.Background(), state)
	if err != nil {
		t.Fatal(err)
	}
	truth, err := value.Boolean()
	if err != nil || !truth {
		t.Fatal(value, err)
	}
}

func TestDomainStoredCastUsesCapturedRenamedNamespace(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	domainUnitPublish(t, e, domainUnitStage(t, e, op, "CREATE DOMAIN public.a AS integer"))
	domainUnitTable(t, e, op, "CREATE TABLE public.one(id integer)")
	b := newTestExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
	ast := statement(t, "SELECT 7::public.a FROM public.one").(*parser.Select)
	node, err := b.bind(ast.Targets[0], expressionPolicy{context: projectionContext})
	if err != nil {
		t.Fatal(err)
	}
	if err = b.captureViewLiterals(ast.Targets[0], node, viewTargetLiteral, 0); err != nil {
		t.Fatal(err)
	}
	renderer := viewRenderer{viewDefinition: &viewDefinition{literals: b.viewLiterals, castUsages: b.viewCastUsages}, frame: b.frameID()}
	old := e.CatalogRead()
	domainUnitPublish(t, e, domainUnitStage(t, e, op, "ALTER SCHEMA public RENAME TO moved"))
	text, err := renderer.expressionSQL(context.Background(), e.CatalogRead(), ast.Targets[0], viewLiteralPath{clause: viewTargetLiteral})
	if err != nil || text != `CAST(7 AS "moved"."a")` {
		t.Fatal(text, err)
	}
	text, err = renderer.expressionSQL(context.Background(), old, ast.Targets[0], viewLiteralPath{clause: viewTargetLiteral})
	if err != nil || text != `CAST(7 AS "public"."a")` {
		t.Fatal("retained rendering authority", text, err)
	}
	// Use a coherent path that does not expose the domain's namespace, so
	// canonical presentation must spell the current qualified identity.
	renderer.presentation = viewPresentation{deparse: true, pretty: true, path: []ids.ObjectAddress{{ClassID: ids.NamespaceClassID, ObjectID: ids.PGCatalogNamespaceOID}}}
	text, err = renderer.expressionSQL(context.Background(), e.CatalogRead(), ast.Targets[0], viewLiteralPath{clause: viewTargetLiteral})
	if err != nil || text != "7::moved.a" {
		t.Fatal("renamed domain canonical cast", text, err)
	}
	text, err = renderer.expressionSQL(context.Background(), old, ast.Targets[0], viewLiteralPath{clause: viewTargetLiteral})
	if err != nil || text != "7::public.a" {
		t.Fatal("retained domain canonical authority", text, err)
	}
}

func TestDomainContextualLiteralTypmodEvaluation(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	domainUnitPublish(t, e, domainUnitStage(t, e, op, "CREATE DOMAIN public.short AS varchar(3)"))
	domainUnitTable(t, e, op, "CREATE TABLE public.one(id integer)")
	target := domainUnitType(t, e.CatalogRead(), "short").SQLType.Usage()
	b := newTestExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
	raw := statement(t, "SELECT 'abcd' FROM public.one").(*parser.Select).Targets[0]
	node, err := b.bind(raw, expressionPolicy{context: assignmentContext, expected: target, mode: sqltype.Assignment})
	if err != nil {
		t.Fatal("assignment typmod evaluated while binding", err)
	}
	coercion, ok := node.(coercionExpression)
	if !ok || coercion.child.ResultUsage().Type.ID() != sqltype.VarChar || node.ResultUsage() != target {
		t.Fatal("literal did not preserve base-to-domain coercion", node)
	}
	_, err = node.Eval(&EvalContext{evalStatement: evalStatement{Context: context.Background()}})
	domainUnitDiagnostic(t, err, "22001", "value too long for type character varying(3)")
	raw = statement(t, "SELECT 'abcd'::public.short FROM public.one").(*parser.Select).Targets[0]
	node, err = b.bind(raw, expressionPolicy{context: projectionContext})
	if err != nil {
		t.Fatal(err)
	}
	value, err := node.Eval(&EvalContext{evalStatement: evalStatement{Context: context.Background()}})
	if err != nil || value.Type() != target.Type {
		t.Fatal(value, err)
	}
	base, err := value.BaseValue()
	if err != nil || base.Type().ID() != sqltype.VarChar {
		t.Fatal("domain did not retain varchar base authority", base, err)
	}
	text, err := base.Text()
	if err != nil || text != "abc" {
		t.Fatal(text, err)
	}
}

func TestDomainQualifiedBuiltinNameModifierIdentity(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	domainUnitPublish(t, e, domainUnitStage(t, e, op, "CREATE DOMAIN public.uuid AS integer"))
	domainUnitTable(t, e, op, "CREATE TABLE public.one(id integer)")
	b := newTestExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
	raw := statement(t, "SELECT 1::public.uuid(3) FROM public.one").(*parser.Select).Targets[0]
	_, err := b.bind(raw, expressionPolicy{context: projectionContext})
	domainUnitDiagnostic(t, err, "42601", `type modifier is not allowed for type "public.uuid"`)
}

func TestDomainRegclassQueryOutputCapturesFormatter(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	domainUnitPublish(t, e, domainUnitStage(t, e, op, "CREATE DOMAIN public.relation_name AS regclass"))
	domainUnitTable(t, e, op, "CREATE TABLE public.t(v public.relation_name)")
	query := domainUnitBindSelect(t, e, op, "SELECT v FROM public.t")
	if !query.regclassOutput {
		t.Fatal("domain output lacks captured REGCLASS presentation demand", query)
	}
}

func TestDomainRegclassOutputSnapshotRepresentations(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	domainUnitPublish(t, e, domainUnitStage(t, e, op, "CREATE DOMAIN public.relation_name AS regclass"))
	domain := domainUnitType(t, e.CatalogRead(), "relation_name")
	table := domainUnitTable(t, e, op, "CREATE TABLE public.t(v public.relation_name)")
	regclass, _ := e.CatalogRead().LookupType(sqltype.Regclass)
	value, err := domain.SQLType.Wrap(sqltype.String(regclass, strconv.FormatUint(uint64(table.Address().ObjectID), 10)))
	if err != nil {
		t.Fatal(err)
	}
	base, err := value.BaseValue()
	if err != nil {
		t.Fatal(err)
	}
	for _, representation := range []string{"flat", "anonymous", "selected source"} {
		t.Run(representation, func(t *testing.T) {
			rows := newFinalRows(table.Columns, 1)
			switch representation {
			case "flat":
				rows.values = []sqltype.Value{value}
			case "anonymous":
				rows.anonymous = [][]sqltype.Value{{value}}
			case "selected source":
				rows.source = &querySource{committed: []storage.RowVersion{{Values: []sqltype.Value{sqltype.Null(domain.SQLType)}}, {Values: []sqltype.Value{value}}}}
				rows.selection = []int{1}
				rows.slots = []int{0}
			}
			if err := e.captureRegclassOutput(op, rows); err != nil {
				t.Fatal(err)
			}
			text, err := rows.RegclassText(base)
			if err != nil || text != "t" || rows.position != 0 {
				t.Fatal("snapshot capture changed cursor or missed output", text, rows.position, err)
			}
		})
	}
}

func TestDomainArrayBoundLiteralDiagnosticPosition(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	domainUnitPublish(t, e, domainUnitStage(t, e, op, "CREATE DOMAIN public.a AS name[]"))
	domainUnitTable(t, e, op, "CREATE TABLE public.one(id integer)")
	b := newTestExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
	raw := statement(t, "SELECT '[1:0]={}'::public.a FROM public.one").(*parser.Select).Targets[0]
	_, err := b.bind(raw, expressionPolicy{context: projectionContext})
	domainUnitDiagnostic(t, err, "2202E", "upper bound cannot be less than lower bound")
	diagnostic, ok := err.(*sqlstate.Error)
	if !ok || diagnostic.Position != "8" {
		t.Fatal("array bound input lost literal source position", err)
	}
}

func domainUnitBindSelect(t *testing.T, e *Engine, op *Operation, q string) *BoundQuery {
	t.Helper()
	raw := statement(t, q).(*parser.Select)
	e.mu.Lock()
	auth, err := e.authorizationLocked(op)
	b := newExpressionBinder(op.Context(), e, sqltype.InputEnvironment{}, op, nil)
	b.queryAuth = &auth
	var query *BoundQuery
	if err == nil {
		query, err = b.bindSelect(raw, false)
	}
	e.mu.Unlock()
	if err != nil {
		t.Fatal(q, err)
	}
	return query
}

func TestDomainIntervalLiteralCastInputUsage(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	domainUnitTable(t, e, op, "CREATE TABLE public.one(id integer)")
	for _, tc := range []struct{ name, base, input, output string }{
		{"days", "interval day", "1", "1 day"},
		{"hours", "interval hour", "1", "01:00:00"},
		{"months", "interval month", "1", "1 mon"},
		{"range_clock", "interval minute to second(2)", "1:30.126", "00:01:30.13"},
		{"chain", "public.days", "1", "1 day"},
	} {
		domainUnitPublish(t, e, domainUnitStage(t, e, op, "CREATE DOMAIN public."+tc.name+" AS "+tc.base))
		target := domainUnitType(t, e.CatalogRead(), tc.name).SQLType.Usage()
		for _, expression := range []string{"'" + tc.input + "'::public." + tc.name, "CAST('" + tc.input + "' AS public." + tc.name + ")"} {
			b := newTestExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
			raw := statement(t, "SELECT "+expression+" FROM public.one").(*parser.Select).Targets[0]
			node, err := b.bind(raw, expressionPolicy{context: projectionContext})
			if err != nil {
				t.Fatal(expression, err)
			}
			coercion, ok := node.(coercionExpression)
			if !ok || node.ResultUsage() != target || coercion.child.ResultUsage() != target.BaseUsage() {
				t.Fatal("lost effective input usage or logical cast identity", expression, node)
			}
			value, err := node.Eval(&EvalContext{evalStatement: evalStatement{Context: context.Background()}})
			if err != nil || value.Type() != target.Type {
				t.Fatal(expression, value, err)
			}
			text, err := target.Type.Output(context.Background(), value)
			if err != nil || text != tc.output {
				t.Fatal(expression, text, err)
			}
		}
	}
}
