package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceTransactionalViewsCreation(t *testing.T) {
	for _, commit := range []bool{false, true} {
		t.Run(map[bool]string{false: "rollback", true: "commit"}[commit], func(t *testing.T) {
			transactionalViewsRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c, peer *sql.Conn, mode string, held bool) {
				e, finish := transactionalViewsBegin(t, p, b, c, mode)
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, e, b.Recorder, s) }
				run(fixture.Exec("CREATE TABLE txv_original(id INT,txt TEXT)", 0))
				run(fixture.Exec("ALTER TABLE txv_original RENAME TO txv_source", 0))
				queries := []string{"CREATE VIEW txv_v AS SELECT id,txt FROM txv_source", "CREATE MATERIALIZED VIEW txv_m AS SELECT id,txt FROM txv_source WITH DATA"}
				executors := []fixture.Executor{e, e}
				if held {
					for i, q := range queries {
						stmt, err := c.PrepareContext(p.Ctx, q)
						if err != nil {
							t.Fatal(err)
						}
						defer stmt.Close()
						executors[i] = fixture.HeldStatement{Executor: e, Statement: stmt, SQL: q}
					}
				}
				// Preparation neither installs a definition nor executes its producer.
				fixture.QueryTableCreateAbsent(t, run, "txv_v")
				fixture.QueryTableCreateAbsent(t, run, "txv_m")
				run(fixture.Exec("INSERT INTO txv_source VALUES(1,'a'),(1,'a'),(2,NULL)", 3))
				fixture.RunStep(t, p.Ctx, executors[0], b.Recorder, fixture.Exec(queries[0], 0))
				fixture.RunStep(t, p.Ctx, executors[1], b.Recorder, fixture.Exec(queries[1], 3))
				run(transactionalViewsBag("txv_v", false))
				run(transactionalViewsBag("txv_m", false))
				run(fixture.Select("SELECT relname,relkind::TEXT AS kind FROM pg_class WHERE relnamespace='public'::regnamespace AND relname IN ('txv_v','txv_m') ORDER BY relname", []string{"relname", "kind"}, []string{"NAME", "TEXT"}, [][]any{{"txv_m", "m"}, {"txv_v", "v"}}))
				vIdentity := transactionalViewsIdentity(t, p, e, "txv_v")
				mIdentity := transactionalViewsIdentity(t, p, e, "txv_m")
				run(fixture.Select("SELECT EXISTS(SELECT 1 FROM pg_depend WHERE classid=2618 AND objid=$1 AND refobjid='txv_source'::regclass) AS linked", []string{"linked"}, []string{"BOOL"}, [][]any{{true}}, mIdentity[3]))
				if mode != "autocommit" {
					prun := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, peer, b.Recorder, s) }
					for _, name := range []string{"txv_source", "txv_v", "txv_m"} {
						fixture.QueryTableCreateAbsent(t, prun, name)
					}
					prun(fixture.Select("SELECT viewname FROM pg_views WHERE schemaname='public' AND viewname='txv_v'", []string{"viewname"}, []string{"NAME"}, nil))
					prun(fixture.Select("SELECT matviewname FROM pg_matviews WHERE schemaname='public' AND matviewname='txv_m'", []string{"matviewname"}, []string{"NAME"}, nil))
					prun(fixture.Select("SELECT count(*) FROM pg_rewrite WHERE oid IN ($1,$2)", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}, vIdentity[3], mIdentity[3]))
					prun(fixture.Select("SELECT count(*) FROM pg_type WHERE oid IN ($1,$2,$3,$4)", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}, vIdentity[1], vIdentity[2], mIdentity[1], mIdentity[2]))
					prun(fixture.Select("SELECT count(*) FROM pg_depend WHERE classid=2618 AND objid IN ($1,$2)", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}, vIdentity[3], mIdentity[3]))
					for _, name := range []string{"txv_v", "txv_m"} {
						s := fixture.ImplicitError("SELECT * FROM "+name, fixture.Diagnostic{Code: "42P01", Message: `relation "` + name + `" does not exist`, Position: "15"})
						s.SelectRows = true
						prun(s)
					}
				}
				run(fixture.Exec("INSERT INTO txv_source VALUES(3,'c')", 1))
				run(transactionalViewsBag("txv_v", true))
				run(transactionalViewsBag("txv_m", false))
				finish(commit)
				prun := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, peer, b.Recorder, s) }
				if commit || mode == "autocommit" {
					prun(transactionalViewsBag("txv_source", true))
					prun(transactionalViewsBag("txv_v", true))
					prun(transactionalViewsBag("txv_m", false))
				} else {
					for _, name := range []string{"txv_source", "txv_v", "txv_m"} {
						fixture.QueryTableCreateAbsent(t, prun, name)
					}
					// Fresh lifetime with identical spelling must not revive private data.
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE MATERIALIZED VIEW txv_m AS SELECT 9 AS id", 1))
					prun(fixture.Select("SELECT id FROM txv_m", []string{"id"}, []string{"INT4"}, [][]any{{int64(9)}}))
				}
			})
		})
	}
}
