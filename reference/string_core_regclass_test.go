//go:build reference

package reference_test

import (
	"context"
	"database/sql"
	"testing"
	"time"
)

func TestReferenceStringCoreRegclassOracle(t *testing.T) {
	stringCoreReferenceRegclass(t, newReferencePair(t), true)
}
func TestReferenceStringCoreRegclassNamespaceOutput(t *testing.T) {
	stringCoreReferenceRegclass(t, newReferencePair(t), false)
}

// Expected names are specified from SQL namespace visibility, never an OID
// allocator or one server's output copied to the other. The oracle path executes
// only the real PostgreSQL connection before native parity is claimed.
func stringCoreReferenceRegclass(t *testing.T, p *referencePair, oracle bool) {
	t.Helper()
	// The shared fixture resets public only; this case owns these two namespaces.
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if _, err := p.postgres.ExecContext(ctx, "DROP SCHEMA IF EXISTS sgreg_a CASCADE; DROP SCHEMA IF EXISTS sgreg_b CASCADE"); err != nil {
			t.Errorf("regclass namespace cleanup: %v", err)
		}
	})
	step := func(s referenceStep) {
		t.Helper()
		if oracle {
			referenceRunStep(t, p.ctx, p.postgres, p.postgresRecord, s)
		} else {
			p.step(t, s)
		}
	}
	for _, q := range []string{"CREATE SCHEMA sgreg_a", "CREATE SCHEMA sgreg_b", `CREATE TABLE sgreg_a."Mixed Name"(id INT)`, `CREATE TABLE sgreg_b."Mixed Name"(id INT)`, "CREATE TABLE public.sgreg_sink(id INT,v TEXT)",
		`CREATE TABLE public.sgreg_store(id INT,v TEXT DEFAULT btrim(' stored '::char(10)),CHECK(v IS NORMALIZED AND ('['::text||'sgreg_a."Mixed Name"'::regclass) IS NORMALIZED))`,
		`CREATE VIEW public.sgreg_view AS SELECT btrim('['::text||'sgreg_a."Mixed Name"'::regclass) AS v`,
	} {
		step(referenceExec(q, 0))
	}
	dbs := []*sql.DB{p.memory, p.postgres}
	if oracle {
		dbs = []*sql.DB{p.postgres}
	}
	for _, db := range dbs {
		db.SetMaxOpenConns(1)
		c, err := db.Conn(p.ctx)
		if err != nil {
			t.Fatal(err)
		}
		func() {
			defer c.Close()
			exec := func(q string) {
				t.Helper()
				if _, err := c.ExecContext(p.ctx, q); err != nil {
					t.Fatal(q, err)
				}
			}
			forward, err := c.PrepareContext(p.ctx, `SELECT '['::text||'sgreg_a."Mixed Name"'::regclass AS v`)
			if err != nil {
				t.Fatal(err)
			}
			defer forward.Close()
			reverse, err := c.PrepareContext(p.ctx, `SELECT 'sgreg_a."Mixed Name"'::regclass||']'::text AS v`)
			if err != nil {
				t.Fatal(err)
			}
			defer reverse.Close()
			insert, err := c.PrepareContext(p.ctx, "INSERT INTO public.sgreg_store(id) VALUES($1)")
			if err != nil {
				t.Fatal(err)
			}
			defer insert.Close()
			for i, tc := range []struct{ path, spelling string }{{"sgreg_a, public", `"Mixed Name"`}, {"sgreg_b, public", `sgreg_a."Mixed Name"`}} {
				exec("SET search_path=" + tc.path)
				var path string
				if err := c.QueryRowContext(p.ctx, "SHOW search_path").Scan(&path); err != nil || path != tc.path {
					t.Fatal("search path fixture", path, tc.path, err)
				}
				for _, held := range []struct {
					stmt *sql.Stmt
					want string
				}{{forward, "[" + tc.spelling}, {reverse, tc.spelling + "]"}} {
					var got string
					if err := held.stmt.QueryRowContext(p.ctx).Scan(&got); err != nil || got != held.want {
						t.Fatalf("held regclass output got %q want %q err %v", got, held.want, err)
					}
				}
				if _, err := insert.ExecContext(p.ctx, i+1); err != nil {
					t.Fatal("default/CHECK namespace snapshot", err)
				}
				var viewOutput string
				if err := c.QueryRowContext(p.ctx, "SELECT v FROM public.sgreg_view").Scan(&viewOutput); err != nil || viewOutput != "["+tc.spelling {
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
	}, oracle)
	step(referenceSelect("SELECT id,v FROM public.sgreg_store ORDER BY id", []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "stored"}, {int64(2), "stored"}}))
	for _, s := range []referenceStep{
		referenceExec(`INSERT INTO public.sgreg_sink VALUES(1,'['::text||'sgreg_a."Mixed Name"'::regclass)`, 1),
		referencePrepared(referenceExec(`INSERT INTO public.sgreg_sink VALUES(2,'sgreg_a."Mixed Name"'::regclass||']'::text)`, 1)),
		referenceSelect("SELECT v FROM public.sgreg_sink WHERE id=1", []string{"v"}, []string{"TEXT"}, [][]any{{`[sgreg_a."Mixed Name"`}}),
		referenceExec(`UPDATE public.sgreg_sink SET v='<'::text||'sgreg_a."Mixed Name"'::regclass WHERE id=1`, 1),
		referencePrepared(referenceExec(`UPDATE public.sgreg_sink SET v='sgreg_a."Mixed Name"'::regclass||'>'::text WHERE id=2`, 1)),
		referenceSelect("SELECT v FROM public.sgreg_sink ORDER BY id", []string{"v"}, []string{"TEXT"}, [][]any{{`<sgreg_a."Mixed Name"`}, {`sgreg_a."Mixed Name">`}}),
		referenceExec(`DELETE FROM public.sgreg_sink WHERE v='<'::text||'sgreg_a."Mixed Name"'::regclass`, 1),
	} {
		step(s)
	}
	for prepared, name := range []string{"sgreg_alter_direct", "sgreg_alter_prepared"} {
		step(referenceExec("CREATE TABLE public."+name+"(v TEXT)", 0))
		step(referenceExec("INSERT INTO public."+name+" VALUES('!')", 1))
		action := referenceExec(`ALTER TABLE public.`+name+` ALTER COLUMN v TYPE TEXT USING v||'sgreg_a."Mixed Name"'::regclass`, 0)
		if prepared != 0 {
			action = referencePrepared(action)
		}
		step(action)
		step(referenceSelect("SELECT v FROM public."+name, []string{"v"}, []string{"TEXT"}, [][]any{{`!sgreg_a."Mixed Name"`}}))
	}
}

// PostgreSQL permits this DEFAULT; native deliberately retains its earlier
// REGCLASS-default exclusion rather than widening an adjacent capability.
func TestReferenceStringCoreRegclassDefaultOracle(t *testing.T) {
	p := newReferencePair(t)
	for _, s := range []referenceStep{
		referenceExec("CREATE TABLE regclass_default_source(v INT)", 0),
		referenceExec(`CREATE TABLE regclass_default_stored(v TEXT DEFAULT ('x'::text||'regclass_default_source'::regclass))`, 0),
		referenceExec("INSERT INTO regclass_default_stored DEFAULT VALUES", 1),
		referenceSelect("SELECT v FROM regclass_default_stored", []string{"v"}, []string{"TEXT"}, [][]any{{"xregclass_default_source"}}),
		referencePrepared(referenceExec("INSERT INTO regclass_default_stored DEFAULT VALUES", 1)),
		referenceSelect("SELECT v FROM regclass_default_stored", []string{"v"}, []string{"TEXT"}, [][]any{{"xregclass_default_source"}, {"xregclass_default_source"}}),
	} {
		referenceRunStep(t, p.ctx, p.postgres, p.postgresRecord, s)
	}
}
