package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceACLValuesLifecycle(t *testing.T) {
	aclValuesScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		q := "SELECT * FROM aclexplode(acldefault($1::\"char\",10))"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		rows, err := stmt.QueryContext(p.Ctx, "r")
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		if !rows.Next() {
			t.Fatalf("missing first expansion row: %v", rows.Err())
		}
		if err := rows.Close(); err != nil {
			t.Fatal(err)
		}
		for _, code := range []string{"c", "r", "c", "r"} {
			var want [][]any
			if code == "r" {
				want = aclValuesRows(b.PG, 10, 10, "INSERT,SELECT,UPDATE,DELETE,TRUNCATE,REFERENCES,TRIGGER,MAINTAIN", false)
			}
			s := aclValuesExplode(q, want)
			s.Args = []any{code}
			fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
		}
		bad := fixture.ErrorDiagnostic(q, aclitemDiagnostic("XX000", "unrecognized object type abbreviation: z"), "z")
		bad.SelectRows = true
		bad.ErrorStage = "execute"
		fixture.RunStep(t, p.Ctx, held, b.Recorder, bad)
		s := aclValuesExplode(q, nil)
		s.Args = []any{"c"}
		fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
		// A finite ACL expansion provides no stable PostgreSQL admission window.
		// Native precancellation is an ordinary context boundary, not SQLSTATE.
		if !b.PG {
			ctx, cancel := context.WithCancel(p.Ctx)
			cancel()
			rows, err := stmt.QueryContext(ctx, "r")
			if rows != nil || !errors.Is(err, context.Canceled) {
				t.Fatalf("precanceled expansion rows=%v err=%v", rows, err)
			}
			fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
		}
	})
}

func TestReferenceACLValuesSmoke(t *testing.T) {
	aclValuesScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		owner := aclValuesOwner(b.PG)
		s := aclValuesExplode("SELECT * FROM aclexplode(acldefault('r',10)) ORDER BY privilege_type", aclValuesRows(b.PG, 10, 10, "DELETE,INSERT,MAINTAIN,REFERENCES,SELECT,TRIGGER,TRUNCATE,UPDATE", false))
		s.Bag = false
		run(s)
		run(fixture.Prepared(s))
		run(aclitemSelect("SELECT makeaclitem(10,10,' select, INSERT, update ,DELETE ',true) AS a", [][]any{{owner + "=a*r*w*d*/" + owner}}))
		run(fixture.Select("SELECT ARRAY[makeaclitem(10,10,'SELECT,UPDATE',true)] @> makeaclitem(10,10,'SELECT',true) AS yes,ARRAY[makeaclitem(10,10,'SELECT',false),makeaclitem(10,10,'UPDATE',false)] @> makeaclitem(10,10,'SELECT,UPDATE',false) AS no", []string{"yes", "no"}, []string{"BOOL", "BOOL"}, [][]any{{true, false}}))
		run(aclValuesExplode("SELECT * FROM aclexplode(NULL::aclitem[])", nil))
		run(aclValuesExplode("SELECT * FROM aclexplode(acldefault('c',0))", nil))
		run(fixture.ErrorDiagnostic("SELECT makeaclitem(10,10,'bad',false)", aclitemDiagnostic("22023", `unrecognized privilege type: "bad"`)))
		run(fixture.ErrorDiagnostic("SELECT acldefault('z',10)", aclitemDiagnostic("XX000", "unrecognized object type abbreviation: z")))
		run(fixture.Exec("CREATE TABLE acl_values_smoke(a aclitem,aa aclitem[],empty_acl aclitem[])", 0))
		run(fixture.Exec("INSERT INTO acl_values_smoke VALUES (makeaclitem(10,10,'SELECT',true),acldefault('r',10),acldefault('c',0))", 1))
		check := fixture.Select("SELECT a,aa FROM acl_values_smoke", []string{"a", "aa"}, []string{"ACLITEM", "_ACLITEM"}, [][]any{{owner + "=r*/" + owner, "{" + owner + "=arwdDxtm/" + owner + "}"}})
		run(check)
		if !b.PG {
			fixture.ArrayReferenceNativeRoundtrip(t, p, c, b, check)
		}
		run(aclValuesExplode("SELECT x.* FROM acl_values_smoke s,LATERAL aclexplode(s.empty_acl) x", nil))
	})
}
