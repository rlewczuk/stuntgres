package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceDomainAlterNotNullNativeControls(t *testing.T) {
	domainAlterValueScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fixture.Recorder) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
		for _, q := range []string{
			"CREATE DOMAIN domain_s.d AS integer",
			"CREATE DOMAIN domain_s.child AS domain_s.d",
			"CREATE DOMAIN domain_s.checked AS integer CONSTRAINT positive CHECK(VALUE>0)",
			"ALTER DOMAIN domain_s.d SET NOT NULL",
			"ALTER DOMAIN domain_s.checked SET NOT NULL",
			"CREATE TABLE domain_s.t(id integer,v domain_s.d,w domain_s.checked)",
			"CREATE TABLE domain_s.derived(v domain_s.child)",
		} {
			run(fixture.Exec(q, 0))
		}
		run(fixture.Exec("INSERT INTO domain_s.t VALUES(1,2,3)", 1))
		run(fixture.Exec("INSERT INTO domain_s.t SELECT 2,r.v,r.w FROM public.one o LEFT JOIN domain_s.t r ON false", 1))
		check := func(pair *fixture.Pair, x fixture.Executor, rec *fixture.Recorder, pg bool) {
			fixture.RunStep(t, pair.Ctx, x, rec, fixture.Select("SELECT id,v,w FROM domain_s.t ORDER BY id", []string{"id", "v", "w"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(1), int64(2), int64(3)}, {int64(2), nil, nil}}))
			fixture.RunStep(t, pair.Ctx, x, rec, fixture.Select("SELECT t.typname,c.conname,c.contype::text AS kind,pg_get_constraintdef(c.oid) AS definition FROM pg_type t JOIN pg_constraint c ON c.contypid=t.oid JOIN pg_namespace n ON n.oid=t.typnamespace WHERE n.nspname='domain_s' ORDER BY t.typname,c.conname", []string{"typname", "conname", "kind", "definition"}, []string{"NAME", "NAME", "TEXT", "TEXT"}, [][]any{{"checked", "checked_not_null", "n", "NOT NULL"}, {"checked", "positive", "c", "CHECK ((VALUE > 0))"}, {"d", "d_not_null", "n", "NOT NULL"}}))
			for _, prepared := range []bool{false, true} {
				fixture.RunStep(t, pair.Ctx, x, rec, domainDefaultFailure("INSERT INTO domain_s.t VALUES(3,NULL,3)", domainNotNullDiagnostic("domain_s", "d"), prepared, false))
				fixture.RunStep(t, pair.Ctx, x, rec, domainDefaultFailure("INSERT INTO domain_s.derived VALUES(NULL)", domainNotNullDiagnostic("domain_s", "child"), prepared, false))
				fixture.RunStep(t, pair.Ctx, x, rec, domainCheckFailure("INSERT INTO domain_s.t VALUES(3,2,-1)", "checked", "positive", false, prepared))
				s := fixture.Select("SELECT v,w FROM domain_s.t WHERE false", []string{"v", "w"}, []string{"INT4", "INT4"}, nil)
				s.Prepared = prepared
				s.CheckMetadata = func(t *testing.T, rs *sql.Rows) { domainReferenceCheckMetadata(t, rs, pg, false, s.Types) }
				fixture.RunStep(t, pair.Ctx, x, rec, s)
			}
			ids := map[int64]bool{}
			for _, q := range []string{"SELECT 'domain_s.d'::regtype::oid", "SELECT 'domain_s.checked'::regtype::oid", "SELECT 'domain_s.child'::regtype::oid", "SELECT oid FROM pg_constraint WHERE contypid='domain_s.d'::regtype::oid", "SELECT oid FROM pg_constraint WHERE contypid='domain_s.checked'::regtype::oid AND contype='n'", "SELECT oid FROM pg_constraint WHERE contypid='domain_s.checked'::regtype::oid AND contype='c'"} {
				id := domainAlterOID(t, pair, x, q)
				if ids[id] {
					t.Fatal("replay aliased genuine definitions", id)
				}
				ids[id] = true
			}
			fixture.RunStep(t, pair.Ctx, x, rec, fixture.Select("SELECT count(*) AS n FROM pg_depend d JOIN pg_constraint c ON c.oid=d.objid JOIN pg_type t ON t.oid=c.contypid JOIN pg_namespace ns ON ns.oid=t.typnamespace WHERE ns.nspname='domain_s' AND c.contype='n' AND d.classid='pg_constraint'::regclass::oid AND d.refclassid='pg_type'::regclass::oid AND d.refobjid=c.contypid AND d.deptype='a'", []string{"n"}, []string{"INT8"}, [][]any{{int64(2)}}))
		}
		check(p, c, r, b.PG)
		run(fixture.Exec("ALTER DOMAIN domain_s.d SET NOT NULL", 0))
		if b.PG {
			return
		}
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		exercise := func(pair *fixture.Pair, x fixture.Executor, rec *fixture.Recorder) {
			check(pair, x, rec, false)
			fixture.RunStep(t, pair.Ctx, x, rec, fixture.Exec("ALTER DOMAIN domain_s.d SET NOT NULL", 0))
			fixture.RunStep(t, pair.Ctx, x, rec, fixture.Exec("ALTER DOMAIN domain_s.d DROP NOT NULL", 0))
			fixture.RunStep(t, pair.Ctx, x, rec, domainAlterNullFailure("ALTER DOMAIN domain_s.d SET NOT NULL", "t", "v"))
			fixture.RunStep(t, pair.Ctx, x, rec, fixture.Prepared(fixture.Exec("INSERT INTO domain_s.derived VALUES(NULL)", 1)))
			fixture.RunStep(t, pair.Ctx, x, rec, fixture.Exec("INSERT INTO domain_s.t VALUES(3,NULL,4)", 1))
			for _, s := range domainAlterOwnNull("d", false, nil) {
				fixture.RunStep(t, pair.Ctx, x, rec, s)
			}
		}
		exercise(p, c, r)
		target := fixture.NewFixture(t, false, true)
		fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, fixture.Exec("CREATE DOMAIN public.spacer AS integer NOT NULL", 0))
		if err := stuntgres.Restore(target.Ctx, target.Native, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		exercise(target, target.Memory, target.MemoryRecord)
		fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, fixture.Exec("CREATE TABLE public.sentinel(v integer)", 0))
		fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, fixture.Exec("INSERT INTO public.sentinel VALUES(42)", 1))
		// Restore's ALTER allow-list remains bounded to captured CHECK replay;
		// this admitted CREATE/input failure still proves replacement atomicity.
		err := stuntgres.Restore(target.Ctx, target.Native, strings.NewReader("CREATE DOMAIN public.bad AS integer NOT NULL; CREATE TABLE public.bad_rows(v public.bad); INSERT INTO public.bad_rows VALUES(NULL);"))
		fixture.State(t, err, "23502")
		ctx, cancel := context.WithCancel(target.Ctx)
		cancel()
		if err := stuntgres.Restore(ctx, target.Native, bytes.NewReader(dump.Bytes())); !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled restore=%v", err)
		}
		fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, fixture.Select("SELECT v FROM public.sentinel", []string{"v"}, []string{"INT4"}, [][]any{{int64(42)}}))
		for _, s := range domainAlterOwnNull("d", false, nil) {
			fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, s)
		}
		fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, fixture.Exec("INSERT INTO domain_s.derived VALUES(NULL)", 1))
		fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, fixture.Select("SELECT count(*) AS n FROM domain_s.derived", []string{"n"}, []string{"INT8"}, [][]any{{int64(2)}}))
	})
}
