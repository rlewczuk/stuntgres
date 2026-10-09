package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"stuntgres"
	"testing"
	"time"
)

func TestReferenceStringCoreRegclassNamespaceOutputSourceConsumerRoutes(t *testing.T) {
	p := newReferenceScenario(t)
	t.Cleanup(func() {
		if p.postgres == nil {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if _, err := p.postgres.ExecContext(ctx, "DROP SCHEMA IF EXISTS sgreg_a CASCADE; DROP SCHEMA IF EXISTS sgreg_b CASCADE"); err != nil {
			t.Error(err)
		}
	})
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			db := backend.db
			db.SetMaxOpenConns(1)
			ctx := p.ctx
			c, err := db.Conn(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			exec := func(q string) {
				t.Helper()
				if _, err := c.ExecContext(ctx, q); err != nil {
					t.Fatal(q, err)
				}
			}
			check := func(q, want string) {
				t.Helper()
				for prepared := range 2 {
					var rows *sql.Rows
					var err error
					if prepared == 0 {
						rows, err = c.QueryContext(ctx, q)
					} else {
						s, e := c.PrepareContext(ctx, q)
						if e != nil {
							t.Fatal(q, e)
						}
						rows, err = s.QueryContext(ctx)
						stringCoreRetainedRows(t, rows, err, []string{"v"}, []string{"TEXT"}, [][]any{{want}}, false)
						s.Close()
						continue
					}
					stringCoreRetainedRows(t, rows, err, []string{"v"}, []string{"TEXT"}, [][]any{{want}}, false)
				}
			}
			for _, q := range []string{"CREATE SCHEMA sgreg_a", "CREATE SCHEMA sgreg_b", `CREATE TABLE sgreg_a."Mixed Name"(id INT)`, `CREATE TABLE sgreg_b."Mixed Name"(id INT)`, `CREATE TABLE public.sgreg_sink(id INT,v TEXT)`,
				`CREATE TABLE public.sgreg_store(id INT,v TEXT DEFAULT btrim(' stored '::char(10)),CHECK(v IS NORMALIZED AND ('['::text||'sgreg_a."Mixed Name"'::regclass) IS NORMALIZED))`,
				`CREATE VIEW public.sgreg_view AS SELECT btrim('['::text||'sgreg_a."Mixed Name"'::regclass) AS v`,
			} {
				exec(q)
			}
			forward, err := c.PrepareContext(ctx, `SELECT '['::text||'sgreg_a."Mixed Name"'::regclass AS v`)
			if err != nil {
				t.Fatal(err)
			}
			defer forward.Close()
			reverse, err := c.PrepareContext(ctx, `SELECT 'sgreg_a."Mixed Name"'::regclass||']'::text AS v`)
			if err != nil {
				t.Fatal(err)
			}
			defer reverse.Close()
			insert, err := c.PrepareContext(ctx, "INSERT INTO public.sgreg_store(id) VALUES($1)")
			if err != nil {
				t.Fatal(err)
			}
			defer insert.Close()
			for i, tc := range []struct{ path, spelling string }{{"sgreg_a, public", `"Mixed Name"`}, {"sgreg_b, public", `sgreg_a."Mixed Name"`}} {
				exec("SET search_path=" + tc.path)
				var path string
				if err := c.QueryRowContext(ctx, "SHOW search_path").Scan(&path); err != nil || path != tc.path {
					t.Fatal("search path fixture", path, tc.path, err)
				}
				check(`SELECT '['::text||'sgreg_a."Mixed Name"'::regclass AS v`, "["+tc.spelling)
				check(`SELECT 'sgreg_a."Mixed Name"'::regclass||']'::text AS v`, tc.spelling+"]")
				check("SELECT v FROM public.sgreg_view", "["+tc.spelling)
				rows, e := forward.QueryContext(ctx)
				stringCoreRetainedRows(t, rows, e, []string{"v"}, []string{"TEXT"}, [][]any{{"[" + tc.spelling}}, false)
				rows, e = reverse.QueryContext(ctx)
				stringCoreRetainedRows(t, rows, e, []string{"v"}, []string{"TEXT"}, [][]any{{tc.spelling + "]"}}, false)
				if _, err := insert.ExecContext(ctx, i+1); err != nil {
					t.Fatal("stored/default CHECK snapshot", err)
				}
			}
			check("SELECT 'x'::text||'4294967294'::regclass AS v", "x4294967294")
			check("SELECT '4294967294'::regclass||'x'::text AS v", "4294967294x")
			check("SELECT '['::text||'-'::regclass AS v", "[-")
			exec(`INSERT INTO public.sgreg_sink VALUES(1,'['::text||'sgreg_a."Mixed Name"'::regclass)`)
			check("SELECT v FROM public.sgreg_sink WHERE id=1", `[sgreg_a."Mixed Name"`)
			exec(`UPDATE public.sgreg_sink SET v='<'::text||'sgreg_a."Mixed Name"'::regclass WHERE id=1`)
			check("SELECT v FROM public.sgreg_sink WHERE id=1", `<sgreg_a."Mixed Name"`)
			for prepared, name := range []string{"sgreg_alter_direct", "sgreg_alter_prepared"} {
				exec("CREATE TABLE public." + name + "(v TEXT)")
				exec("INSERT INTO public." + name + " VALUES('!')")
				q := `ALTER TABLE public.` + name + ` ALTER COLUMN v TYPE TEXT USING v||'sgreg_a."Mixed Name"'::regclass`
				if prepared == 0 {
					exec(q)
				} else {
					s, e := c.PrepareContext(ctx, q)
					if e != nil {
						t.Fatal(e)
					}
					_, e = s.ExecContext(ctx)
					s.Close()
					if e != nil {
						t.Fatal(e)
					}
				}
				check("SELECT v FROM public."+name, `!sgreg_a."Mixed Name"`)
			}
			exec(`DELETE FROM public.sgreg_sink WHERE v='<'::text||'sgreg_a."Mixed Name"'::regclass`)
			forward.Close()
			reverse.Close()
			insert.Close()
			c.Close()
			stringCoreControlTuples(t, db, "SELECT id,v FROM public.sgreg_store ORDER BY id", [][]any{{int64(1), "stored"}, {int64(2), "stored"}})
			if !backend.pg {
				var dump bytes.Buffer
				if err := stuntgres.Dump(ctx, p.native, &dump); err != nil {
					t.Fatal(err)
				}
				if err := stuntgres.Reset(ctx, p.native); err != nil {
					t.Fatal(err)
				}
				if err := stuntgres.Restore(ctx, p.native, bytes.NewReader(dump.Bytes())); err != nil {
					t.Fatal(err)
				}
			}
			stringCoreControlExec(t, db, "SET search_path=sgreg_b, public")
			stringCoreControlExec(t, db, "INSERT INTO public.sgreg_store(id) VALUES(3)")
			stringCoreControlTuples(t, db, "SELECT v FROM public.sgreg_store WHERE id=3", [][]any{{"stored"}})
			stringCoreControlTuples(t, db, "SELECT v FROM public.sgreg_view", [][]any{{`[sgreg_a."Mixed Name"`}})
		})
	}
}
