package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceACLItemSmoke(t *testing.T) {
	aclitemScenario(t, []string{"acl_smoke_a", "acl_smoke_b"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE public.acl_smoke (id INT,a aclitem,aa aclitem[])", 0))
		run(fixture.Exec("INSERT INTO public.acl_smoke VALUES (1,'acl_smoke_a=wr**/acl_smoke_b','{}')", 1))
		q := "INSERT INTO public.acl_smoke VALUES (2,$1::aclitem,$2::aclitem[])"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}, b.Recorder, fixture.Exec(q, 1, "acl_smoke_a=r*/acl_smoke_b", "[0:2]={=r*/postgres,NULL,postgres=/postgres}"))
		check := fixture.Select("SELECT a,aa FROM public.acl_smoke ORDER BY id", []string{"a", "aa"}, []string{"ACLITEM", "_ACLITEM"}, [][]any{{"acl_smoke_a=r*w/acl_smoke_b", "{}"}, {"acl_smoke_a=r*/acl_smoke_b", "[0:2]={=r*/postgres,NULL,postgres=/postgres}"}})
		check.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
			aclitemMetadata(t, b.PG, rows, check.Columns, check.Types, []bool{true, true}, false)
		}
		run(check)
		run(fixture.Prepared(check))
		for _, prepared := range []bool{false, true} {
			s := fixture.Select("SELECT a=$1::aclitem AS same,a=$2::aclitem AS different,aa=aa AS array_same,aa<>'{NULL}'::aclitem[] AS array_different FROM public.acl_smoke WHERE id=2", []string{"same", "different", "array_same", "array_different"}, []string{"BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{true, false, true, true}}, "acl_smoke_a=r*/acl_smoke_b", "acl_smoke_a=r/acl_smoke_b")
			s.Prepared = prepared
			run(s)
		}
		d := aclitemDiagnostic("42704", `role "missing_acl_role" does not exist`)
		d.Context = "unnamed portal parameter $1 = '...'"
		bad := fixture.ErrorDiagnostic("INSERT INTO public.acl_smoke VALUES (3,$1::aclitem,NULL)", d, "missing_acl_role=r/postgres")
		bad.ErrorStage = "execute"
		run(bad)
		run(fixture.Prepared(bad))
		run(check)
		if !b.PG {
			fixture.ArrayReferenceNativeRoundtrip(t, p, c, b, check, fixture.Exec("CREATE ROLE acl_smoke_spacer", 0), fixture.Exec("CREATE ROLE acl_smoke_a", 0), fixture.Exec("CREATE ROLE acl_smoke_b", 0))
		}
	})
}
