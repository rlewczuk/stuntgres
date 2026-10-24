package reference_test

import (
	"database/sql"
	"reflect"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceTransactionalViewsDrop(t *testing.T) {
	for _, commit := range []bool{false, true} {
		t.Run(map[bool]string{false: "rollback", true: "commit"}[commit], func(t *testing.T) {
			transactionalViewsRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c, peer *sql.Conn, mode string, held bool) {
				base := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
				for _, s := range []fixture.Step{
					fixture.Exec("CREATE TABLE txv_source(id INT)", 0), fixture.Exec("INSERT INTO txv_source VALUES(7)", 1),
					fixture.Exec("CREATE TABLE txv_unrelated(id INT)", 0), fixture.Exec("INSERT INTO txv_unrelated VALUES(8)", 1),
					fixture.Exec("CREATE VIEW txv_v AS SELECT id FROM txv_source", 0),
					fixture.Exec("CREATE VIEW txv_w AS SELECT id FROM txv_v", 0),
					fixture.Exec("CREATE MATERIALIZED VIEW txv_m AS SELECT id FROM txv_v", 1),
					fixture.Exec("CREATE VIEW txv_z AS SELECT id FROM txv_m", 0),
					fixture.Exec("CREATE VIEW txv_standalone AS SELECT id FROM txv_source", 0),
					fixture.Exec("CREATE MATERIALIZED VIEW txv_stored AS SELECT id FROM txv_source", 1),
				} {
					base(s)
				}
				before := transactionalViewsIdentity(t, p, c, "txv_v")
				prepared := make(map[string]*sql.Stmt)
				if held {
					for _, q := range []string{"DROP VIEW txv_v RESTRICT", "DROP VIEW txv_standalone,txv_m", "DROP MATERIALIZED VIEW txv_stored,txv_absent", "DROP VIEW txv_v CASCADE", "DROP VIEW txv_standalone", "DROP MATERIALIZED VIEW txv_stored"} {
						stmt, err := c.PrepareContext(p.Ctx, q)
						if err != nil {
							t.Fatal(err)
						}
						defer stmt.Close()
						prepared[q] = stmt
					}
				}
				e, finish := transactionalViewsBegin(t, p, b, c, mode)
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, e, b.Recorder, s) }
				ddl := func(s fixture.Step) {
					var x fixture.Executor = e
					if held {
						x = fixture.HeldStatement{Executor: e, Statement: prepared[s.Query], SQL: s.Query}
					}
					fixture.RunStep(t, p.Ctx, x, b.Recorder, s)
				}
				if mode != "autocommit" {
					run(fixture.Exec("SAVEPOINT drops", 0))
				}
				want := fixture.Diagnostic{Code: "2BP01", Message: "cannot drop view txv_v because other objects depend on it", Detail: "view txv_w depends on view txv_v\nmaterialized view txv_m depends on view txv_v\nview txv_z depends on materialized view txv_m", Hint: "Use DROP ... CASCADE to drop the dependent objects too."}
				ddl(fixture.ImplicitError("DROP VIEW txv_v RESTRICT", want))
				if mode != "autocommit" {
					run(fixture.Exec("ROLLBACK TO drops", 0))
				}
				for _, name := range []string{"txv_v", "txv_w", "txv_m", "txv_z"} {
					run(fixture.Select("SELECT id FROM "+name, []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}))
				}
				// A later invalid target must not remove the earlier valid one.
				wrong := fixture.Diagnostic{Code: "42809", Message: `"txv_m" is not a view`, Hint: "Use DROP MATERIALIZED VIEW to remove a materialized view."}
				ddl(fixture.ImplicitError("DROP VIEW txv_standalone,txv_m", wrong))
				if mode != "autocommit" {
					run(fixture.Exec("ROLLBACK TO drops", 0))
				}
				run(transactionalViewsCount("txv_standalone", 1))
				ddl(fixture.ImplicitError("DROP MATERIALIZED VIEW txv_stored,txv_absent", fixture.Diagnostic{Code: "42P01", Message: `materialized view "txv_absent" does not exist`}))
				if mode != "autocommit" {
					run(fixture.Exec("ROLLBACK TO drops", 0))
				}
				run(transactionalViewsCount("txv_stored", 1))
				ddl(fixture.Exec("DROP VIEW txv_v CASCADE", 0))
				ddl(fixture.Exec("DROP VIEW txv_standalone", 0))
				ddl(fixture.Exec("DROP MATERIALIZED VIEW txv_stored", 0))
				for _, name := range []string{"txv_v", "txv_w", "txv_m", "txv_z", "txv_standalone", "txv_stored"} {
					fixture.QueryTableCreateAbsent(t, run, name)
				}
				run(fixture.Select("SELECT id FROM txv_source", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}))
				run(fixture.Select("SELECT id FROM txv_unrelated", []string{"id"}, []string{"INT4"}, [][]any{{int64(8)}}))
				if mode != "autocommit" {
					fixture.RunStep(t, p.Ctx, peer, b.Recorder, transactionalViewsCount("txv_v", 1))
				}
				finish(commit)
				if !commit && mode != "autocommit" {
					if got := transactionalViewsIdentity(t, p, c, "txv_v"); !reflect.DeepEqual(before, got) {
						t.Fatal(before, got)
					}
					for _, name := range []string{"txv_v", "txv_w", "txv_m", "txv_z", "txv_standalone", "txv_stored"} {
						base(fixture.Select("SELECT id FROM "+name, []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}))
					}
				} else {
					fixture.RunStep(t, p.Ctx, peer, b.Recorder, transactionalViewsCount("txv_v", 0))
				}
			})
		})
	}
}
