package engine

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/control"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// Expectations come from REL_18_0 ruleutils constant and expression printers;
// the core operator/CASE goldens also have pinned-release public witnesses.
func TestDeparseConstraintsPersistedFamilies(t *testing.T) {
	for _, tc := range []struct{ expr, compact, pretty string }{
		{"a::bigint>0", "((a)::bigint > 0)", "a::bigint > 0"},
		{"a+1>0", "((a + 1) > 0)", "(a + 1) > 0"},
		{"a-1>0", "((a - 1) > 0)", "(a - 1) > 0"},
		{"a*2>0", "((a * 2) > 0)", "(a * 2) > 0"},
		{"a/2>0", "((a / 2) > 0)", "(a / 2) > 0"},
		{"a%2=0", "((a % 2) = 0)", "(a % 2) = 0"},
		{"-a>0", "((- a) > 0)", "(- a) > 0"},
		{"+a>0", "((+ a) > 0)", "(+ a) > 0"},
		{"(a&3)=1", "((a & 3) = 1)", "(a & 3) = 1"},
		{"(a|3)=1", "((a | 3) = 1)", "(a | 3) = 1"},
		{"(a#3)=1", "((a # 3) = 1)", "(a # 3) = 1"},
		{"(~a)=1", "((~ a) = 1)", "(~ a) = 1"},
		{"(a<<1)=2", "((a << 1) = 2)", "(a << 1) = 2"},
		{"(a>>1)=2", "((a >> 1) = 2)", "(a >> 1) = 2"},
		{"a=1", "(a = 1)", "a = 1"},
		{"a<>1", "(a <> 1)", "a <> 1"},
		{"a<1", "(a < 1)", "a < 1"},
		{"a<=1", "(a <= 1)", "a <= 1"},
		{"a>=1", "(a >= 1)", "a >= 1"},
		{"a IS NULL", "(a IS NULL)", "a IS NULL"},
		{"a IS NOT NULL", "(a IS NOT NULL)", "a IS NOT NULL"},
		{"flag IS TRUE", "(flag IS TRUE)", "flag IS TRUE"},
		{"flag IS FALSE", "(flag IS FALSE)", "flag IS FALSE"},
		{"flag IS UNKNOWN", "(flag IS UNKNOWN)", "flag IS UNKNOWN"},
		{"flag IS NOT TRUE", "(flag IS NOT TRUE)", "flag IS NOT TRUE"},
		{"flag IS NOT FALSE", "(flag IS NOT FALSE)", "flag IS NOT FALSE"},
		{"flag IS NOT UNKNOWN", "(flag IS NOT UNKNOWN)", "flag IS NOT UNKNOWN"},
		{"a IS DISTINCT FROM 1", "(a IS DISTINCT FROM 1)", "a IS DISTINCT FROM 1"},
		{"a IS NOT DISTINCT FROM 1", "(NOT (a IS DISTINCT FROM 1))", "NOT a IS DISTINCT FROM 1"},
		{"NOT flag", "(NOT flag)", "NOT flag"},
		{"a>0 AND (a<5 OR flag)", "((a > 0) AND ((a < 5) OR flag))", "a > 0 AND (a < 5 OR flag)"},
		{"COALESCE(a,1)>0", "(COALESCE(a, 1) > 0)", "COALESCE(a, 1) > 0"},
		{"NULLIF(a,1)>0", "(NULLIF(a, 1) > 0)", "NULLIF(a, 1) > 0"},
		{"GREATEST(a,1)>0", "(GREATEST(a, 1) > 0)", "GREATEST(a, 1) > 0"},
		{"LEAST(a,1)>0", "(LEAST(a, 1) > 0)", "LEAST(a, 1) > 0"},
		{"CASE WHEN flag THEN a ELSE 2 END>0", "(\nCASE\n    WHEN flag THEN a\n    ELSE 2\nEND > 0)", "\nCASE\n    WHEN flag THEN a\n    ELSE 2\nEND > 0"},
		{"CASE a WHEN 1 THEN TRUE ELSE FALSE END", "\nCASE a\n    WHEN 1 THEN true\n    ELSE false\nEND", "\nCASE a\n    WHEN 1 THEN true\n    ELSE false\nEND"},
		{"abs(a)>0", "(abs(a) > 0)", "abs(a) > 0"},
		{"round(n,1)>0", "(round(n, 1) > (0)::numeric)", "round(n, 1) > 0::numeric"},
		{"num_nulls(a)>0", "(num_nulls(a) > 0)", "num_nulls(a) > 0"},
		{"num_nonnulls(a)>0", "(num_nonnulls(a) > 0)", "num_nonnulls(a) > 0"},
		{"current_schemas(false) IS NOT NULL", "(current_schemas(false) IS NOT NULL)", "current_schemas(false) IS NOT NULL"},
		{"obj_description(a,'pg_class') IS NULL", "(obj_description((a)::oid, 'pg_class'::name) IS NULL)", "obj_description(a::oid, 'pg_class'::name) IS NULL"},
		{"col_description(a,1) IS NULL", "(col_description((a)::oid, 1) IS NULL)", "col_description(a::oid, 1) IS NULL"},
		{"shobj_description(a,'pg_authid') IS NULL", "(shobj_description((a)::oid, 'pg_authid'::name) IS NULL)", "shobj_description(a::oid, 'pg_authid'::name) IS NULL"},
		{"obj_description(a) IS NULL", "(obj_description((a)::oid) IS NULL)", "obj_description(a::oid) IS NULL"},
		{"TIMESTAMP '2026-10-08 00:00:00' AT LOCAL IS NOT NULL", "(('2026-10-08 00:00:00'::timestamp without time zone AT LOCAL) IS NOT NULL)", "('2026-10-08 00:00:00'::timestamp without time zone AT LOCAL) IS NOT NULL"},
		{"(a OPERATOR(pg_catalog.&) 3)=1", "((a & 3) = 1)", "(a & 3) = 1"},
		{"a OPERATOR(pg_catalog.>) 0", "(a > 0)", "a > 0"},
		{"(a OPERATOR(pg_catalog.+) 1)>0", "((a + 1) > 0)", "(a + 1) > 0"},
		{"random() IS NOT NULL", "(random() IS NOT NULL)", "random() IS NOT NULL"},
		{"random()>0", "(random() > (0)::double precision)", "random() > 0::double precision"},
		{"random_normal() IS NOT NULL", "(random_normal() IS NOT NULL)", "random_normal() IS NOT NULL"},
		{"setseed(0.5) IS NOT NULL", "(setseed((0.5)::double precision) IS NOT NULL)", "setseed(0.5::double precision) IS NOT NULL"},
		{"nextval('s')>0", "(nextval('s'::regclass) > 0)", "nextval('s'::regclass) > 0"},
		{"currval('s')>0", "(currval('s'::regclass) > 0)", "currval('s'::regclass) > 0"},
		{"lastval()>0", "(lastval() > 0)", "lastval() > 0"},
		{"setval('s',1,false)>0", "(setval('s'::regclass, (1)::bigint, false) > 0)", "setval('s'::regclass, 1::bigint, false) > 0"},
		{"TIMESTAMP '2026-10-08 00:00:00' AT TIME ZONE 'UTC' IS NOT NULL", "(('2026-10-08 00:00:00'::timestamp without time zone AT TIME ZONE 'UTC'::text) IS NOT NULL)", "('2026-10-08 00:00:00'::timestamp without time zone AT TIME ZONE 'UTC'::text) IS NOT NULL"},
	} {
		t.Run(tc.expr, func(t *testing.T) {
			e := New()
			run(t, e, "CREATE SEQUENCE s")
			run(t, e, "CREATE TABLE t(a INT,flag BOOL,n NUMERIC,CONSTRAINT c CHECK("+tc.expr+"))")
			op, done, err := testOperation(e, context.Background())
			if err != nil {
				t.Fatal(err)
			}
			defer done()
			scope, err := e.StartStatement(op)
			if err != nil {
				t.Fatal(err)
			}
			defer scope.Finish(nil)
			e.Mu.Lock()
			err = e.CaptureCommentSnapshotLocked(scope)
			e.Mu.Unlock()
			if err != nil {
				t.Fatal(err)
			}
			view := scope.Inquiry.CommentCatalog
			d := view.MustRelation(roleTableRef(t, e, "t").Address)
			node := d.Constraints.Check[0].Expression
			for _, pretty := range []bool{false, true} {
				want := tc.compact
				if pretty {
					want = tc.pretty
				}
				got, err := (stored.Deparser{View: view, Relation: d, Pretty: pretty, Path: scope.Inquiry.DeparsePath, Output: scope.Output, NonstandardStrings: op.NonstandardStrings}).SQL(context.Background(), node, stored.DeparseParent{})
				if err != nil || got != want {
					t.Fatalf("pretty=%t got %q, %v; want %q", pretty, got, err, want)
				}
			}
		})
	}
}

