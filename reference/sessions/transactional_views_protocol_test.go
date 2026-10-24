//go:build !noservertests

package reference_test

import (
	"database/sql"
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceTransactionalViewsProtocol(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		for _, held := range []bool{false, true} {
			t.Run(fmt.Sprintf("explicit=%v/held=%v", explicit, held), func(t *testing.T) {
				f := fixture.NewServerFixture(t)
				for _, b := range f.Backends() {
					t.Run(b.Name, func(t *testing.T) {
						w := serverAuthenticated(t, f.DSNFor(b))
						peer := serverAuthenticated(t, f.DSNFor(b))
						defer func() { serverWantCode(t, w.query(t, "ROLLBACK"), "") }()
						serverWantCode(t, w.query(t, "CREATE TABLE txv_source(id INT)"), "")
						serverWantCode(t, w.query(t, "INSERT INTO txv_source VALUES(1),(2)"), "")
						queries := []string{
							"CREATE VIEW txv_v AS SELECT id FROM txv_source",
							"CREATE MATERIALIZED VIEW txv_m AS SELECT id FROM txv_source",
							"CREATE OR REPLACE VIEW txv_v AS SELECT id+10 AS id FROM txv_source",
							"ALTER VIEW txv_v RENAME TO txv_renamed",
							"ALTER MATERIALIZED VIEW txv_m RENAME TO txv_stored",
							"DROP VIEW txv_renamed",
							"DROP MATERIALIZED VIEW txv_stored",
						}
						if held {
							for i, q := range queries {
								frames := serverCycle(t, w, serverParse(fmt.Sprint(i), q), serverDescribe('S', fmt.Sprint(i)))
								serverWantTags(t, frames, "1tnZ")
								serverWantReady(t, frames, 'I')
							}
							serverScalar(t, w, "SELECT count(*) FROM pg_class WHERE relnamespace='public'::regnamespace AND relname IN ('txv_v','txv_m')", "0")
						}
						if explicit {
							serverWantCode(t, w.query(t, "BEGIN"), "")
						}
						commandTags := []string{"CREATE VIEW", "SELECT 2", "CREATE VIEW", "ALTER VIEW", "ALTER MATERIALIZED VIEW", "DROP VIEW", "DROP MATERIALIZED VIEW"}
						for i, q := range queries {
							if held {
								frames := serverCycle(t, w, serverBind(fmt.Sprintf("portal%d", i), fmt.Sprint(i), nil, nil), serverExecute(fmt.Sprintf("portal%d", i), 0))
								serverWantTags(t, frames, "2CZ")
								ready := byte('I')
								if explicit {
									ready = 'T'
								}
								serverWantReady(t, frames, ready)
								transactionalViewsWireCommand(t, frames, commandTags[i])
							} else {
								frames := w.query(t, q)
								serverWantCode(t, frames, "")
								transactionalViewsWireCommand(t, frames, commandTags[i])
							}
							if i == 1 {
								serverScalar(t, w, "SELECT count(*) FROM txv_v", "2")
								serverScalar(t, w, "SELECT count(*) FROM txv_m", "2")
								if explicit {
									serverScalar(t, peer, "SELECT count(*) FROM pg_class WHERE relnamespace='public'::regnamespace AND relname IN ('txv_v','txv_m')", "0")
								}
							}
							if i == 2 {
								serverScalar(t, w, "SELECT sum(id) FROM txv_v", "23")
								serverScalar(t, w, "SELECT sum(id) FROM txv_m", "3")
							}
							if i == 4 {
								serverScalar(t, w, "SELECT sum(id) FROM txv_renamed", "23")
								serverScalar(t, w, "SELECT sum(id) FROM txv_stored", "3")
							}
						}
						if explicit {
							serverWantCode(t, w.query(t, "ROLLBACK"), "")
						}
						serverScalar(t, peer, "SELECT count(*) FROM pg_class WHERE relnamespace='public'::regnamespace AND relname IN ('txv_v','txv_m','txv_renamed','txv_stored')", "0")
						// Genuine Q messages retain one atomic implicit transaction.
						serverNotificationError(t, w.query(t, "INSERT INTO txv_source VALUES(3); CREATE VIEW txv_batch AS SELECT id FROM txv_source; SELECT 1/0"), "22012", "division by zero", "")
						serverScalar(t, w, "SELECT count(*) FROM txv_source", "2")
						serverScalar(t, peer, "SELECT count(*) FROM pg_class WHERE relnamespace='public'::regnamespace AND relname='txv_batch'", "0")
						serverWantCode(t, w.query(t, "INSERT INTO txv_source VALUES(3); CREATE VIEW txv_batch AS SELECT id FROM txv_source; CREATE MATERIALIZED VIEW txv_batch_m AS SELECT id FROM txv_batch"), "")
						serverScalar(t, peer, "SELECT count(*) FROM txv_batch_m", "3")
						serverWantCode(t, w.query(t, "BEGIN; SAVEPOINT scoped; ALTER VIEW txv_batch RENAME TO txv_rolled; DROP MATERIALIZED VIEW txv_batch_m; ROLLBACK TO scoped; RELEASE scoped; COMMIT"), "")
						serverScalar(t, peer, "SELECT count(*) FROM txv_batch", "3")
						serverScalar(t, peer, "SELECT count(*) FROM txv_batch_m", "3")
						serverWantTags(t, serverCycle(t, w, serverParse("reader", "SELECT id FROM txv_batch ORDER BY id"), serverDescribe('S', "reader")), "1tTZ")
						serverWantTags(t, serverCycle(t, w, serverParse("star", "SELECT * FROM txv_batch ORDER BY id"), serverDescribe('S', "star")), "1tTZ")
						serverWantCode(t, w.query(t, "BEGIN; CREATE OR REPLACE VIEW txv_batch AS SELECT id+10 AS id FROM txv_source"), "")
						readFrames := serverCycle(t, w, serverBind("fresh_read", "reader", nil, nil), serverExecute("fresh_read", 0))
						serverWantTags(t, readFrames, "2DDDCZ")
						serverWantValues(t, readFrames, [][]string{{"11"}, {"12"}, {"13"}})
						serverWantCode(t, w.query(t, "ROLLBACK"), "")
						readFrames = serverCycle(t, w, serverBind("rolled_read", "reader", nil, nil), serverExecute("rolled_read", 0))
						serverWantValues(t, readFrames, [][]string{{"1"}, {"2"}, {"3"}})
						serverWantCode(t, w.query(t, "BEGIN; CREATE OR REPLACE VIEW txv_batch AS SELECT id,true AS flag FROM txv_source; SAVEPOINT shape"), "")
						shapeFrames := serverCycle(t, w, serverBind("changed_shape", "star", nil, nil), serverExecute("changed_shape", 0))
						serverNotificationError(t, shapeFrames, "0A000", "cached plan must not change result type", "")
						serverWantReady(t, shapeFrames, 'E')
						serverWantCode(t, w.query(t, "ROLLBACK TO shape; ROLLBACK"), "")
						serverScalar(t, w, "SELECT count(*) FROM txv_batch", "3")
						// Also use real database/sql BeginTx through the server transport.
						c, err := b.DB.Conn(f.Ctx)
						if err != nil {
							t.Fatal(err)
						}
						defer c.Close()
						goQueries := []string{
							"CREATE VIEW txv_go AS SELECT id FROM txv_source",
							"CREATE MATERIALIZED VIEW txv_go_m AS SELECT id FROM txv_source",
							"CREATE OR REPLACE VIEW txv_go AS SELECT id+10 AS id FROM txv_source",
							"ALTER VIEW txv_go RENAME TO txv_go_v",
							"ALTER MATERIALIZED VIEW txv_go_m RENAME TO txv_go_stored",
							"DROP VIEW txv_go_v",
							"DROP MATERIALIZED VIEW txv_go_stored",
						}
						goStatements := make(map[string]*sql.Stmt)
						if held {
							for _, q := range goQueries {
								stmt, err := c.PrepareContext(f.Ctx, q)
								if err != nil {
									t.Fatal(err)
								}
								defer stmt.Close()
								goStatements[q] = stmt
							}
						}
						tx, err := c.BeginTx(f.Ctx, nil)
						if err != nil {
							t.Fatal(err)
						}
						defer tx.Rollback()
						for i, q := range goQueries {
							var e fixture.Executor = tx
							if held {
								e = fixture.HeldStatement{Executor: tx, Statement: goStatements[q], SQL: q}
							}
							count := int64(0)
							if i == 1 {
								count = 3
							}
							if i == 5 {
								fixture.RunStep(t, f.Ctx, tx, b.Recorder, fixture.Exec("SAVEPOINT go_drops", 0))
							}
							fixture.RunStep(t, f.Ctx, e, b.Recorder, fixture.Exec(q, count))
							if i == 4 {
								fixture.RunStep(t, f.Ctx, tx, b.Recorder, fixture.Select("SELECT id FROM txv_go_v ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(11)}, {int64(12)}, {int64(13)}}))
								fixture.RunStep(t, f.Ctx, tx, b.Recorder, fixture.Select("SELECT id FROM txv_go_stored ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}, {int64(3)}}))
								serverScalar(t, peer, "SELECT count(*) FROM pg_class WHERE relnamespace='public'::regnamespace AND relname IN ('txv_go_v','txv_go_stored')", "0")
							}
						}
						fixture.RunStep(t, f.Ctx, tx, b.Recorder, fixture.Exec("ROLLBACK TO go_drops", 0))
						fixture.RunStep(t, f.Ctx, tx, b.Recorder, fixture.Exec("RELEASE go_drops", 0))
						if err := tx.Commit(); err != nil {
							t.Fatal(err)
						}
						serverScalar(t, peer, "SELECT sum(id) FROM txv_go_v", "36")
						serverScalar(t, peer, "SELECT sum(id) FROM txv_go_stored", "6")
					})
				}
			})
		}
	}
}

func transactionalViewsWireCommand(t *testing.T, frames []serverFrame, want string) {
	t.Helper()
	n := 0
	for _, frame := range frames {
		if frame.tag == 'C' {
			n++
			if string(frame.body) != want+"\x00" {
				t.Fatalf("command tag %q want %q", frame.body, want)
			}
		}
	}
	if n != 1 {
		t.Fatalf("command completions %d want 1", n)
	}
}
