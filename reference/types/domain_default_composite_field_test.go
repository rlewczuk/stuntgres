package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceDomainDefaultCompositeFieldDependencies(t *testing.T) {
	for _, preparedDDL := range []bool{false, true} {
		route := "direct"
		if preparedDDL {
			route = "prepared"
		}
		t.Run(route, func(t *testing.T) {
			var dumpText string
			var exercise func(*testing.T, *fixture.Pair, fixture.Executor, *fixture.Recorder)
			sourceOK := t.Run("source", func(t *testing.T) {
			domainCompositeFieldScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fixture.Recorder, notices *[]fixture.Diagnostic) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
				for _, query := range []string{
					"CREATE TYPE domain_s.dd_pair AS(a integer,b integer)",
					"CREATE DOMAIN domain_s.dd AS integer DEFAULT (ROW(7,8)::domain_s.dd_pair).a",
					"CREATE DOMAIN domain_s.dd_child AS domain_s.dd",
					"CREATE DOMAIN domain_s.dd_b AS integer DEFAULT (ROW(17,18)::domain_s.dd_pair).b",
					"CREATE TABLE domain_s.dd_rows(id integer,own domain_s.dd,child domain_s.dd_child,keep integer)",
					"CREATE VIEW domain_s.dd_view AS SELECT id,own,child,keep FROM domain_s.dd_rows",
					"CREATE TABLE domain_s.dd_survivor(id integer,v domain_s.dd_b)",
					"CREATE TABLE domain_s.dd_sentinel(id integer,v integer)",
				} {
					run(fixture.Exec(query, 0))
				}
				run(fixture.Exec("INSERT INTO domain_s.dd_rows(id,keep) VALUES(1,101)", 1))
				run(fixture.Prepared(fixture.Exec("INSERT INTO domain_s.dd_rows VALUES(2,DEFAULT,DEFAULT,102)", 1)))
				run(fixture.Exec("INSERT INTO domain_s.dd_survivor(id) VALUES(1)", 1))
				run(fixture.Exec("INSERT INTO domain_s.dd_sentinel VALUES(1,99)", 1))
				checkBefore := func(extra bool) {
					for _, prepared := range []bool{false, true} {
						for _, empty := range []bool{false, true} {
							query := "SELECT id,own,child,keep FROM domain_s.dd_view"
							want := [][]any{{int64(1), int64(7), int64(7), int64(101)}, {int64(2), int64(7), int64(7), int64(102)}}
							if extra {
								want = append(want, []any{int64(3), int64(7), int64(7), int64(103)})
							}
							if empty {
								query += " WHERE false"
								want = nil
							}
							query += " ORDER BY id"
							step := fixture.Select(query, []string{"id", "own", "child", "keep"}, []string{"INT4", "INT4", "INT4", "INT4"}, want)
							step.Prepared = prepared
							step.CheckMetadata = func(t *testing.T, rows *sql.Rows) { domainReferenceCheckMetadata(t, rows, b.PG, false, step.Types) }
							run(step)
						}
						step := fixture.Select("SELECT id,v FROM domain_s.dd_survivor ORDER BY id", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(18)}})
						step.Prepared = prepared
						run(step)
					}
					run(fixture.Select("SELECT attname FROM pg_attribute WHERE attrelid='domain_s.dd_pair'::regclass::oid AND attnum>0 AND NOT attisdropped ORDER BY attnum", []string{"attname"}, []string{"NAME"}, [][]any{{"a"}, {"b"}}))
					run(fixture.Select("SELECT t.typname FROM pg_type t JOIN pg_namespace n ON n.oid=t.typnamespace WHERE n.nspname='domain_s' AND t.typtype='d' ORDER BY t.typname", []string{"typname"}, []string{"NAME"}, [][]any{{"dd"}, {"dd_b"}, {"dd_child"}}))
				}
				checkBefore(false)
				attributeDependencies := "SELECT t.typname,a.attname,d.deptype='n' AS normal FROM pg_depend d JOIN pg_type t ON t.oid=d.objid JOIN pg_namespace n ON n.oid=t.typnamespace JOIN pg_attribute a ON a.attrelid=d.refobjid AND a.attnum=d.refobjsubid WHERE d.classid='pg_type'::regclass::oid AND d.refclassid='pg_class'::regclass::oid AND d.objsubid=0 AND d.refobjid='domain_s.dd_pair'::regclass::oid AND n.nspname='domain_s' ORDER BY t.typname,a.attnum"
				defaultMetadata := "SELECT t.typname,t.typdefault FROM pg_type t JOIN pg_namespace n ON n.oid=t.typnamespace WHERE n.nspname='domain_s' AND t.typtype='d' ORDER BY t.typname"
				typeDependencies := "SELECT t.typname,provider.typname AS provider,d.deptype='n' AS normal FROM pg_depend d JOIN pg_type t ON t.oid=d.objid JOIN pg_namespace n ON n.oid=t.typnamespace JOIN pg_type provider ON provider.oid=d.refobjid JOIN pg_namespace pn ON pn.oid=provider.typnamespace WHERE d.classid='pg_type'::regclass::oid AND d.refclassid='pg_type'::regclass::oid AND d.objsubid=0 AND n.nspname='domain_s' AND pn.nspname='domain_s' AND t.typtype='d' ORDER BY t.typname,provider.typname,d.refobjsubid"
				// Pinned PostgreSQL18.6: own and copied recipes retain the actual
				// selected attribute edge, in addition to ordinary type edges.
				checkDependencies := func(after bool) {
					attributes := [][]any{{"dd", "a", true}, {"dd_b", "b", true}, {"dd_child", "a", true}}
					defaults := [][]any{{"dd", "(ROW(7, 8)::domain_s.dd_pair).a"}, {"dd_b", "(ROW(17, 18)::domain_s.dd_pair).b"}, {"dd_child", "(ROW(7, 8)::domain_s.dd_pair).a"}}
					types := [][]any{{"dd", "dd_pair", true}, {"dd_b", "dd_pair", true}, {"dd_child", "dd", true}, {"dd_child", "dd_pair", true}}
					if after {
						attributes = [][]any{{"dd_b", "b", true}}
						defaults = [][]any{{"dd_b", "(ROW(17, 18)::domain_s.dd_pair).b"}}
						types = [][]any{{"dd_b", "dd_pair", true}}
					}
					for _, prepared := range []bool{false, true} {
						for _, step := range []fixture.Step{
							fixture.Select(attributeDependencies, []string{"typname", "attname", "normal"}, []string{"NAME", "NAME", "BOOL"}, attributes),
							fixture.Select(defaultMetadata, []string{"typname", "typdefault"}, []string{"NAME", "TEXT"}, defaults),
							fixture.Select(typeDependencies, []string{"typname", "provider", "normal"}, []string{"NAME", "NAME", "BOOL"}, types),
						} {
							step.Prepared = prepared
							run(step)
						}
					}
				}
				checkDependencies(false)
				restrictSQL := "ALTER TYPE domain_s.dd_pair DROP ATTRIBUTE a RESTRICT"
				*notices = nil
				d := fixture.ControlsReferenceDiagnostic("2BP01", "cannot drop column a of composite type domain_s.dd_pair because other objects depend on it")
				d.Detail = "type domain_s.dd depends on column a of composite type domain_s.dd_pair\ncolumn own of table domain_s.dd_rows depends on type domain_s.dd\ntype domain_s.dd_child depends on column a of composite type domain_s.dd_pair\ncolumn child of table domain_s.dd_rows depends on type domain_s.dd_child\nview domain_s.dd_view depends on column child of table domain_s.dd_rows"
				d.Hint = "Use DROP ... CASCADE to drop the dependent objects too."
				run(domainDefaultFailure(restrictSQL, d, preparedDDL, false))
				if len(*notices) != 0 {
					t.Errorf("RESTRICT emitted notices=%#v", *notices)
				}
				checkBefore(false)
				run(fixture.Prepared(fixture.Exec("INSERT INTO domain_s.dd_rows(id,keep) VALUES(3,103)", 1)))
				checkBefore(true)
				checkDependencies(false)
				*notices = nil
				cascade := fixture.Exec("ALTER TYPE domain_s.dd_pair DROP ATTRIBUTE a CASCADE", 0)
				cascade.Prepared = preparedDDL
				run(cascade)
				fixture.DomainReferenceNotice(t, notices, "drop cascades to 5 other objects", "drop cascades to type domain_s.dd\ndrop cascades to column own of table domain_s.dd_rows\ndrop cascades to type domain_s.dd_child\ndrop cascades to column child of table domain_s.dd_rows\ndrop cascades to view domain_s.dd_view")
				checkDependencies(true)
				run(fixture.Select("SELECT c.relname,c.relkind FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='domain_s' ORDER BY c.relname", []string{"relname", "relkind"}, []string{"NAME", "CHAR"}, [][]any{{"dd_pair", "c"}, {"dd_rows", "r"}, {"dd_sentinel", "r"}, {"dd_survivor", "r"}}))
				run(fixture.Select("SELECT attname FROM pg_attribute WHERE attrelid='domain_s.dd_rows'::regclass::oid AND attnum>0 AND NOT attisdropped ORDER BY attnum", []string{"attname"}, []string{"NAME"}, [][]any{{"id"}, {"keep"}}))
				d = fixture.ControlsReferenceDiagnostic("42703", `column "own" does not exist`)
				d.Position = "11"
				run(domainDefaultFailure("SELECT id,own,child,keep FROM domain_s.dd_rows ORDER BY id", d, false, true))
				run(fixture.Select("SELECT id,keep FROM domain_s.dd_rows ORDER BY id", []string{"id", "keep"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(101)}, {int64(2), int64(102)}, {int64(3), int64(103)}}))
				d = fixture.ControlsReferenceDiagnostic("42P01", `relation "domain_s.dd_view" does not exist`)
				d.Position = "31"
				run(domainDefaultFailure("SELECT id,own,child,keep FROM domain_s.dd_view ORDER BY id", d, false, true))
				for _, name := range []string{"dd", "dd_child"} {
					d = fixture.ControlsReferenceDiagnostic("42704", `type "domain_s.`+name+`" does not exist`)
					d.Position = "14"
					run(domainDefaultFailure("SELECT NULL::domain_s."+name+" AS v", d, false, true))
				}
				run(fixture.Select("SELECT attname FROM pg_attribute WHERE attrelid='domain_s.dd_pair'::regclass::oid AND attnum>0 AND NOT attisdropped ORDER BY attnum", []string{"attname"}, []string{"NAME"}, [][]any{{"b"}}))
				// The unrelated b-field recipe remains executable; the table's
				// surviving attributes and old data remain independently usable.
				run(fixture.Prepared(fixture.Exec("INSERT INTO domain_s.dd_rows(id,keep) VALUES(4,104)", 1)))
				for _, prepared := range []bool{false, true} {
					step := fixture.Select("SELECT id,keep FROM domain_s.dd_rows ORDER BY id", []string{"id", "keep"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(101)}, {int64(2), int64(102)}, {int64(3), int64(103)}, {int64(4), int64(104)}})
					step.Prepared = prepared
					step.CheckMetadata = func(t *testing.T, rows *sql.Rows) { domainReferenceCheckMetadata(t, rows, b.PG, false, step.Types) }
					run(step)
				}
				run(fixture.Prepared(fixture.Exec("INSERT INTO domain_s.dd_survivor(id) VALUES(2)", 1)))
				for _, prepared := range []bool{false, true} {
					step := fixture.Select("SELECT id,v FROM domain_s.dd_survivor ORDER BY id", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(18)}, {int64(2), int64(18)}})
					step.Prepared = prepared
					run(step)
				}
				run(fixture.Exec("INSERT INTO domain_s.dd_sentinel VALUES(2,100)", 1))
				run(fixture.Select("SELECT id,v FROM domain_s.dd_sentinel ORDER BY id", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(99)}, {int64(2), int64(100)}}))
				// Native-only logical replay: public SQL above is shared with PG.
				// Source metadata retains the historical written constructor;
				// restored metadata describes executable current-shape SQL.
				if b.PG {
					return
				}
				assertSurvivors := func(t *testing.T, pair *fixture.Pair, e fixture.Executor, rec *fixture.Recorder, restored, fresh bool) {
					wantDefault := "(ROW(17, 18)::domain_s.dd_pair).b"
					if restored {
						wantDefault = "(ROW(18)::domain_s.dd_pair).b"
					}
					wantRows := [][]any{{int64(1), int64(18)}, {int64(2), int64(18)}}
					if fresh {
						wantRows = append(wantRows, []any{int64(3), int64(18)})
					}
					wantTableRows := [][]any{{int64(1), int64(101)}, {int64(2), int64(102)}, {int64(3), int64(103)}, {int64(4), int64(104)}}
					if fresh {
						wantTableRows = append(wantTableRows, []any{int64(5), int64(105)})
					}
					for _, prepared := range []bool{false, true} {
						for _, step := range []fixture.Step{
							fixture.Select(defaultMetadata, []string{"typname", "typdefault"}, []string{"NAME", "TEXT"}, [][]any{{"dd_b", wantDefault}}),
							fixture.Select(attributeDependencies, []string{"typname", "attname", "normal"}, []string{"NAME", "NAME", "BOOL"}, [][]any{{"dd_b", "b", true}}),
							fixture.Select(typeDependencies, []string{"typname", "provider", "normal"}, []string{"NAME", "NAME", "BOOL"}, [][]any{{"dd_b", "dd_pair", true}}),
							fixture.Select("SELECT id,keep FROM domain_s.dd_rows ORDER BY id", []string{"id", "keep"}, []string{"INT4", "INT4"}, wantTableRows),
							fixture.Select("SELECT id,v FROM domain_s.dd_sentinel ORDER BY id", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(99)}, {int64(2), int64(100)}}),
							fixture.Select("SELECT c.relname,a.attname FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace JOIN pg_attribute a ON a.attrelid=c.oid WHERE n.nspname='domain_s' AND c.relname IN ('dd_pair','dd_rows') AND a.attnum>0 AND NOT a.attisdropped ORDER BY c.relname,a.attnum", []string{"relname", "attname"}, []string{"NAME", "NAME"}, [][]any{{"dd_pair", "b"}, {"dd_rows", "id"}, {"dd_rows", "keep"}}),
						} {
							step.Prepared = prepared
							fixture.RunStep(t, pair.Ctx, e, rec, step)
						}
						for _, empty := range []bool{false, true} {
							query := "SELECT id,v FROM domain_s.dd_survivor"
							rows := wantRows
							if empty {
								query += " WHERE false"
								rows = nil
							}
							query += " ORDER BY id"
							step := fixture.Select(query, []string{"id", "v"}, []string{"INT4", "INT4"}, rows)
							step.Prepared = prepared
							step.CheckMetadata = func(t *testing.T, rows *sql.Rows) { domainReferenceCheckMetadata(t, rows, false, false, step.Types) }
							fixture.RunStep(t, pair.Ctx, e, rec, step)
						}
					}
					missing := fixture.ControlsReferenceDiagnostic("42704", `type "domain_s.dd_child" does not exist`)
					missing.Position = "14"
					fixture.RunStep(t, pair.Ctx, e, rec, domainDefaultFailure("SELECT NULL::domain_s.dd_child AS v", missing, false, true))
					missing = fixture.ControlsReferenceDiagnostic("42P01", `relation "domain_s.dd_view" does not exist`)
					missing.Position = "31"
					fixture.RunStep(t, pair.Ctx, e, rec, domainDefaultFailure("SELECT id,own,child,keep FROM domain_s.dd_view ORDER BY id", missing, false, true))
				}
				assertSurvivors(t, p, c, r, false, false)
				var dump bytes.Buffer
				if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
					t.Fatal(err)
				}
				dumpText = dump.String()
				assertSurvivors(t, p, c, r, false, false)
				if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
					t.Fatal(err)
				}
				if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump.String())); err != nil {
					t.Fatal(err)
				}
				exercise = func(t *testing.T, pair *fixture.Pair, e fixture.Executor, rec *fixture.Recorder) {
					assertSurvivors(t, pair, e, rec, true, false)
					fixture.RunStep(t, pair.Ctx, e, rec, fixture.Prepared(fixture.Exec("INSERT INTO domain_s.dd_rows(id,keep) VALUES(5,105)", 1)))
					fixture.RunStep(t, pair.Ctx, e, rec, fixture.Prepared(fixture.Exec("INSERT INTO domain_s.dd_survivor(id) VALUES(3)", 1)))
					assertSurvivors(t, pair, e, rec, true, true)
				}
				exercise(t, p, c, r)
			})
			})
			if !sourceOK {
				return
			}
			t.Run("fresh-target", func(t *testing.T) {
				if dumpText == "" || exercise == nil {
					t.Fatal("source produced no native replay payload")
				}
				target := fixture.NewFixture(t, false, true)
				if err := stuntgres.Restore(target.Ctx, target.Native, strings.NewReader(dumpText)); err != nil {
					t.Fatal(err)
				}
				exercise(t, target, target.Memory, target.MemoryRecord)
			})
		})
	}
}