func TestDeparseConstraintsLiteralTypes(t *testing.T) {
	e := New()
	view := e.CatalogRead()
	for _, tc := range []struct {
		id                     sqltype.ID
		input, compact, pretty string
	}{
		{sqltype.Bool, "true", "true", "true"},
		{sqltype.Int2, "1", "'1'::smallint", "'1'::smallint"},
		{sqltype.Int4, "1", "1", "1"},
		{sqltype.Int8, "1", "'1'::bigint", "'1'::bigint"},
		{sqltype.Oid, "1", "'1'::oid", "'1'::oid"},
		{sqltype.Text, "hello", "'hello'::text", "'hello'::text"},
		{sqltype.Name, "hello", "'hello'::name", "'hello'::name"},
		{sqltype.Char, "x", "'x'::\"char\"", "'x'::\"char\""},
		{sqltype.BPChar, "hi", "'hi'::bpchar", "'hi'::bpchar"},
		{sqltype.VarChar, "hi", "'hi'::character varying", "'hi'::character varying"},
		{sqltype.Bytea, `\x00ff`, `'\x00ff'::bytea`, `'\x00ff'::bytea`},
		{sqltype.UUID, "00000000-0000-0000-0000-000000000001", "'00000000-0000-0000-0000-000000000001'::uuid", "'00000000-0000-0000-0000-000000000001'::uuid"},
		{sqltype.Inet, "192.168.0.129/25", "'192.168.0.129/25'::inet", "'192.168.0.129/25'::inet"},
		{sqltype.Cidr, "10", "'10.0.0.0/8'::cidr", "'10.0.0.0/8'::cidr"},
		{sqltype.Numeric, "1.20", "1.20", "1.20"},
		{sqltype.Numeric, "NaN", "'NaN'::numeric", "'NaN'::numeric"},
		{sqltype.Numeric, "Infinity", "'Infinity'::numeric", "'Infinity'::numeric"},
		{sqltype.Numeric, "-Infinity", "'-Infinity'::numeric", "'-Infinity'::numeric"},
		{sqltype.Float4, "1.5", "'1.5'::real", "'1.5'::real"},
		{sqltype.Float8, "NaN", "'NaN'::double precision", "'NaN'::double precision"},
		{sqltype.Float8, "Infinity", "'Infinity'::double precision", "'Infinity'::double precision"},
		{sqltype.Float8, "-Infinity", "'-Infinity'::double precision", "'-Infinity'::double precision"},
		{sqltype.Date, "2026-10-08", "'2026-10-08'::date", "'2026-10-08'::date"},
		{sqltype.Time, "12:34:56", "'12:34:56'::time without time zone", "'12:34:56'::time without time zone"},
		{sqltype.Timetz, "12:34:56+00", "'12:34:56+00'::time with time zone", "'12:34:56+00'::time with time zone"},
		{sqltype.Timestamp, "2026-10-08 12:34:56", "'2026-10-08 12:34:56'::timestamp without time zone", "'2026-10-08 12:34:56'::timestamp without time zone"},
		{sqltype.Timestamptz, "2026-10-08 12:34:56+00", "'2026-10-08 12:34:56+00'::timestamp with time zone", "'2026-10-08 12:34:56+00'::timestamp with time zone"},
		{sqltype.Interval, "1 day", "'1 day'::interval", "'1 day'::interval"},
		{sqltype.Regclass, "123", "'123'::regclass", "'123'::regclass"},
	} {
		t.Run(fmt.Sprintf("%d/%s", tc.id, tc.input), func(t *testing.T) {
			typ, ok := view.LookupType(tc.id)
			if !ok {
				t.Fatal(tc.id)
			}
			value, err := typ.Input(context.Background(), tc.input, -1)
			if err != nil {
				t.Fatal(err)
			}
			node := plan.LiteralExpr{Info: expr.Info{Usage: typ.Usage()}, Value: value}
			for _, pretty := range []bool{false, true} {
				got, err := (stored.Deparser{View: view, Pretty: pretty}).SQL(context.Background(), node, stored.DeparseParent{})
				want := tc.compact
				if pretty {
					want = tc.pretty
				}
				if err != nil || got != want {
					t.Fatalf("got %q, %v; want %q", got, err, want)
				}
				node.Value = sqltype.Null(typ)
				got, err = (stored.Deparser{View: view, Pretty: pretty}).SQL(context.Background(), node, stored.DeparseParent{})
				declaration, err2 := analyze.PGType(typ.Usage())
				if err != nil || err2 != nil || got != "NULL::"+declaration {
					t.Fatal(got, err, err2)
				}
				node.Value = value
			}
		})
	}
}

