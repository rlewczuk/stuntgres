package engine

import (
	"context"
	"reflect"
	"testing"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// Exercise the actual bound constructors and conversion plans: raw view recipes
// do not go through these renderers and cannot prove their coercion boundaries.
func TestSQLJSONRendererBoundaries(t *testing.T) {
	e := New()
	usage := func(id sqltype.ID) sqltype.Usage { return aclUnitType(t, e, id).Usage() }
	literal := func(v sqltype.Value) expr.Expr {
		return literalExpression{Info: expr.Info{Usage: v.Type().Usage()}, Value: v}
	}
	integer, err := sqltype.Integer(usage(sqltype.Int4).Type, 1)
	if err != nil {
		t.Fatal(err)
	}
	one := literal(integer)
	key := literal(sqltype.String(usage(sqltype.Text).Type, "a"))
	document := literal(sqltype.String(usage(sqltype.Text).Type, "{}"))
	null := literal(sqltype.Null(usage(sqltype.Text).Type))
	constructor := func(raw parser.SQLJSONExpr, args ...expr.Expr) functions.SQLJSON {
		t.Helper()
		n, err := functions.NewSQLJSON(expr.Info{Usage: usage(sqltype.JSON)}, raw, args)
		if err != nil {
			t.Fatal(err)
		}
		return n
	}
	convert := func(child expr.Expr, target sqltype.Usage, mode sqltype.CoercionContext, provenance string) expr.Expr {
		t.Helper()
		var plan sqltype.CoercionPlan
		var err error
		switch provenance {
		case "input":
			plan, err = sqltype.PlanJSONFormattedInput(child.ResultUsage(), target, "UTF8")
		case "returning":
			plan, err = sqltype.PlanJSONReturning(child.ResultUsage(), target)
		default:
			plan, err = sqltype.PlanCoercion(child.ResultUsage(), target, mode)
		}
		if err != nil {
			t.Fatal(err)
		}
		return coercionExpression{Info: expr.Info{Usage: target}, Child: child, Mode: mode, Plan: plan}
	}
	// Publish a genuine catalog domain; both interpreters must retain its identity
	// and use their catalog-owned type spelling, not the raw RETURNING name.
	e.mu.Lock()
	oid, err := e.catalog.allocateOIDLocked()
	e.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	domain, err := sqltype.Domain(sqltype.ID(oid), "json_render_domain", "json_render_domain", usage(sqltype.Text))
	if err != nil {
		t.Fatal(err)
	}
	descriptor := &catalog.Type{SQLType: domain, ObjectNamespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, ObjectRevision: 1, Owner: e.roles.published.Root.Read(authorization.PrincipalRef{}).LookupRef("postgres")}
	stage := e.BeginCatalogStage(e.CatalogRead())
	if err := stage.Create(descriptor); err != nil {
		t.Fatal(err)
	}
	queryCreatePublish(t, e, stage)
	view := e.CatalogRead()
	domain = aclUnitType(t, e, domain.ID())

	scalar := constructor(parser.SQLJSONExpr{Kind: "json_scalar"}, one)
	formatted := convert(document, usage(sqltype.JSON), sqltype.Explicit, "input")
	parsed := constructor(parser.SQLJSONExpr{Kind: "json"}, formatted)
	object := constructor(parser.SQLJSONExpr{Kind: "json_object"}, key, one)
	textObject := convert(object, usage(sqltype.Text), sqltype.Assignment, "returning")
	empty := constructor(parser.SQLJSONExpr{Kind: "json_object"})
	emptyOutput := constructor(parser.SQLJSONExpr{Kind: "json_object", Output: &parser.JSONFormat{Encoding: "UTF8"}})
	nullObject := constructor(parser.SQLJSONExpr{Kind: "json_object", Absent: true}, null, null)
	varchar := usage(sqltype.VarChar)
	varchar.Typmod = 24 // PostgreSQL's VARHDRSZ + declared length 20.
	explicitJSON := convert(document, usage(sqltype.JSON), sqltype.Explicit, "")
	formattedArray := constructor(parser.SQLJSONExpr{Kind: "json_array", Absent: true, Formats: []*parser.JSONFormat{{}}}, explicitJSON)
	byteValue, err := sqltype.Binary(usage(sqltype.Bytea).Type, "{}")
	if err != nil {
		t.Fatal(err)
	}
	bytes := literal(byteValue)
	byteArray := constructor(parser.SQLJSONExpr{Kind: "json_array", Absent: true, Formats: []*parser.JSONFormat{{Encoding: "UTF8"}}}, convert(bytes, usage(sqltype.JSON), sqltype.Explicit, "input"))
	serialize := constructor(parser.SQLJSONExpr{Kind: "json_serialize"}, formatted)
	unique := constructor(parser.SQLJSONExpr{Kind: "json", Unique: true}, formatted)
	nested := constructor(parser.SQLJSONExpr{Kind: "json_array", Absent: true}, scalar)
	ordinary, err := functions.NewBinary(expr.Info{Usage: usage(sqltype.JSON)}, "json_object", []expr.Expr{literal(sqltype.Null(usage(1009).Type)), literal(sqltype.Null(usage(1009).Type))}, parser.OrdinaryFunctionSyntax)
	if err != nil {
		t.Fatal(err)
	}
	length, err := functions.NewBinary(expr.Info{Usage: usage(sqltype.Int4)}, "length", []expr.Expr{bytes}, parser.OrdinaryFunctionSyntax)
	if err != nil {
		t.Fatal(err)
	}
	implicitArray := constructor(parser.SQLJSONExpr{Kind: "json_array", Absent: true}, convert(length, usage(sqltype.Int8), sqltype.Implicit, ""))

	cases := []struct {
		name                    string
		node                    expr.Expr
		display, pretty, replay string
	}{
		{"scalar", scalar, "JSON_SCALAR(1)", "JSON_SCALAR(1)", "JSON_SCALAR(1)"},
		{"parse formatted source", parsed, "JSON('{}'::text)", "JSON('{}'::text)", "JSON('{}')"},
		{"scalar assignment", convert(scalar, usage(sqltype.JSONB), sqltype.Assignment, ""), "(JSON_SCALAR(1))::jsonb", "(JSON_SCALAR(1))::jsonb", "CAST(JSON_SCALAR(1) AS JSONB)"},
		{"parse assignment", convert(parsed, usage(sqltype.JSONB), sqltype.Assignment, ""), "(JSON('{}'::text))::jsonb", "(JSON('{}'::text))::jsonb", "CAST(JSON('{}') AS JSONB)"},
		{"member returning", textObject, "JSON_OBJECT(('a'::text) VALUE 1 NULL ON NULL RETURNING text)", "JSON_OBJECT(('a'::text) VALUE 1 NULL ON NULL RETURNING text)", "JSON_OBJECT(('a') VALUE 1 NULL ON NULL RETURNING TEXT)"},
		{"empty object", empty, "JSON_OBJECT()", "JSON_OBJECT()", "JSON_OBJECT()"},
		{"empty output encoding", convert(emptyOutput, usage(sqltype.Bytea), sqltype.Assignment, "returning"), "JSON_OBJECT(RETURNING bytea FORMAT JSON ENCODING UTF8)", "JSON_OBJECT(RETURNING bytea FORMAT JSON ENCODING UTF8)", "JSON_OBJECT(RETURNING BYTEA FORMAT JSON ENCODING UTF8)"},
		{"empty array", constructor(parser.SQLJSONExpr{Kind: "json_array", Absent: true}), "JSON_ARRAY( ABSENT ON NULL)", "JSON_ARRAY( ABSENT ON NULL)", "JSON_ARRAY( ABSENT ON NULL)"},
		{"absent assignment", convert(nullObject, usage(sqltype.JSONB), sqltype.Assignment, ""), "(JSON_OBJECT((NULL::text) VALUE NULL::text ABSENT ON NULL))::jsonb", "(JSON_OBJECT((NULL::text) VALUE NULL::text ABSENT ON NULL))::jsonb", "CAST(JSON_OBJECT((NULL) VALUE NULL ABSENT ON NULL) AS JSONB)"},
		{"text then explicit jsonb", convert(textObject, usage(sqltype.JSONB), sqltype.Explicit, ""), "(JSON_OBJECT(('a'::text) VALUE 1 NULL ON NULL RETURNING text))::jsonb", "(JSON_OBJECT(('a'::text) VALUE 1 NULL ON NULL RETURNING text))::jsonb", "CAST(CAST(JSON_OBJECT(('a') VALUE 1 NULL ON NULL RETURNING TEXT) AS TEXT) AS JSONB)"},
		{"typmod returning", convert(empty, varchar, sqltype.Assignment, "returning"), "JSON_OBJECT(RETURNING character varying(20))", "JSON_OBJECT(RETURNING character varying(20))", "JSON_OBJECT(RETURNING VARCHAR(20))"},
		{"domain returning", convert(empty, domain.Usage(), sqltype.Assignment, "returning"), "JSON_OBJECT(RETURNING json_render_domain)", "JSON_OBJECT(RETURNING json_render_domain)", "JSON_OBJECT(RETURNING \"public\".\"json_render_domain\")"},
		{"explicit input cast survives", formattedArray, "JSON_ARRAY(('{}'::text)::json FORMAT JSON ABSENT ON NULL)", "JSON_ARRAY('{}'::text::json FORMAT JSON ABSENT ON NULL)", "JSON_ARRAY(CAST(CAST('{}' AS TEXT) AS JSON) FORMAT JSON ABSENT ON NULL)"},
		{"byte input encoding", byteArray, "JSON_ARRAY('\\x7b7d'::bytea FORMAT JSON ENCODING UTF8 ABSENT ON NULL)", "JSON_ARRAY('\\x7b7d'::bytea FORMAT JSON ENCODING UTF8 ABSENT ON NULL)", "JSON_ARRAY('\\x7b7d' FORMAT JSON ENCODING UTF8 ABSENT ON NULL)"},
		{"serialize returning", convert(serialize, usage(sqltype.Text), sqltype.Assignment, "returning"), "JSON_SERIALIZE('{}'::text RETURNING text)", "JSON_SERIALIZE('{}'::text RETURNING text)", "JSON_SERIALIZE('{}' RETURNING TEXT)"},
		{"unique parse", unique, "JSON('{}'::text WITH UNIQUE KEYS)", "JSON('{}'::text WITH UNIQUE KEYS)", "JSON('{}' WITH UNIQUE KEYS)"},
		{"nested", nested, "JSON_ARRAY(JSON_SCALAR(1) ABSENT ON NULL)", "JSON_ARRAY(JSON_SCALAR(1) ABSENT ON NULL)", "JSON_ARRAY(JSON_SCALAR(1) ABSENT ON NULL)"},
		{"ordinary call assignment", convert(ordinary, usage(sqltype.JSONB), sqltype.Assignment, ""), "\"json_object\"(NULL::text[], NULL::text[])", "\"json_object\"(NULL::text[], NULL::text[])", "pg_catalog.json_object(CAST(NULL AS TEXT[]), CAST(NULL AS TEXT[]))"},
		{"implicit nonliteral remains hidden", implicitArray, "JSON_ARRAY(length('\\x7b7d'::bytea) ABSENT ON NULL)", "JSON_ARRAY(length('\\x7b7d'::bytea) ABSENT ON NULL)", "JSON_ARRAY(pg_catalog.length('\\x7b7d') ABSENT ON NULL)"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			for _, pretty := range []bool{false, true} {
				got, err := (pgRenderer{view: view, pretty: pretty}).sql(context.Background(), c.node, pgRenderParent{})
				want := c.display
				if pretty {
					want = c.pretty
				}
				if err != nil || got != want {
					t.Fatalf("display pretty=%v = %q, %v; want %q", pretty, got, err, want)
				}
			}
			got, err := defaultSQL(context.Background(), view, c.node)
			if err != nil || got != c.replay {
				t.Fatalf("replay = %q, %v; want %q", got, err, c.replay)
			}
		})
	}

	// A real volatile operand requires an execution context and physical random
	// owner to Eval. Both real renderers must work without either, repeatedly,
	// and leave the bound children, conversion provenance and tree untouched.
	random := functions.Random{Info: expr.Info{Usage: usage(sqltype.Float8), Property: expr.Properties{Volatility: expr.Volatile}}, Name: "random"}
	effect := constructor(parser.SQLJSONExpr{Kind: "json_array", Absent: true}, random, formatted)
	before := effect
	before.Args = append([]expr.Expr(nil), effect.Args...)
	tree, err := effect.Render()
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		got, err := (pgRenderer{view: view}).sql(context.Background(), effect, pgRenderParent{})
		if err != nil || got != "JSON_ARRAY(random(), '{}'::text ABSENT ON NULL)" {
			t.Fatal(got, err)
		}
		got, err = (scalarRenderer{owner: &catalog.Relation{}}).sql(context.Background(), view, effect)
		if err != nil || got != "JSON_ARRAY(pg_catalog.random(), '{}' ABSENT ON NULL)" {
			t.Fatal(got, err)
		}
	}
	after, err := effect.Render()
	if err != nil || !reflect.DeepEqual(before, effect) || tree.Tree != after.Tree || !reflect.DeepEqual(tree.Inputs, after.Inputs) {
		t.Fatal("rendering mutated bound constructor/children/plan/tree", err)
	}
}
