package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceDomainDefaultNativeControls(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"domain_default_owner"})
	domainDefaultScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fixture.Recorder) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
		for _, q := range []string{
			"CREATE ROLE domain_default_owner",
			"CREATE SCHEMA domain_other",
			"CREATE DOMAIN domain_s.\"Parent Default\" AS integer DEFAULT 11 NOT NULL",
			"ALTER DOMAIN domain_s.\"Parent Default\" OWNER TO domain_default_owner",
			"CREATE DOMAIN domain_other.\"Child Default\" AS domain_s.\"Parent Default\" DEFAULT 22",
			"CREATE DOMAIN domain_s.\"Nullable Default\" AS integer DEFAULT 11",
			"CREATE DOMAIN domain_other.\"Null Mask\" AS domain_s.\"Nullable Default\" DEFAULT NULL",
			"CREATE SEQUENCE domain_s.persist_steps START 50",
			"CREATE DOMAIN domain_s.\"Provider Default\" AS bigint DEFAULT nextval('domain_s.persist_steps'::regclass)",
			"CREATE TABLE domain_other.\"Default Rows\"(id integer,v domain_other.\"Child Default\",w domain_other.\"Child Default\" DEFAULT 44,z domain_other.\"Null Mask\",m domain_s.\"Nullable Default\" DEFAULT NULL,p domain_s.\"Provider Default\")",
			"CREATE VIEW domain_other.\"Default View\" AS SELECT id,v,w,z,m,p FROM domain_other.\"Default Rows\"",
			"CREATE DOMAIN domain_s.required_default AS integer DEFAULT NULL NOT NULL",
			"CREATE DOMAIN domain_other.inherited_required AS domain_s.required_default",
			"CREATE DOMAIN domain_other.explicit_required AS domain_s.required_default DEFAULT NULL",
			"CREATE TABLE domain_other.inherited_demand(v domain_other.inherited_required)",
			"CREATE TABLE domain_other.explicit_demand(v domain_other.explicit_required)",
		} {
			run(fixture.Exec(q, 0))
		}
		run(fixture.Exec("INSERT INTO domain_other.\"Default Rows\"(id) VALUES(1)", 1))
		check := func(pair *fixture.Pair, e fixture.Executor, rec *fixture.Recorder, after bool) {
			want := [][]any{{int64(1), int64(22), int64(44), nil, nil, int64(50)}}
			if after {
				want = append(want, []any{int64(2), int64(22), int64(44), nil, nil, int64(51)})
			}
			for _, prepared := range []bool{false, true} {
				s := fixture.Select("SELECT id,v,w,z,m,p FROM domain_other.\"Default View\" ORDER BY id", []string{"id", "v", "w", "z", "m", "p"}, []string{"INT4", "INT4", "INT4", "INT4", "INT4", "INT8"}, want)
				s.Prepared = prepared
				fixture.RunStep(t, pair.Ctx, e, rec, s)
				fixture.RunStep(t, pair.Ctx, e, rec, domainDefaultFailure("INSERT INTO domain_other.inherited_demand DEFAULT VALUES", domainNotNullDiagnostic("domain_other", "inherited_required"), prepared, false))
				fixture.RunStep(t, pair.Ctx, e, rec, domainDefaultFailure("INSERT INTO domain_other.explicit_demand DEFAULT VALUES", domainNotNullDiagnostic("domain_s", "required_default"), prepared, false))
			}
		}
		check(p, c, r, false)
		for _, prepared := range []bool{false, true} {
			run(domainDefaultFailure("INSERT INTO domain_other.\"Default Rows\"(id,v) VALUES(3,NULL)", domainNotNullDiagnostic("domain_other", `"Child Default"`), prepared, false))
		}
		check(p, c, r, false)
		// A failed earlier domain conversion must not demand the later provider
		// default. This shared witness independently owns the restore sequence.
		run(fixture.Select("SELECT last_value,is_called FROM domain_s.persist_steps", []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, [][]any{{int64(50), true}}))
		// Explicit native-only control phase: PostgreSQL independently exercises seed
		// and read above; native process-local replacement has no PostgreSQL API.
		if b.PG {
			return
		}
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
			t.Fatal(err)
		}
		check(p, c, r, false)
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump.String())); err != nil {
			t.Fatal(err)
		}
		exercise := func(pair *fixture.Pair, e fixture.Executor, rec *fixture.Recorder) {
			check(pair, e, rec, false)
			fixture.RunStep(t, pair.Ctx, e, rec, fixture.Prepared(fixture.Exec("INSERT INTO domain_other.\"Default Rows\"(id) VALUES(2)", 1)))
			check(pair, e, rec, true)
			d := fixture.ControlsReferenceDiagnostic("23502", `domain domain_other."Child Default" does not allow null values`)
			d.Schema = "domain_other"
			d.DataType = "Child Default"
			s := fixture.ErrorDiagnostic("INSERT INTO domain_other.\"Default Rows\"(id,v) VALUES(3,NULL)", d)
			s.ErrorStage = "execute"
			fixture.RunStep(t, pair.Ctx, e, rec, s)
			check(pair, e, rec, true)
			fixture.RunStep(t, pair.Ctx, e, rec, fixture.Select("SELECT t.typnotnull AS own,t.typdefault IS NOT NULL AS has_default FROM pg_type t JOIN pg_namespace n ON n.oid=t.typnamespace WHERE n.nspname='domain_other' AND t.typname='Child Default'", []string{"own", "has_default"}, []string{"BOOL", "BOOL"}, [][]any{{false, true}}))
			s = fixture.Select("SELECT v,w,z,m FROM domain_other.\"Default Rows\" WHERE false", []string{"v", "w", "z", "m"}, []string{"INT4", "INT4", "INT4", "INT4"}, nil)
			s.CheckMetadata = func(t *testing.T, rows *sql.Rows) { domainReferenceCheckMetadata(t, rows, false, true, s.Types) }
			fixture.RunStep(t, pair.Ctx, e, rec, s)
		}
		exercise(p, c, r)
		target := fixture.NewFixture(t, false, true)
		fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, fixture.Exec("CREATE ROLE default_spacer", 0))
		fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, fixture.Exec("CREATE ROLE domain_default_owner", 0))
		if err := stuntgres.Restore(target.Ctx, target.Native, strings.NewReader(dump.String())); err != nil {
			t.Fatal(err)
		}
		exercise(target, target.Memory, target.MemoryRecord)
		fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, fixture.Exec("CREATE TABLE public.default_sentinel(v integer)", 0))
		fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, fixture.Exec("INSERT INTO public.default_sentinel VALUES(42)", 1))
		err := stuntgres.Restore(target.Ctx, target.Native, strings.NewReader("CREATE DOMAIN public.bad_restore AS integer DEFAULT 7 NOT NULL; CREATE TABLE public.bad_rows(v public.bad_restore); INSERT INTO public.bad_rows VALUES(NULL);"))
		fixture.State(t, err, "23502")
		ctx, cancel := context.WithCancel(target.Ctx)
		cancel()
		if err := stuntgres.Restore(ctx, target.Native, strings.NewReader(dump.String())); !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled restore=%v", err)
		}
		fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, fixture.Select("SELECT v FROM public.default_sentinel", []string{"v"}, []string{"INT4"}, [][]any{{int64(42)}}))
		fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, fixture.Select("SELECT typname AS name FROM pg_type WHERE typname='bad_restore'", []string{"name"}, []string{"NAME"}, nil))
		check(target, target.Memory, target.MemoryRecord, true)
		fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, fixture.Exec("INSERT INTO domain_other.\"Default Rows\"(id) VALUES(4)", 1))
		fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, fixture.Select("SELECT v,p FROM domain_other.\"Default Rows\" WHERE id=4", []string{"v", "p"}, []string{"INT4", "INT8"}, [][]any{{int64(22), int64(52)}}))
		owner, err := target.Memory.Conn(target.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer owner.Close()
		// Exercise real namespace resolution privileges only after all states
		// that will be dumped; namespace ACL serialization is a separate boundary.
		fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, fixture.Exec("GRANT USAGE ON SCHEMA domain_s TO domain_default_owner", 0))
		for _, s := range []fixture.Step{
			fixture.Exec("SET ROLE default_spacer", 0),
			fixture.Error("DROP DOMAIN domain_s.\"Parent Default\"", "42501"),
			fixture.Exec("RESET ROLE", 0),
			fixture.Exec("SET ROLE domain_default_owner", 0),
			fixture.Error("DROP DOMAIN domain_s.\"Parent Default\"", "2BP01"),
			fixture.Exec("RESET ROLE", 0),
		} {
			fixture.RunStep(t, target.Ctx, owner, target.MemoryRecord, s)
		}
	})
}