type deparseTrap struct {
	expr.Info
	calls *int
}

func (n deparseTrap) Eval(expr.Context) (sqltype.Value, error) {
	*n.calls++
	panic("stored tree evaluated")
}

func TestDeparseConstraintsNoEvalAndCarrierLifetime(t *testing.T) {
	e := New()
	run(t, e, "CREATE TABLE t(a INT DEFAULT (1/0),CONSTRAINT c CHECK(a>0))")
	view := e.CatalogRead()
	d := view.MustRelation(roleTableRef(t, e, "t").Address)
	treeType, _ := view.LookupType(sqltype.PgNodeTree)
	saved, err := plan.CatalogTree(treeType, d.ColumnMeta.Defaults[1].Expression)
	if err != nil {
		t.Fatal(err)
	}
	run(t, e, "DROP TABLE t")
	payload, err := saved.Tree()
	if err != nil {
		t.Fatal(err)
	}
	got, err := (stored.Deparser{View: e.CatalogRead()}).SQL(context.Background(), payload.(plan.BoundTree).Expression, stored.DeparseParent{})
	if err != nil || got != "(1 / 0)" {
		t.Fatal(got, err)
	}
	structural, err := treeType.Output(context.Background(), saved)
	if err != nil || !strings.Contains(structural, "Arithmetic") || strings.Contains(structural, "SELECT") {
		t.Fatal(structural, err)
	}
	calls := 0
	trap := deparseTrap{Info: expr.Info{Usage: d.Columns[0].Usage}, calls: &calls}
	if _, err := (stored.Deparser{View: view}).SQL(context.Background(), trap, stored.DeparseParent{}); err == nil || calls != 0 {
		t.Fatal(err, calls)
	}
	if plan.StructuralNull(trap) || calls != 0 {
		t.Fatal("NULL producer evaluated")
	}
	for _, input := range []string{"", "x", string([]byte{0xff, 0})} {
		_, err := treeType.Input(context.Background(), input, -1)
		var diagnostic *sqlstate.Error
		if !errors.As(err, &diagnostic) || diagnostic.Code != "0A000" || diagnostic.Message != "cannot accept a value of type pg_node_tree" {
			t.Fatal(err)
		}
	}
}

