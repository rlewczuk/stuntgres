package stored_test

import (
	"context"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
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
			e := engine.New()
			enginetest.Run(t, e, "CREATE SEQUENCE s")
			enginetest.Run(t, e, "CREATE TABLE t(a INT,flag BOOL,n NUMERIC,CONSTRAINT c CHECK("+tc.expr+"))")
			op, done, err := enginetest.Operation(e, context.Background())
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
			d := view.MustRelation(enginetest.RoleTableRef(t, e, "t").Address)
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

func TestDeparseConstraintsVolatileTreesDoNotExecute(t *testing.T) {
	e := engine.New()
	enginetest.Run(t, e, "CREATE SEQUENCE s START 41")
	enginetest.Run(t, e, "CREATE TABLE t(a INT,CHECK(nextval('s')>0 AND random()>0))")
	view := e.CatalogRead()
	d := view.MustRelation(enginetest.RoleTableRef(t, e, "t").Address)
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
	if got := enginetest.Tuples(enginetest.Run(t, e, "SELECT nextval('s')")); len(got) != 1 || got[0][0] != int64(41) {
		t.Fatal("deparse drew sequence", got)
	}
}
