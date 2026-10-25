package engine

import (
	"context"
	"errors"
	"strings"
	"testing"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func TestJSONBRecordBinding(t *testing.T) {
	e := New()
	b := newTestExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
	e.Mu.Lock()
	defer e.Mu.Unlock()
	auth, err := e.AuthorizationLocked(binderOperation(b))
	if err != nil {
		t.Fatal(err)
	}
	b.Query.Auth = &auth
	b.Query.Frame = 1
	definition := []parser.FunctionColumnDefinition{
		{Name: parser.Identifier{Text: "a"}, Type: parser.TypeName{Name: "integer"}},
		{Name: parser.Identifier{Text: "b"}, Type: parser.TypeName{Name: "text"}},
	}
	literal := parser.LiteralExpr{Value: parser.Value{Kind: parser.StringValue, Text: `{"a":7,"b":"x"}`}}
	raw := parser.FunctionExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "jsonb_to_record"}}}, Args: []parser.Expr{literal}, ColumnDefinitions: definition}
	node, ns, err := b.BindFrom(&parser.FunctionTable{Calls: []parser.FunctionExpr{raw}})
	if err != nil {
		t.Fatal(err)
	}
	source, ok := node.(*plan.FunctionFromNode)
	if !ok || len(source.Calls) != 1 || len(ns.Columns) != 2 || ns.Columns[0].Descriptor.Usage.Type.ID() != sqltype.Int4 || ns.Columns[1].Descriptor.Usage.Type.ID() != sqltype.Text {
		t.Fatal("lost definition usage", node, ns)
	}
	call := source.Calls[0].(functions.JSONBRecord)
	if exec.SourceReturnsSet(call) || !exec.SourceAdmitted(call) || !call.Definitions || call.Unresolved || call.Properties().Volatility != expr.Stable {
		t.Fatal("invalid scalar properties", call)
	}
	eval := &exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}}
	v, err := call.Eval(eval)
	if err != nil {
		t.Fatal(err)
	}
	if n, err := v.CompositeField(0).Integer(); err != nil || n != 7 {
		t.Fatal(n, err)
	}
	if s, err := v.CompositeField(1).Text(); err != nil || s != "x" {
		t.Fatal(s, err)
	}
	fresh := make(map[sqltype.ID]*sqltype.Descriptor)
	for _, d := range sqltype.Builtins() {
		fresh[d.ID()] = d
	}
	mapped, changed, err := stored.RetagTypes(auth.Catalog, call, func(d *sqltype.Descriptor) *sqltype.Descriptor {
		if next := fresh[d.ID()]; next != nil {
			return next
		}
		return d
	})
	if err != nil || !changed {
		t.Fatal("record generation not remapped", err)
	}
	next := mapped.(functions.JSONBRecord)
	if next.ResultUsage().Type == call.ResultUsage().Type || next.ResultUsage().Type.CompositeMember(0).Usage.Type != fresh[sqltype.Int4] || next.Args[0].ResultUsage().Type != fresh[sqltype.JSONB] {
		t.Fatal("source-owned record usage retained")
	}
	value, err := next.Eval(eval)
	if err != nil || value.Type() != next.ResultUsage().Type || value.CompositeField(0).Type() != fresh[sqltype.Int4] {
		t.Fatal(value, err)
	}
	if !analyze.SameGroupExpression(call, call) {
		t.Fatal("record equality lost")
	}
	if exec.PlannedVolatility(call) != expr.Stable {
		t.Fatal("record incorrectly immutable")
	}
	raw.Name.Parts[0].Text = "jsonb_to_recordset"
	raw.Args[0] = parser.LiteralExpr{Value: parser.Value{Kind: parser.StringValue, Text: `[{"a":1}]`}}
	node, _, err = b.BindFrom(&parser.FunctionTable{Calls: []parser.FunctionExpr{raw}, Ordinality: true})
	if err != nil {
		t.Fatal(err)
	}
	set := node.(*plan.FunctionFromNode).Calls[0]
	if !exec.SourceReturnsSet(set) || exec.FunctionFromCallWidth(set) != 2 || !set.Properties().StageDependent {
		t.Fatal("recordset placement/width", set)
	}
	cursor, err := exec.OpenFunctionSource(eval, set)
	if err != nil {
		t.Fatal(err)
	}
	cursor.Close()
	if cursor.Values != nil || !cursor.Exhausted {
		t.Fatal("recordset rows retained after close")
	}
	raw.ColumnDefinitions = nil
	_, _, err = b.BindFrom(&parser.FunctionTable{Calls: []parser.FunctionExpr{raw}})
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) || diagnostic.Code != "42601" {
		t.Fatal("missing source definition", err)
	}
	definition[1].Name.Text = "a"
	if _, err := b.BindJSONBRecordDefinition(definition); !errors.As(err, &diagnostic) || diagnostic.Code != "42701" {
		t.Fatal("duplicate definition", err)
	}
	// Parsing ordinary ROWS FROM names for diagnostic precedence must not
	// permit a resolved but unselected scalar source to execute.
	unselected := parser.FunctionExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "abs"}}}, Args: []parser.Expr{parser.LiteralExpr{Value: parser.Value{Kind: parser.IntegerValue, Integer: 1}}}}
	_, _, err = b.BindMixedFunctionTable([]parser.FunctionExpr{unselected, raw}, false, 0, nil, false)
	if !errors.As(err, &diagnostic) || diagnostic.Code != "0A000" || diagnostic.Message != "function table source is unsupported" {
		t.Fatal("ordinary scalar source admission widened", err)
	}
}