// This selected scenario owns cleanup independently of its assertion budget.
// Register teardown before notice-pool handles, which must close first.
func domainCompositeFieldScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn, *fixture.Recorder, *[]fixture.Diagnostic)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			t.Cleanup(func() {
				ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
				defer cancel()
				for _, schema := range []string{"domain_external", "domain_other", "domain_s"} {
					if _, err := b.DB.ExecContext(ctx, "DROP SCHEMA IF EXISTS "+schema+" CASCADE"); err != nil {
						t.Error(err)
					}
				}
				if _, err := b.DB.ExecContext(ctx, "DROP TABLE IF EXISTS public.one"); err != nil {
					t.Error(err)
				}
			})
			var notices []fixture.Diagnostic
			_, conn, recorder := fixture.NoticeReferencePool(t, p, b.PG, &notices)
			fixture.RunStep(t, p.Ctx, conn, recorder, fixture.Exec("CREATE SCHEMA domain_s", 0))
			fixture.RunStep(t, p.Ctx, conn, recorder, fixture.Exec("CREATE TABLE public.one (v integer)", 0))
			fixture.RunStep(t, p.Ctx, conn, recorder, fixture.Exec("INSERT INTO public.one VALUES (1)", 1))
			body(t, p, b, conn, recorder, &notices)
		})
	}
}