func TestDeparseConstraintsDefinitionIdentityAndNullProducer(t *testing.T) {
	e := New()
	run(t, e, "CREATE TABLE t(a INT DEFAULT NULL,b INT DEFAULT (NULL::integer),c INT DEFAULT COALESCE(NULL,1),d INT NOT NULL,CHECK(c>0))")
	view := e.CatalogRead()
	d := view.MustRelation(roleTableRef(t, e, "t").Address)
	if len(d.ColumnMeta.Defaults) != 1 || d.ColumnMeta.Defaults[3].OID == 0 || d.Constraints.Check[0].OID == 0 || d.Constraints.NotNull[0].OID == 0 {
		t.Fatal(d.ColumnMeta.Defaults, d.Constraints.Check, d.Constraints.NotNull)
	}
	if err := catalog.ValidateDefinitionOIDs(view); err != nil {
		t.Fatal(err)
	}
	old := d.ColumnMeta.Defaults[3].OID
	run(t, e, "ALTER TABLE t ALTER c SET DEFAULT 3")
	next := e.CatalogRead().MustRelation(d.Address())
	if next.ColumnMeta.Defaults[3].OID == old || next.Constraints.Check[0].OID != d.Constraints.Check[0].OID {
		t.Fatal("definition identity replacement/preservation")
	}
	run(t, e, "ALTER TABLE t ALTER c SET DEFAULT NULL")
	if len(e.CatalogRead().MustRelation(d.Address()).ColumnMeta.Defaults) != 0 {
		t.Fatal("NULL definition persisted")
	}
}

