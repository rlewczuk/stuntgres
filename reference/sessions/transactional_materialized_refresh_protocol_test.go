//go:build !noservertests

package reference_test

import (
	"database/sql"
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceTransactionalMaterializedRefreshProtocol(t *testing.T) {
	for _, concurrent := range []bool{false, true} {
		for _, explicit := range []bool{false, true} {
			for _, held := range []bool{false, true} {
				t.Run(fmt.Sprintf("concurrent=%v/explicit=%v/held=%v", concurrent, explicit, held), func(t *testing.T) {
					f := fixture.NewServerFixture(t)
					for _, b := range f.Backends() {
						t.Run(b.Name, func(t *testing.T) {
							w := serverAuthenticated(t, f.DSNFor(b))
							peer := serverAuthenticated(t, f.DSNFor(b))
							defer func() { serverWantCode(t, w.query(t, "ROLLBACK"), "") }()
							serverWantCode(t, w.query(t, "CREATE TABLE txr_source(i INT)"), "")
							serverWantCode(t, w.query(t, "INSERT INTO txr_source VALUES(1)"), "")
							serverWantCode(t, w.query(t, "CREATE MATERIALIZED VIEW txr_mv AS SELECT i FROM txr_source"), "")
							serverWantCode(t, w.query(t, "CREATE UNIQUE INDEX txr_u ON txr_mv(i)"), "")
							q := "REFRESH MATERIALIZED VIEW txr_mv"
							if concurrent {
								q = "REFRESH MATERIALIZED VIEW CONCURRENTLY txr_mv"
							}
							if held {
								frames := serverCycle(t, w, serverParse("refresh", q), serverDescribe('S', "refresh"))
								serverWantTags(t, frames, "1tnZ")
								serverWantReady(t, frames, 'I')
								for _, frame := range frames {
									if frame.tag == 't' && (len(frame.body) != 2 || frame.body[0] != 0 || frame.body[1] != 0) {
										t.Fatal("nonempty utility signature", frame.body)
									}
								}
							}
							serverScalar(t, w, "SELECT sum(i) FROM txr_mv", "1")
							execute := func(portal string) {
								var frames []serverFrame
								if held {
									frames = serverCycle(t, w, serverBind(portal, "refresh", nil, nil), serverExecute(portal, 0))
									serverWantTags(t, frames, "2CZ")
								} else {
									frames = w.query(t, q)
									serverWantCode(t, frames, "")
								}
								transactionalViewsWireCommand(t, frames, "REFRESH MATERIALIZED VIEW")
								ready := byte('I')
								if explicit {
									ready = 'T'
								}
								serverWantReady(t, frames, ready)
							}
							for round, commit := range []bool{false, true} {
								if explicit {
									serverWantReady(t, w.query(t, "BEGIN"), 'T')
								}
								serverWantCode(t, w.query(t, "INSERT INTO txr_source VALUES(2)"), "")
								execute(fmt.Sprintf("refresh%d", round))
								serverScalar(t, w, "SELECT sum(i) FROM txr_mv", "3")
								if explicit {
									serverScalar(t, peer, "SELECT sum(i) FROM txr_source", "1")
									if concurrent {
										serverScalar(t, peer, "SELECT sum(i) FROM txr_mv", "1")
									}
									end := "ROLLBACK"
									if commit {
										end = "COMMIT"
									}
									serverWantReady(t, w.query(t, end), 'I')
								}
								want := "1"
								if commit || !explicit {
									want = "3"
								}
								serverScalar(t, peer, "SELECT sum(i) FROM txr_mv", want)
								serverScalar(t, peer, "SELECT sum(i) FROM txr_source", want)
								serverScalar(t, w, "SELECT 42", "42")
								if !explicit && !commit {
									serverWantCode(t, w.query(t, "DELETE FROM txr_source WHERE i=2; "+q), "")
								}
							}
							serverNotificationError(t, w.query(t, "INSERT INTO txr_source VALUES(3); "+q+"; SELECT i FROM txr_mv; SELECT 1/0"), "22012", "division by zero", "")
							serverScalar(t, w, "SELECT sum(i) FROM txr_source", "3")
							serverScalar(t, w, "SELECT sum(i) FROM txr_mv", "3")
							serverWantReady(t, w.query(t, "SELECT 42"), 'I')
							serverWantCode(t, w.query(t, "INSERT INTO txr_source VALUES(3); "+q+"; SELECT i FROM txr_mv ORDER BY i"), "")
							serverScalar(t, peer, "SELECT sum(i) FROM txr_mv", "6")
							serverWantCode(t, w.query(t, "BEGIN; SAVEPOINT kept; REFRESH MATERIALIZED VIEW txr_mv WITH NO DATA"), "")
							unpopulated := w.query(t, "SELECT i FROM txr_mv")
							serverWantCode(t, unpopulated, "55000")
							errors := 0
							for _, frame := range unpopulated {
								if frame.tag == 'E' {
									errors++
									fields := serverFields(frame.body)
									for key, want := range map[byte]string{'S': "ERROR", 'V': "ERROR", 'C': "55000", 'M': `materialized view "txr_mv" has not been populated`, 'H': "Use the REFRESH MATERIALIZED VIEW command.", 'P': "", 'D': "", 'W': "", 'p': "", 'q': "", 's': "", 't': "", 'c': "", 'd': "", 'n': ""} {
										if fields[key] != want {
											t.Fatalf("diagnostic %c=%q want %q", key, fields[key], want)
										}
									}
								}
							}
							if errors != 1 {
								t.Fatal("unpopulated error count", errors)
							}
							serverWantReady(t, w.query(t, "SELECT 1"), 'E')
							serverWantCode(t, w.query(t, "ROLLBACK TO kept; RELEASE kept; COMMIT"), "")
							serverScalar(t, w, "SELECT sum(i) FROM txr_mv", "6")
							// BeginTx uses the actual network transport and exactly the Conn-owned
							// prepared handle. Tx.Stmt would not prove a held utility Execute.
							c, err := b.DB.Conn(f.Ctx)
							if err != nil {
								t.Fatal(err)
							}
							defer c.Close()
							var stmt *sql.Stmt
							if held {
								stmt, err = c.PrepareContext(f.Ctx, q)
								if err != nil {
									t.Fatal(err)
								}
								defer stmt.Close()
							}
							for _, commit := range []bool{false, true} {
								tx, err := c.BeginTx(f.Ctx, nil)
								if err != nil {
									t.Fatal(err)
								}
								defer tx.Rollback()
								run := func(s fixture.Step) { fixture.RunStep(t, f.Ctx, tx, b.Recorder, s) }
								run(fixture.Exec("INSERT INTO txr_source VALUES(4)", 1))
								var e fixture.Executor = tx
								if held {
									e = fixture.HeldStatement{Executor: tx, Statement: stmt, SQL: q}
								}
								fixture.RunStep(t, f.Ctx, e, b.Recorder, fixture.Exec(q, 0))
								run(fixture.Select("SELECT i FROM txr_mv ORDER BY i", []string{"i"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}, {int64(3)}, {int64(4)}}))
								if concurrent {
									serverScalar(t, peer, "SELECT sum(i) FROM txr_mv", "6")
								}
								if commit {
									err = tx.Commit()
								} else {
									err = tx.Rollback()
								}
								if err != nil {
									t.Fatal(err)
								}
								want := "6"
								if commit {
									want = "10"
								}
								serverScalar(t, peer, "SELECT sum(i) FROM txr_mv", want)
								fixture.RunStep(t, f.Ctx, c, b.Recorder, fixture.Select("SELECT 42 AS n", []string{"n"}, []string{"INT4"}, [][]any{{int64(42)}}))
							}
						})
					}
				})
			}
		}
	}
}
