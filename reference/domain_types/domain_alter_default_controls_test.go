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

func TestReferenceDomainAlterDefaultNativeControls(t *testing.T) {
	domainAlterValueScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fixture.Recorder) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
		for _, q := range []string{
			"CREATE DOMAIN domain_s.p AS integer DEFAULT 7",
			"CREATE DOMAIN domain_s.c AS domain_s.p",
			"CREATE DOMAIN domain_s.z AS domain_s.p",
			"ALTER DOMAIN domain_s.z DROP DEFAULT",
			"ALTER DOMAIN domain_s.p SET DEFAULT 11",
			"CREATE DOMAIN domain_s.late AS domain_s.p",
			"CREATE TABLE domain_s.t(id integer,p domain_s.p,c domain_s.c,z domain_s.z,o domain_s.p DEFAULT 99,l domain_s.late)",
			"CREATE DOMAIN domain_s.required AS integer NOT NULL",
			"CREATE DOMAIN domain_s.wrapped AS domain_s.required DEFAULT NULL",
			"CREATE DOMAIN domain_s.wrapped_child AS domain_s.wrapped",
			"ALTER DOMAIN domain_s.wrapped DROP DEFAULT",
			"CREATE DOMAIN domain_s.num AS numeric(5,2) DEFAULT 7",
			"ALTER DOMAIN domain_s.num SET DEFAULT NULL",
			"CREATE TABLE domain_s.wrap_rows(v domain_s.wrapped_child)",
			"CREATE TABLE domain_s.num_rows(v domain_s.num)",
			"CREATE SEQUENCE domain_s.demands START 100",
			"CREATE DOMAIN domain_s.counted AS integer DEFAULT 7 CHECK(nextval('domain_s.demands'::regclass)>0)",
			"CREATE DOMAIN domain_s.copied AS domain_s.counted",
			"ALTER DOMAIN domain_s.counted SET DEFAULT 11",
			"CREATE TABLE domain_s.count_rows(v domain_s.copied)",
			"CREATE TYPE domain_s.pair AS(retired integer,kept integer)",
			"CREATE DOMAIN domain_s.field_parent AS integer DEFAULT (ROW(7,8)::domain_s.pair).kept",
			"CREATE DOMAIN domain_s.field_child AS domain_s.field_parent",
			"ALTER DOMAIN domain_s.field_parent SET DEFAULT 13",
			"ALTER TYPE domain_s.pair DROP ATTRIBUTE retired RESTRICT",
			"CREATE TABLE domain_s.field_rows(v domain_s.field_child)",
		} {
			run(fixture.Exec(q, 0))
		}
		run(fixture.Exec("INSERT INTO domain_s.t(id) VALUES(1)", 1))
		run(fixture.Exec("INSERT INTO domain_s.count_rows DEFAULT VALUES", 1))
		run(fixture.Exec("INSERT INTO domain_s.field_rows DEFAULT VALUES", 1))
		run(fixture.Select("SELECT typdefault FROM pg_type WHERE oid='domain_s.field_child'::regtype::oid", []string{"typdefault"}, []string{"TEXT"}, [][]any{{"(ROW(7, 8)::domain_s.pair).kept"}}))
		check := func(pair *fixture.Pair, x fixture.Executor, rec *fixture.Recorder, after bool) {
			rows := [][]any{{int64(1), int64(11), int64(7), nil, int64(99), int64(11)}}
			if after {
				rows = append(rows, []any{int64(2), int64(11), int64(7), nil, int64(99), int64(11)})
			}
			for _, prepared := range []bool{false, true} {
				s := fixture.Select("SELECT id,p,c,z,o,l FROM domain_s.t ORDER BY id", []string{"id", "p", "c", "z", "o", "l"}, []string{"INT4", "INT4", "INT4", "INT4", "INT4", "INT4"}, rows)
				s.Prepared = prepared
				s.CheckMetadata = func(t *testing.T, rs *sql.Rows) { domainReferenceCheckMetadata(t, rs, b.PG, false, s.Types) }
				fixture.RunStep(t, pair.Ctx, x, rec, s)
				fixture.RunStep(t, pair.Ctx, x, rec, domainDefaultFailure("INSERT INTO domain_s.wrap_rows DEFAULT VALUES", domainNotNullDiagnostic("domain_s", "required"), prepared, false))
				numRows := [][]any(nil)
				if after {
					numRows = [][]any{{nil}}
				}
				numeric := fixture.Select("SELECT v FROM domain_s.num_rows", []string{"v"}, []string{"NUMERIC"}, numRows)
				numeric.Prepared = prepared
				numeric.CheckMetadata = func(t *testing.T, rs *sql.Rows) { domainReferenceCheckMetadata(t, rs, b.PG, false, numeric.Types) }
				fixture.RunStep(t, pair.Ctx, x, rec, numeric)
			}
			fixture.RunStep(t, pair.Ctx, x, rec, fixture.Select("SELECT typname,typdefaultbin IS NOT NULL AS recipe FROM pg_type t JOIN pg_namespace n ON n.oid=t.typnamespace WHERE n.nspname='domain_s' AND typname IN('p','c','z','late','wrapped','wrapped_child','num') ORDER BY typname", []string{"typname", "recipe"}, []string{"NAME", "BOOL"}, [][]any{{"c", true}, {"late", true}, {"num", true}, {"p", true}, {"wrapped", false}, {"wrapped_child", true}, {"z", false}}))
			n := int64(100)
			if after {
				n = 101
			}
			fixture.RunStep(t, pair.Ctx, x, rec, fixture.Select("SELECT last_value,is_called FROM domain_s.demands", []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, [][]any{{n, true}}))
			fieldRows := [][]any{{int64(8)}}
			if after {
				fieldRows = append(fieldRows, []any{int64(8)})
			}
			fixture.RunStep(t, pair.Ctx, x, rec, fixture.Select("SELECT v FROM domain_s.field_rows", []string{"v"}, []string{"INT4"}, fieldRows))
		}
		check(p, c, r, false)
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
			fixture.RunStep(t, pair.Ctx, x, rec, fixture.Prepared(fixture.Exec("INSERT INTO domain_s.t(id) VALUES(2)", 1)))
			fixture.RunStep(t, pair.Ctx, x, rec, fixture.Exec("INSERT INTO domain_s.count_rows DEFAULT VALUES", 1))
			fixture.RunStep(t, pair.Ctx, x, rec, fixture.Exec("INSERT INTO domain_s.num_rows DEFAULT VALUES", 1))
			fixture.RunStep(t, pair.Ctx, x, rec, fixture.Exec("INSERT INTO domain_s.field_rows DEFAULT VALUES", 1))
			fixture.RunStep(t, pair.Ctx, x, rec, fixture.Select("SELECT v FROM domain_s.num_rows", []string{"v"}, []string{"NUMERIC"}, [][]any{{nil}}))
			check(pair, x, rec, true)
			fixture.RunStep(t, pair.Ctx, x, rec, fixture.Select("SELECT typdefault FROM pg_type WHERE oid='domain_s.field_child'::regtype::oid", []string{"typdefault"}, []string{"TEXT"}, [][]any{{"(ROW(8)::domain_s.pair).kept"}}))
			fixture.RunStep(t, pair.Ctx, x, rec, fixture.Exec("ALTER DOMAIN domain_s.p SET DEFAULT 13", 0))
			fixture.RunStep(t, pair.Ctx, x, rec, fixture.Exec("INSERT INTO domain_s.t(id) VALUES(3)", 1))
			fixture.RunStep(t, pair.Ctx, x, rec, fixture.Select("SELECT p,c,z,l FROM domain_s.t WHERE id=3", []string{"p", "c", "z", "l"}, []string{"INT4", "INT4", "INT4", "INT4"}, [][]any{{int64(13), int64(7), nil, int64(11)}}))
		}
		exercise(p, c, r)
		target := fixture.NewFixture(t, false, true)
		fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, fixture.Exec("CREATE DOMAIN public.spacer AS integer NOT NULL", 0))
		if err := stuntgres.Restore(target.Ctx, target.Native, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		exercise(target, target.Memory, target.MemoryRecord)
		for _, q := range []string{"CREATE TABLE public.sentinel(v integer)", "INSERT INTO public.sentinel VALUES(42)"} {
			count := int64(0)
			if strings.HasPrefix(q, "INSERT") {
				count = 1
			}
			fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, fixture.Exec(q, count))
		}
		for _, script := range []string{"CREATE DOMAIN public.bad AS integer DEFAULT 2; nonsense;", "CREATE DOMAIN public.bad AS integer NOT NULL; CREATE TABLE public.bad_rows(v public.bad); INSERT INTO public.bad_rows VALUES(NULL);"} {
			if err := stuntgres.Restore(target.Ctx, target.Native, strings.NewReader(script)); err == nil {
				t.Fatal("invalid restore succeeded")
			}
		}
		ctx, cancel := context.WithCancel(target.Ctx)
		cancel()
		if err := stuntgres.Restore(ctx, target.Native, bytes.NewReader(dump.Bytes())); !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled restore=%v", err)
		}
		fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, fixture.Select("SELECT v FROM public.sentinel", []string{"v"}, []string{"INT4"}, [][]any{{int64(42)}}))
		fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, fixture.Select("SELECT typname FROM pg_type WHERE typname='bad'", []string{"typname"}, []string{"NAME"}, nil))
		fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, fixture.Exec("INSERT INTO domain_s.t(id) VALUES(4)", 1))
		fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, fixture.Select("SELECT p,c,z,l FROM domain_s.t WHERE id=4", []string{"p", "c", "z", "l"}, []string{"INT4", "INT4", "INT4", "INT4"}, [][]any{{int64(13), int64(7), nil, int64(11)}}))
	})
}