func TestJSONBRecordDomainErrors(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	for _, q := range []string{
		`CREATE DOMAIN public.jbr_parent AS integer CONSTRAINT jbr_parent_check CHECK(VALUE>0)`,
		`CREATE DOMAIN public.jbr_child AS public.jbr_parent`,
		`CREATE DOMAIN public.jbr_cast AS text CONSTRAINT jbr_cast_check CHECK(VALUE::integer>0)`,
		`CREATE DOMAIN public.jbr_nested AS text CONSTRAINT jbr_nested_check CHECK(VALUE::public.jbr_parent>0)`,
		`CREATE TYPE public.jbr_record AS (d public.jbr_cast)`,
	} {
		domainUnitPublish(t, e, domainUnitStage(t, e, op, q))
	}
	view := e.CatalogRead()
	eval := &exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background(), Catalog: view}}
	parent := domainUnitType(t, view, "jbr_parent").SQLType
	child := domainUnitType(t, view, "jbr_child").SQLType
	castDefinition := domainUnitType(t, view, "jbr_cast")
	cast := castDefinition.SQLType
	nested := domainUnitType(t, view, "jbr_nested").SQLType
	integer, _ := view.LookupType(sqltype.Int4)
	array, ok := view.LookupType(castDefinition.Array)
	if !ok {
		t.Fatal("missing captured domain array")
	}
	record := domainUnitType(t, view, "jbr_record").SQLType
	for _, tc := range []struct {
		name, input, code, dataType string
		target                      *sqltype.Descriptor
		soft                        bool
	}{
		{"field", "bad", "22P02", "", integer, true},
		{"own check", "-1", "23514", "jbr_parent", parent, true},
		{"inherited check", "-1", "23514", "jbr_child", child, true},
		{"CHECK-body cast", "bad", "22P02", "", cast, false},
		{"own text check", "-1", "23514", "jbr_cast", cast, true},
		{"nested other domain", "-1", "23514", "jbr_parent", nested, false},
		{"array text child", "{bad}", "22P02", "", array, false},
		{"composite text child", "(bad)", "22P02", "", record, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := tc.target.InputWithEnvironment(eval.Context, tc.input, -1, eval.FunctionJSONBRecordInput())
			var diagnostic *sqlstate.Error
			if !errors.As(err, &diagnostic) || diagnostic.Code != tc.code || diagnostic.DataType != tc.dataType {
				t.Fatal("lost original diagnostic", err)
			}
			if sqltype.InputSoftError(err) != tc.soft {
				t.Fatal("incorrect saved-error classification", err)
			}
		})
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	eval.Context = cancelled
	if err := eval.FunctionJSONBRecordInput().DomainChecks.CheckDomain(cancelled, parent, sqltype.Null(integer)); err != context.Canceled {
		t.Fatal("cancellation identity changed", err)
	}
}

