package reference_test

import (
	"bytes"
	"database/sql"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceQueryTableOIDPersistence(t *testing.T) {
	routineScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		routineExec(t, p, b, c,
			"CREATE TABLE toi_persist(a INT)",
			"CREATE TABLE toi_persist_child() INHERITS(toi_persist)",
			"CREATE TABLE toi_persist_inner(a INT)")
		for _, s := range []fixture.Step{fixture.Exec("INSERT INTO toi_persist VALUES(1)", 1), fixture.Exec("INSERT INTO toi_persist_child VALUES(2)", 1), fixture.Exec("INSERT INTO toi_persist_inner VALUES(7)", 1)} {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		}
		routineExec(t, p, b, c,
			"CREATE FUNCTION toi_persist_hard() RETURNS oid LANGUAGE SQL BEGIN ATOMIC SELECT q.tableoid FROM toi_persist_child q; END",
			"CREATE FUNCTION toi_persist_soft() RETURNS oid LANGUAGE SQL AS $$SELECT q.tableoid FROM toi_persist_child q$$",
			"CREATE FUNCTION toi_persist_join() RETURNS oid LANGUAGE SQL BEGIN ATOMIC SELECT y.tableoid FROM toi_persist x JOIN toi_persist y ON x.a=1 AND y.a=2; END",
			"CREATE FUNCTION toi_persist_correlated() RETURNS oid LANGUAGE SQL BEGIN ATOMIC SELECT (SELECT q.tableoid FROM toi_persist_inner i WHERE i.a=7) FROM toi_persist q WHERE q.a=2; END",
			"CREATE VIEW toi_persist_view AS SELECT q.a,q.tableoid AS physical FROM toi_persist q",
			"ALTER TABLE toi_persist_child RENAME TO toi_persist_saved",
			"CREATE TABLE toi_persist_child(a INT)")
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO toi_persist_child VALUES(9)", 1))
		check := func(conn *sql.Conn, backend fixture.Backend) {
			t.Helper()
			parent := tableoidCell(backend, tableoidIdentity(t, p, conn, "toi_persist"))
			child := tableoidCell(backend, tableoidIdentity(t, p, conn, "toi_persist_saved"))
			shadow := tableoidCell(backend, tableoidIdentity(t, p, conn, "toi_persist_child"))
			for _, name := range []string{"toi_persist_hard", "toi_persist_join", "toi_persist_correlated"} {
				tableoidRoutes(t, p, backend, conn, fixture.Select("SELECT "+name+"() AS physical", []string{"physical"}, []string{"OID"}, [][]any{{child}}))
			}
			tableoidRoutes(t, p, backend, conn, fixture.Select("SELECT toi_persist_soft() AS physical", []string{"physical"}, []string{"OID"}, [][]any{{shadow}}))
			tableoidRoutes(t, p, backend, conn, fixture.Select("SELECT * FROM toi_persist_view ORDER BY a", []string{"a", "physical"}, []string{"INT4", "OID"}, [][]any{{int64(1), parent}, {int64(2), child}}))
			tableoidRoutes(t, p, backend, conn, fixture.Select("SELECT a,tableoid FROM ONLY toi_persist", []string{"a", "tableoid"}, []string{"INT4", "OID"}, [][]any{{int64(1), parent}}))
			tableoidDependencies(t, p, backend, conn, "toi_persist_hard", "toi_persist_saved", true)
			tableoidDependencies(t, p, backend, conn, "toi_persist_soft", "toi_persist_child", false)
			query := "SELECT q.a,q.tableoid FROM toi_persist q ORDER BY q.a"
			stmt, err := conn.PrepareContext(p.Ctx, query)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := fixture.HeldStatement{Executor: conn, Statement: stmt, SQL: query}
			for range 2 {
				fixture.RunStep(t, p.Ctx, held, backend.Recorder, fixture.Select(query, []string{"a", "tableoid"}, []string{"INT4", "OID"}, [][]any{{int64(1), parent}, {int64(2), child}}))
			}
		}
		check(c, b)
		if b.PG {
			return
		}
		// Native controls have no PG API equivalent. Every round reacquires
		// identities and replays the unchanged native output on the real oracle.
		for round := 1; round <= 2; round++ {
			var dump bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(dump.String(), "BEGIN ATOMIC") {
				t.Fatal("standard body form lost from emitted SQL")
			}
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
			check(c, b)
			if p.Postgres != nil {
				pg, err := p.Postgres.Conn(p.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				func() {
					defer pg.Close()
					if _, err := pg.ExecContext(p.Ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
						t.Fatal(err)
					}
					if _, err := pg.ExecContext(p.Ctx, dump.String()); err != nil {
						t.Fatalf("round %d unchanged native SQL replay: %v", round, err)
					}
					check(pg, fixture.Backend{PG: true, Recorder: p.PostgresRecord})
					if _, err := pg.ExecContext(p.Ctx, "SET check_function_bodies=true; DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
						t.Fatal(err)
					}
				}()
			}
		}
	})
}
