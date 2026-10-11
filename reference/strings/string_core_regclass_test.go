package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func TestReferenceStringCoreRegclassNamespaceOutput(t *testing.T) {
	stringCoreReferenceRegclass(t, fixture.NewScenario(t))
}

// Expected names are specified from SQL namespace visibility, never an OID
// allocator or one server's output copied to the other. Each selected backend
// executes the same direct, held and fresh-prepared consumers.
func stringCoreReferenceRegclass(t *testing.T, p *fixture.Pair) {
	t.Helper()
	// The shared fixture resets public only; this case owns these two namespaces.
	t.Cleanup(func() {
		if p.Postgres == nil {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if _, err := p.Postgres.ExecContext(ctx, "DROP SCHEMA IF EXISTS sgreg_a CASCADE; DROP SCHEMA IF EXISTS sgreg_b CASCADE"); err != nil {
			t.Errorf("regclass namespace cleanup: %v", err)
		}
	})
	step := func(s fixture.Step) {
		t.Helper()
		p.Step(t, s)
	}
	for _, q := range []string{"CREATE SCHEMA sgreg_a", "CREATE SCHEMA sgreg_b", `CREATE TABLE sgreg_a."Mixed Name"(id INT)`, `CREATE TABLE sgreg_b."Mixed Name"(id INT)`, "CREATE TABLE public.sgreg_sink(id INT,v TEXT)",
		`CREATE TABLE public.sgreg_store(id INT,v TEXT DEFAULT btrim(' stored '::char(10)),CHECK(v IS NORMALIZED AND ('['::text||'sgreg_a."Mixed Name"'::regclass) IS NORMALIZED))`,
		`CREATE VIEW public.sgreg_view AS SELECT btrim('['::text||'sgreg_a."Mixed Name"'::regclass) AS v`,
	} {
		step(fixture.Exec(q, 0))
	}
	for _, backend := range p.Backends() {
		db := backend.DB
		db.SetMaxOpenConns(1)
		c, err := db.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		func() {
			defer c.Close()
			exec := func(q string) {
				t.Helper()
				if _, err := c.ExecContext(p.Ctx, q); err != nil {
					t.Fatal(q, err)
				}
			}
			forward, err := c.PrepareContext(p.Ctx, `SELECT '['::text||'sgreg_a."Mixed Name"'::regclass AS v`)
			if err != nil {
				t.Fatal(err)
			}
			defer forward.Close()
			reverse, err := c.PrepareContext(p.Ctx, `SELECT 'sgreg_a."Mixed Name"'::regclass||']'::text AS v`)
			if err != nil {
				t.Fatal(err)
			}
			defer reverse.Close()
			insert, err := c.PrepareContext(p.Ctx, "INSERT INTO public.sgreg_store(id) VALUES($1)")
			if err != nil {
				t.Fatal(err)
			}
			defer insert.Close()
			for i, tc := range []struct{ path, spelling string }{{"sgreg_a, public", `"Mixed Name"`}, {"sgreg_b, public", `sgreg_a."Mixed Name"`}} {
				exec("SET search_path=" + tc.path)
				var path string
				if err := c.QueryRowContext(p.Ctx, "SHOW search_path").Scan(&path); err != nil || path != tc.path {
					t.Fatal("search path fixture", path, tc.path, err)
				}
				for _, held := range []struct {
					stmt *sql.Stmt
					want string
				}{{forward, "[" + tc.spelling}, {reverse, tc.spelling + "]"}} {
					var got string
					if err := held.stmt.QueryRowContext(p.Ctx).Scan(&got); err != nil || got != held.want {
						t.Fatalf("held regclass output got %q want %q err %v", got, held.want, err)
					}
				}
				var recorder *fixture.Recorder
				for _, backend := range p.Backends() {
					if backend.DB == db {
						recorder = backend.Recorder
					}
				}
				for _, tc := range []struct{ q, want string }{
					{`SELECT '['::text||'sgreg_a."Mixed Name"'::regclass AS v`, "[" + tc.spelling},
					{`SELECT 'sgreg_a."Mixed Name"'::regclass||']'::text AS v`, tc.spelling + "]"},
					{"SELECT v FROM public.sgreg_view", "[" + tc.spelling},
				} {
					step := fixture.Select(tc.q, []string{"v"}, []string{"TEXT"}, [][]any{{tc.want}})
					fixture.RunStep(t, p.Ctx, c, recorder, step)
					fixture.RunStep(t, p.Ctx, c, recorder, fixture.Prepared(step))
				}
				if _, err := insert.ExecContext(p.Ctx, i+1); err != nil {
					t.Fatal("default/CHECK namespace snapshot", err)
				}
				var viewOutput string
				if err := c.QueryRowContext(p.Ctx, "SELECT v FROM public.sgreg_view").Scan(&viewOutput); err != nil || viewOutput != "["+tc.spelling {
					t.Fatal("stored view namespace output", viewOutput, err)
				}
			}
		}()
	}
	stringCoreReferenceValues(t, p, []stringCoreReferenceCase{
		{`SELECT '['::text||'sgreg_a."Mixed Name"'::regclass AS v`, `[sgreg_a."Mixed Name"`, "TEXT"},
		{`SELECT 'sgreg_a."Mixed Name"'::regclass||']'::text AS v`, `sgreg_a."Mixed Name"]`, "TEXT"},
		{"SELECT 'x'::text||'4294967294'::regclass AS v", "x4294967294", "TEXT"},
		{"SELECT '4294967294'::regclass||'x'::text AS v", "4294967294x", "TEXT"},
		{"SELECT '['::text||'-'::regclass AS v", "[-", "TEXT"},
		{"SELECT v FROM public.sgreg_view", `[sgreg_a."Mixed Name"`, "TEXT"},
	})
	step(fixture.Select("SELECT id,v FROM public.sgreg_store ORDER BY id", []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "stored"}, {int64(2), "stored"}}))
	for _, s := range []fixture.Step{
		fixture.Exec(`INSERT INTO public.sgreg_sink VALUES(1,'['::text||'sgreg_a."Mixed Name"'::regclass)`, 1),
		fixture.Prepared(fixture.Exec(`INSERT INTO public.sgreg_sink VALUES(2,'sgreg_a."Mixed Name"'::regclass||']'::text)`, 1)),
		fixture.Select("SELECT v FROM public.sgreg_sink WHERE id=1", []string{"v"}, []string{"TEXT"}, [][]any{{`[sgreg_a."Mixed Name"`}}),
		fixture.Exec(`UPDATE public.sgreg_sink SET v='<'::text||'sgreg_a."Mixed Name"'::regclass WHERE id=1`, 1),
		fixture.Prepared(fixture.Exec(`UPDATE public.sgreg_sink SET v='sgreg_a."Mixed Name"'::regclass||'>'::text WHERE id=2`, 1)),
		fixture.Select("SELECT v FROM public.sgreg_sink ORDER BY id", []string{"v"}, []string{"TEXT"}, [][]any{{`<sgreg_a."Mixed Name"`}, {`sgreg_a."Mixed Name">`}}),
		fixture.Exec(`DELETE FROM public.sgreg_sink WHERE v='<'::text||'sgreg_a."Mixed Name"'::regclass`, 1),
	} {
		step(s)
	}
	for prepared, name := range []string{"sgreg_alter_direct", "sgreg_alter_prepared"} {
		step(fixture.Exec("CREATE TABLE public."+name+"(v TEXT)", 0))
		step(fixture.Exec("INSERT INTO public."+name+" VALUES('!')", 1))
		action := fixture.Exec(`ALTER TABLE public.`+name+` ALTER COLUMN v TYPE TEXT USING v||'sgreg_a."Mixed Name"'::regclass`, 0)
		if prepared != 0 {
			action = fixture.Prepared(action)
		}
		step(action)
		step(fixture.Select("SELECT v FROM public."+name, []string{"v"}, []string{"TEXT"}, [][]any{{`!sgreg_a."Mixed Name"`}}))
	}
	stringCoreRegclassRestore(t, p)
}

func stringCoreRegclassRestore(t *testing.T, p *fixture.Pair) {
	t.Helper()
	step := func(s fixture.Step) { p.Step(t, s) }
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
	step(fixture.Exec("SET search_path=sgreg_b, public", 0))
	step(fixture.Exec("INSERT INTO public.sgreg_store(id) VALUES(3)", 1))
	step(fixture.Select("SELECT v FROM public.sgreg_store WHERE id=3", []string{"v"}, []string{"TEXT"}, [][]any{{"stored"}}))
	step(fixture.Select("SELECT v FROM public.sgreg_view", []string{"v"}, []string{"TEXT"}, [][]any{{`[sgreg_a."Mixed Name"`}}))
}
