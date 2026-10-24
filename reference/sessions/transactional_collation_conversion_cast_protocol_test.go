//go:build !noservertests

package reference_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

func TestReferenceTransactionalCollationConversionCastProtocol(t *testing.T) {
	for _, held := range []bool{false, true} {
		t.Run(fmt.Sprintf("held=%v", held), func(t *testing.T) {
			f := fixture.NewServerFixture(t)
			for _, b := range f.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					w, peer := serverAuthenticated(t, f.DSNFor(b)), serverAuthenticated(t, f.DSNFor(b))
					defer func() { serverWantCode(t, w.query(t, "ROLLBACK"), "") }()
					for _, q := range []string{"CREATE SCHEMA wire_hidden", "CREATE TYPE wire_src AS ENUM ('red')", "CREATE TYPE wire_dst AS ENUM ('red')", "SET search_path=public"} {
						serverWantCode(t, w.query(t, q), "")
					}
					serverWantCode(t, peer.query(t, "SET search_path=public"), "")
					// Repair is registered before touching the pinned builtin. It
					// uses a fresh bounded context even if an assertion aborts.
					t.Cleanup(func() {
						ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
						defer cancel()
						if _, err := b.DB.ExecContext(ctx, "ALTER CONVERSION wire_hidden.utf8_to_iso_8859_1 SET SCHEMA pg_catalog"); err != nil {
							t.Error("repair seed", err)
						}
						if _, err := b.DB.ExecContext(ctx, "DROP SCHEMA wire_hidden"); err != nil {
							t.Error("retire hidden schema", err)
						}
					})
					serverWantCode(t, w.query(t, "ALTER CONVERSION pg_catalog.utf8_to_iso_8859_1 SET SCHEMA wire_hidden"), "")
					cases := []struct{ create, alter, drop, count, use, value string }{
						{`CREATE COLLATION wire_coll FROM pg_catalog."C"`, "ALTER COLLATION wire_coll RENAME TO wire_coll_new", "DROP COLLATION wire_coll_new", "SELECT count(*) FROM pg_collation WHERE collname='wire_coll'", `SELECT upper('é' COLLATE wire_coll)`, "é"},
						{"CREATE DEFAULT CONVERSION wire_conv FOR 'UTF8' TO 'LATIN1' FROM pg_catalog.utf8_to_iso8859_1", "ALTER CONVERSION wire_conv RENAME TO wire_conv_new", "DROP CONVERSION wire_conv_new", "SELECT count(*) FROM pg_conversion WHERE conname='wire_conv'", "SELECT encode(convert_to('é','LATIN1'),'hex')", "e9"},
						{"CREATE CAST (wire_src AS wire_dst) WITH INOUT", "", "DROP CAST (wire_src AS wire_dst)", "SELECT count(*) FROM pg_cast WHERE castsource='wire_src'::regtype AND casttarget='wire_dst'::regtype", "SELECT ('red'::wire_src)::wire_dst::text", "red"},
					}
					serial := 0
					utility := func(q string, ready byte) {
						if !held {
							frames := w.query(t, q)
							serverWantCode(t, frames, "")
							serverWantReady(t, frames, ready)
							return
						}
						serial++
						name := fmt.Sprintf("definition_%d", serial)
						frames := serverCycle(t, w, serverParse(name, q), serverDescribe('S', name))
						serverWantTags(t, frames, "1tnZ")
						serverWantReady(t, frames, ready)
						// Named statements survive idle Sync; portals do not. Bind only
						// in the executing cycle for autocommit utilities.
						if ready == 'T' {
							frames = serverCycle(t, w, serverBind(name, name, nil, nil), serverDescribe('P', name))
							serverWantTags(t, frames, "2nZ")
							serverWantReady(t, frames, ready)
							frames = serverCycle(t, w, serverExecute(name, 0))
							serverWantTags(t, frames, "CZ")
						} else {
							frames = serverCycle(t, w, serverBind(name, name, nil, nil), serverDescribe('P', name), serverExecute(name, 0))
							serverWantTags(t, frames, "2nCZ")
						}
						serverWantReady(t, frames, ready)
						fields := strings.Fields(q)
						tag := strings.Join(fields[:2], " ")
						if fields[1] == "DEFAULT" {
							tag = "CREATE CONVERSION"
						}
						transactionalTypesCommandTag(t, frames, tag)
					}
					for _, tc := range cases {
						for _, explicit := range []bool{false, true} {
							for _, commit := range []bool{false, true} {
								ready := byte('I')
								if explicit {
									ready = 'T'
									serverWantCode(t, w.query(t, "BEGIN"), "")
								}
								serverScalar(t, w, tc.count, "0")
								if held {
									serial++
									name := fmt.Sprintf("quiet_%d", serial)
									frames := serverCycle(t, w, serverParse(name, tc.create), serverDescribe('S', name))
									serverWantTags(t, frames, "1tnZ")
									serverWantReady(t, frames, ready)
									serverScalar(t, w, tc.count, "0")
									if explicit {
										frames = serverCycle(t, w, serverBind(name, name, nil, nil), serverDescribe('P', name))
										serverWantTags(t, frames, "2nZ")
										serverWantReady(t, frames, ready)
										serverScalar(t, w, tc.count, "0")
										frames = serverCycle(t, w, serverExecute(name, 0))
										serverWantTags(t, frames, "CZ")
									} else {
										frames = serverCycle(t, w, serverBind(name, name, nil, nil), serverDescribe('P', name), serverExecute(name, 0))
										serverWantTags(t, frames, "2nCZ")
									}
									serverWantReady(t, frames, ready)
									fields := strings.Fields(tc.create)
									tag := strings.Join(fields[:2], " ")
									if fields[1] == "DEFAULT" {
										tag = "CREATE CONVERSION"
									}
									transactionalTypesCommandTag(t, frames, tag)
								} else {
									utility(tc.create, ready)
								}
								serverScalar(t, w, tc.use, tc.value)
								if explicit {
									serverScalar(t, peer, tc.count, "0")
									serverWantCode(t, w.query(t, "SAVEPOINT prior"), "")
								} else {
									serverScalar(t, peer, tc.count, "1")
								}
								if tc.alter != "" {
									utility(tc.alter, ready)
								}
								utility(tc.drop, ready)
								if explicit {
									serverWantCode(t, w.query(t, "ROLLBACK TO prior"), "")
									serverScalar(t, w, tc.use, tc.value)
									serverWantCode(t, w.query(t, "SAVEPOINT failure"), "")
									frames := w.query(t, tc.create)
									serverWantCode(t, frames, "42710")
									serverWantReady(t, frames, 'E')
									message := `collation "wire_coll" already exists`
									if strings.HasPrefix(tc.create, "CREATE DEFAULT") {
										message = `conversion "wire_conv" already exists`
									}
									if strings.HasPrefix(tc.create, "CREATE CAST") {
										message = "cast from type wire_src to type wire_dst already exists"
									}
									serverNotificationError(t, frames, "42710", message, "")
									serverNotificationError(t, w.query(t, "SELECT 1"), "25P02", "current transaction is aborted, commands ignored until end of transaction block", "")
									serverWantCode(t, w.query(t, "ROLLBACK TO failure"), "")
									serverWantCode(t, w.query(t, "RELEASE failure"), "")
									serverWantCode(t, w.query(t, "RELEASE prior"), "")
									end := "ROLLBACK"
									if commit {
										end = "COMMIT"
									}
									frames = w.query(t, end)
									serverWantCode(t, frames, "")
									serverWantReady(t, frames, 'I')
									if commit {
										serverScalar(t, peer, tc.count, "1")
										serverScalar(t, peer, tc.use, tc.value)
										if tc.alter != "" {
											utility(tc.alter, 'I')
										}
										utility(tc.drop, 'I')
									} else {
										serverScalar(t, peer, tc.count, "0")
									}
								}
							}
						}
						// Actual network database/sql BeginTx, not SQL BEGIN emulation.
						for _, commit := range []bool{false, true} {
							c, err := b.DB.Conn(f.Ctx)
							if err != nil {
								t.Fatal(err)
							}
							tx, err := c.BeginTx(f.Ctx, nil)
							if err != nil {
								c.Close()
								t.Fatal(err)
							}
							func() {
								defer tx.Rollback()
								defer c.Close()
								run := func(s fixture.Step) { fixture.RunStep(t, f.Ctx, tx, b.Recorder, s) }
								if held {
									stmt, err := tx.PrepareContext(f.Ctx, tc.create)
									if err != nil {
										t.Fatal(err)
									}
									defer stmt.Close()
									fixture.RunStep(t, f.Ctx, fixture.HeldStatement{Executor: tx, Statement: stmt, SQL: tc.create}, b.Recorder, fixture.Exec(tc.create, 0))
								} else {
									run(fixture.Exec(tc.create, 0))
								}
								run(fixture.Select(tc.use+" AS value", []string{"value"}, []string{"TEXT"}, [][]any{{tc.value}}))
								serverScalar(t, peer, tc.count, "0")
								if commit {
									err = tx.Commit()
								} else {
									err = tx.Rollback()
								}
								if err != nil {
									t.Fatal(err)
								}
							}()
							if commit {
								serverScalar(t, peer, tc.use, tc.value)
								if tc.alter != "" {
									utility(tc.alter, 'I')
								}
								utility(tc.drop, 'I')
							} else {
								serverScalar(t, peer, tc.count, "0")
							}
						}
					}
				})
			}
		})
	}
}