func TestDeparseConstraintsStrictnessAndSuppliedAttributes(t *testing.T) {
	e := New()
	view := e.CatalogRead()
	textType, _ := view.LookupType(sqltype.Text)
	oidType, _ := view.LookupType(sqltype.Oid)
	treeType, _ := view.LookupType(sqltype.PgNodeTree)
	boolType, _ := view.LookupType(sqltype.Bool)
	literal := func(v sqltype.Value) expr.Expr {
		return plan.LiteralExpr{Info: expr.Info{Usage: v.Type().Usage()}, Value: v}
	}
	zero, _ := sqltype.OIDValue(oidType, 0)
	tree, err := plan.CatalogTree(treeType, literal(zero))
	if err != nil {
		t.Fatal(err)
	}
	for _, constraint := range []bool{false, true} {
		args := []expr.Expr{literal(tree), literal(zero), literal(sqltype.Boolean(boolType, false))}
		if constraint {
			args = []expr.Expr{literal(zero), literal(sqltype.Boolean(boolType, false))}
		}
		signature := functions.DeparseExprPretty
		if constraint {
			signature = functions.DeparseConstraintPretty
		}
		for nullable := range args {
			test := append([]expr.Expr(nil), args...)
			test[nullable] = literal(sqltype.Null(test[nullable].ResultUsage().Type))
			node := functions.Deparse{Info: expr.Info{Usage: textType.Usage(), Property: expr.Properties{Volatility: expr.Stable}}, Signature: signature, Args: test}
			value, err := node.Eval(&exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}})
			if err != nil || !value.IsNull() || value.Type() != textType {
				t.Fatal(constraint, nullable, value, err)
			}
		}
	}
	run(t, e, "CREATE TABLE t(prefix TEXT,a INT,b INT,CONSTRAINT c CHECK(b>0))")
	original := e.CatalogRead().MustRelation(roleTableRef(t, e, "t").Address)
	node := original.Constraints.Check[0].Expression
	run(t, e, "ALTER TABLE t DROP prefix")
	run(t, e, `CREATE TABLE supplied(x TEXT,y TEXT,"Renamed value" TEXT)`)
	current := e.CatalogRead()
	supplied := current.MustRelation(roleTableRef(t, e, "supplied").Address)
	got, err := (stored.Deparser{View: current, Relation: supplied, Pretty: true}).SQL(context.Background(), node, stored.DeparseParent{})
	if err != nil || got != `"Renamed value" > 0` {
		t.Fatal(got, err)
	}
	if _, err := (stored.Deparser{View: current}).SQL(context.Background(), node, stored.DeparseParent{}); err == nil {
		t.Fatal("Var accepted with zero relation")
	}
	before := e.Catalog.NextOID()
	if _, err := testDescribe(e, statement(t, "SELECT pg_get_expr($1,$2,$3)")); err != nil {
		t.Fatal(err)
	}
	if e.Catalog.NextOID() != before {
		t.Fatal("Describe allocated definition identities")
	}
}

func TestDeparseConstraintsTypmodsAndLiteralVariants(t *testing.T) {
	e := New()
	view := e.CatalogRead()
	for _, tc := range []struct {
		id              sqltype.ID
		input, expected string
		modifiers       []int32
	}{
		{sqltype.Int4, "-1", "'-1'::integer", nil},
		{sqltype.Numeric, "1", "'1'::numeric", nil},
		{sqltype.Numeric, "-1.20", "'-1.20'::numeric", nil},
		{sqltype.Numeric, "1.20", "1.20::numeric(4,2)", []int32{4, 2}},
		{sqltype.BPChar, "xy", "'xy '::character(3)", []int32{3}},
		{sqltype.VarChar, "xy", "'xy'::character varying(3)", []int32{3}},
		{sqltype.Time, "12:34:56.123", "'12:34:56.123'::time(3) without time zone", []int32{3}},
		{sqltype.Timestamp, "2026-10-08 12:34:56.123", "'2026-10-08 12:34:56.123'::timestamp(3) without time zone", []int32{3}},
		{sqltype.Timetz, "12:34:56.123+00", "'12:34:56.123+00'::time(3) with time zone", []int32{3}},
		{sqltype.Timestamptz, "2026-10-08 12:34:56.123+00", "'2026-10-08 12:34:56.123+00'::timestamp(3) with time zone", []int32{3}},
		{sqltype.Date, "infinity", "'infinity'::date", nil},
		{sqltype.Date, "-infinity", "'-infinity'::date", nil},
		{sqltype.Timestamp, "infinity", "'infinity'::timestamp without time zone", nil},
		{sqltype.Timestamp, "-infinity", "'-infinity'::timestamp without time zone", nil},
		{sqltype.Timestamptz, "infinity", "'infinity'::timestamp with time zone", nil},
		{sqltype.Timestamptz, "-infinity", "'-infinity'::timestamp with time zone", nil},
		{sqltype.Float4, "NaN", "'NaN'::real", nil},
		{sqltype.Float4, "Infinity", "'Infinity'::real", nil},
		{sqltype.Float4, "-Infinity", "'-Infinity'::real", nil},
		{sqltype.Float8, "1.5", "'1.5'::double precision", nil},
	} {
		t.Run(fmt.Sprintf("%d/%s/%v", tc.id, tc.input, tc.modifiers), func(t *testing.T) {
			typ, _ := view.LookupType(tc.id)
			u := typ.Usage()
			if tc.modifiers != nil {
				var err error
				u, _, err = typ.ResolveModifiers(tc.modifiers...)
				if err != nil {
					t.Fatal(err)
				}
			}
			value, err := typ.Input(context.Background(), tc.input, u.Typmod)
			if err != nil {
				t.Fatal(err)
			}
			node := plan.LiteralExpr{Info: expr.Info{Usage: u}, Value: value}
			for _, pretty := range []bool{false, true} {
				got, err := (stored.Deparser{View: view, Pretty: pretty}).SQL(context.Background(), node, stored.DeparseParent{})
				if err != nil || got != tc.expected {
					t.Fatal(got, err, tc.expected)
				}
			}
		})
	}
}