func TestJSONBRecordExpansion(t *testing.T) {
	e := New()
	b := newTestExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
	e.Mu.Lock()
	defer e.Mu.Unlock()
	auth, err := e.AuthorizationLocked(binderOperation(b))
	if err != nil {
		t.Fatal(err)
	}
	b.Query.Auth = &auth
	b.Query.Frame = 1
	b.Clause.AggregateClause = "SELECT"
	for _, name := range []string{"jsonb_to_record", "jsonb_to_recordset"} {
		raw := parser.FunctionExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: name}}}, Args: []parser.Expr{parser.LiteralExpr{Value: parser.Value{Kind: parser.StringValue, Text: `{}`}}}}
		base, err := b.BindCompositeBase(raw)
		if err != nil {
			t.Fatal(err)
		}
		if !analyze.JSONBRecordUnresolved(base) {
			t.Fatal("lost explicit unresolved marker", base)
		}
		original := base.(functions.JSONBRecord)
		rebuilt, err := original.Reconstruct(original.Info, original.Args)
		if err != nil || !rebuilt.Unresolved {
			t.Fatal("reconstruction registered unknown layout", err)
		}
		_, err = b.ExpandComposite(parser.FieldExpr{Base: raw, Expansion: true})
		var diagnostic *sqlstate.Error
		if !errors.As(err, &diagnostic) || diagnostic.Code != "42809" || diagnostic.Message != "record type has not been registered" || diagnostic.Position != "" || diagnostic.Severity != "ERROR" {
			t.Fatal("unknown expansion did not reject before member capture", err)
		}
	}
	raw := parser.FunctionExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "jsonb_populate_record"}}}, Args: []parser.Expr{
		parser.RowExpr{Explicit: true},
		parser.LiteralExpr{Value: parser.Value{Kind: parser.StringValue, Text: `{}`}},
	}}
	_, err = b.ExpandComposite(parser.FieldExpr{Base: raw, Expansion: true})
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) || diagnostic.Code != "42809" || diagnostic.Message != "record type has not been registered" {
		t.Fatal("runtime ROW registration fabricated a static function layout", err)
	}
	base, err := b.BindCompositeBase(raw)
	if err != nil || !analyze.JSONBRecordUnresolved(base) {
		t.Fatal(base, err)
	}
	value, err := base.Eval(&exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}})
	if err != nil || value.IsNull() || value.CompositeLen() != 0 {
		t.Fatal("registered empty record lost presence", value, err)
	}
}

func TestJSONBRecordReplay(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		query, want string
		casts       int
	}{
		{`SELECT jsonb_populate_record('(9,keep)'::pair,'{"id":1}'::jsonb)`, "(1,keep)", 2},
		{`SELECT jsonb_populate_record(CAST('(9,keep)'::pair AS pair),'{"id":1}'::jsonb)`, "(1,keep)", 3},
		{`SELECT jsonb_populate_record(NULL::pair,'{"id":1}'::jsonb)`, "(1,)", 2},
		{`SELECT jsonb_populate_record('(9,keep)'::pair,'{"id":1}'::jsonb)::text`, "(1,keep)", 3},
	} {
		t.Run(tc.query, func(t *testing.T) {
			engine, _, _ := typedUnitCatalog(t, false)
			query, previous := tc.query, ""
			for round := range 3 {
				b := newTestExpressionBinder(t, ctx, engine, sqltype.InputEnvironment{}, nil)
				raw := statement(t, query).(*parser.Select).Targets[0]
				node, err := b.Bind(raw, analyze.ExpressionPolicy{Context: analyze.ProjectionContext})
				if err != nil {
					t.Fatal(round, err)
				}
				value, err := node.Eval(&exec.Context{EvalStatement: exec.EvalStatement{Context: ctx, Catalog: engine.CatalogRead()}})
				if err != nil {
					t.Fatal(round, err)
				}
				text, err := value.Type().Output(ctx, value)
				if err != nil || text != tc.want {
					t.Fatal("replay changed conversion", round, text, err)
				}
				if err := b.CaptureViewLiterals(raw, node, analyze.ViewTargetLiteral, 0); err != nil {
					t.Fatal(round, err)
				}
				renderer := stored.QueryRenderer{View: stored.NewView(stored.ViewParts{Literals: b.Capture.Literals, CastUsages: b.Capture.CastUsages}), Frame: b.FrameID()}
				emitted, err := renderer.ExpressionSQL(ctx, engine.CatalogRead(), raw, analyze.ViewLiteralPath{Clause: analyze.ViewTargetLiteral})
				if err != nil {
					t.Fatal(round, err)
				}
				if strings.Count(emitted, "CAST(") != tc.casts {
					t.Fatal("leaf elision removed or duplicated an owning/outer cast", round, emitted)
				}
				if round != 0 && emitted != previous {
					t.Fatalf("captured composite leaf acquired another cast: round %d\nbefore:\n%s\nafter:\n%s", round, previous, emitted)
				}
				previous, query = emitted, "SELECT "+emitted
			}
		})
	}
}