func TestDeparseConstraintsAllocatorOccupancy(t *testing.T) {
	e := New()
	run(t, e, "CREATE TABLE t(a INT DEFAULT 1 NOT NULL,CHECK(a>0))")
	view := e.CatalogRead()
	d := view.MustRelation(roleTableRef(t, e, "t").Address)
	for _, id := range []ids.OID{d.ColumnMeta.Defaults[1].OID, d.Constraints.NotNull[0].OID, d.Constraints.Check[0].OID} {
		e.Mu.Lock()
		e.Catalog.InstallOIDFrontierLocked(id)
		next, err := e.Catalog.AllocateOIDLocked()
		e.Mu.Unlock()
		if err != nil || next == id || catalog.DefinitionOIDUsed(d, next) {
			t.Fatal(id, next, err)
		}
	}
	bad := catalog.CloneState(view.State())
	copy := *d
	copy.Constraints.Check = append([]catalog.CheckConstraint(nil), d.Constraints.Check...)
	copy.Constraints.Check[0].OID = d.ColumnMeta.Defaults[1].OID
	bad.SetDescriptor(d.Address(), &copy)
	if err := catalog.ValidateDefinitionOIDs(e.Catalog.Read(bad)); err == nil {
		t.Fatal("duplicate identity accepted")
	}
	e.Mu.Lock()
	err := control.RemapDefinitionOIDs(e.Catalog.Read(bad), e.Catalog.AllocateOIDLocked)
	e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if err := catalog.ValidateDefinitionOIDs(e.Catalog.Read(bad)); err != nil {
		t.Fatal(err)
	}
	if d.Constraints.Check[0].OID == d.ColumnMeta.Defaults[1].OID {
		t.Fatal("remap mutated original")
	}
}

func TestDeparseConstraintsVolatileTreesDoNotExecute(t *testing.T) {
	e := New()
	run(t, e, "CREATE SEQUENCE s START 41")
	run(t, e, "CREATE TABLE t(a INT,CHECK(nextval('s')>0 AND random()>0))")
	view := e.CatalogRead()
	d := view.MustRelation(roleTableRef(t, e, "t").Address)
	typ, _ := view.LookupType(sqltype.PgNodeTree)
	tree, err := plan.CatalogTree(typ, d.Constraints.Check[0].Expression)
	if err != nil {
		t.Fatal(err)
	}
	for _, pretty := range []bool{false, true} {
		got, err := (stored.Deparser{View: view, Relation: d, Pretty: pretty, Path: []ids.ObjectAddress{{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}}}).SQL(context.Background(), d.Constraints.Check[0].Expression, stored.DeparseParent{})
		want := "((nextval('s'::regclass) > 0) AND (random() > (0)::double precision))"
		if pretty {
			want = "nextval('s'::regclass) > 0 AND random() > 0::double precision"
		}
		if err != nil || got != want {
			t.Fatal(got, err, want)
		}
		if _, err := typ.Output(context.Background(), tree); err != nil {
			t.Fatal(err)
		}
	}
	if got := tuples(run(t, e, "SELECT nextval('s')")); len(got) != 1 || got[0][0] != int64(41) {
		t.Fatal("deparse drew sequence", got)
	}
}