func TestJSONBRecordLayout(t *testing.T) {
	ctx := context.Background()
	e := New()
	b := newTestExpressionBinder(t, ctx, e, sqltype.InputEnvironment{}, nil)
	e.Mu.Lock()
	defer e.Mu.Unlock()
	auth, err := e.AuthorizationLocked(binderOperation(b))
	if err != nil {
		t.Fatal(err)
	}
	b.Query.Auth = &auth
	b.Query.Frame = 1
	b.Clause.AggregateClause = "SELECT"
	for _, tc := range []struct {
		query     string
		nullInner bool
	}{
		{`SELECT * FROM jsonb_populate_record(jsonb_to_record(NULL::jsonb),'{"a":1}'::jsonb) AS r(a int,b text)`, true},
		{`SELECT * FROM jsonb_populate_record(ROW(9,'keep'::text),'{"f1":1}'::jsonb) AS r(a int,b text)`, false},
	} {
		raw := statement(t, tc.query).(*parser.Select)
		root, namespace, err := b.BindFrom(raw.From)
		if err != nil {
			t.Fatal(tc.query, err)
		}
		call := root.(*plan.FunctionFromNode).Calls[0].(functions.JSONBRecord)
		if !call.Definitions || call.Unresolved || len(namespace.Columns) != 2 || call.RecordUsage().Type.CompositeMember(0).Name != "a" {
			t.Fatal("lost caller layout", call, namespace)
		}
		if tc.nullInner {
			inner, ok := call.Args[0].(functions.JSONBRecord)
			if !ok || !inner.Unresolved || inner.Definitions || inner.ResultUsage().Type.CompositeLen() != 0 {
				t.Fatal("context replaced or supplied a descriptor to real inner expression", call.Args[0])
			}
		} else if call.Args[0].ResultUsage().Type.CompositeMember(0).Name != "f1" {
			t.Fatal("caller labels replaced runtime field names")
		}
		value, err := call.Eval(&exec.Context{EvalStatement: exec.EvalStatement{Context: ctx, Catalog: auth.Catalog}})
		if err != nil || value.Type() != call.ResultUsage().Type {
			t.Fatal(value, err)
		}
		if n, err := value.CompositeField(0).Integer(); err != nil || n != 1 {
			t.Fatal(n, err)
		}
	}
	raw := statement(t, `SELECT (jsonb_populate_record(jsonb_populate_record(jsonb_to_record('{}'::jsonb),'{}'::jsonb),'{}'::jsonb)).*`).(*parser.Select).Targets[0].(parser.FieldExpr)
	if _, err := b.ExpandComposite(raw); err == nil {
		t.Fatal("composed unresolved result fabricated empty expansion")
	}
	base, err := b.BindCompositeBase(raw.Base)
	if err != nil || !analyze.JSONBRecordUnresolved(base) {
		t.Fatal("lost transitive static layout state", base, err)
	}
	if _, err := base.Eval(&exec.Context{EvalStatement: exec.EvalStatement{Context: ctx, Catalog: auth.Catalog}}); err == nil {
		t.Fatal("inner unresolved expression was erased")
	}
}
