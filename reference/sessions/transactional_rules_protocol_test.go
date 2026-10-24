//go:build !noservertests

package reference_test

import (
	"database/sql"
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceTransactionalRulesProtocol(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		for _, held := range []bool{false, true} {
			t.Run(fmt.Sprintf("explicit=%v/held=%v", explicit, held), func(t *testing.T) {
				f := fixture.NewServerFixture(t)
				for _, b := range f.Backends() {
					t.Run(b.Name, func(t *testing.T) {
						w := serverAuthenticated(t, f.DSNFor(b))
						peer := serverAuthenticated(t, f.DSNFor(b))
						defer func() { serverWantCode(t, w.query(t, "ROLLBACK"), "") }()
						for _, q := range []string{"CREATE TABLE src(id INT,v INT)", "CREATE TABLE sink1(id INT,v INT)", "CREATE TABLE sink2(id INT,v INT)"} {
							serverWantCode(t, w.query(t, q), "")
						}
						queries := []string{
							"CREATE RULE r AS ON INSERT TO src DO ALSO INSERT INTO sink1 VALUES(NEW.id,abs(NEW.v))",
							"CREATE OR REPLACE RULE r AS ON INSERT TO src DO ALSO INSERT INTO sink2 VALUES(NEW.id,NEW.v+1)",
							"ALTER RULE r ON src RENAME TO renamed",
							"DROP RULE renamed ON src",
						}
						if held {
							for i, q := range queries {
								frames := serverCycle(t, w, serverParse(fmt.Sprint(i), q), serverDescribe('S', fmt.Sprint(i)))
								serverWantTags(t, frames, "1tnZ")
								serverWantReady(t, frames, 'I')
							}
							serverScalar(t, w, "SELECT count(*) FROM pg_rewrite WHERE ev_class='src'::regclass", "0")
						}
						// One named DML statement remains held through the entire lifecycle.
						serverWantTags(t, serverCycle(t, w, serverParse("dml", "INSERT INTO src VALUES(99,-10)"), serverDescribe('S', "dml")), "1tnZ")
						if explicit {
							serverWantCode(t, w.query(t, "BEGIN"), "")
						}
						for i, q := range queries {
							var frames []serverFrame
							if held {
								name := fmt.Sprintf("p%d", i)
								frames = serverCycle(t, w, serverBind(name, fmt.Sprint(i), nil, nil), serverExecute(name, 0))
								serverWantTags(t, frames, "2CZ")
							} else {
								frames = w.query(t, q)
								serverWantCode(t, frames, "")
							}
							transactionalViewsWireCommand(t, frames, []string{"CREATE RULE", "CREATE RULE", "ALTER RULE", "DROP RULE"}[i])
							ready := byte('I')
							if explicit {
								ready = 'T'
							}
							serverWantReady(t, frames, ready)
							// Same SQL handle executes at each transition, with an independently
							// specified aggregate that detects stale create/replace/drop dispatch.
							frames = serverCycle(t, w, serverBind(fmt.Sprintf("d%d", i), "dml", nil, nil), serverExecute(fmt.Sprintf("d%d", i), 0))
							serverWantTags(t, frames, "2CZ")
							transactionalViewsWireCommand(t, frames, "INSERT 0 1")
							serverScalar(t, w, "SELECT count(*) FROM src", fmt.Sprint(i+1))
							serverScalar(t, w, "SELECT count(*) FROM sink1", "1")
							want := 0
							if i >= 1 {
								want = 1
							}
							if i >= 2 {
								want = 2
							}
							serverScalar(t, w, "SELECT count(*) FROM sink2", fmt.Sprint(want))
							if i == 0 {
								serverScalar(t, w, "SELECT v FROM sink1", "10")
							}
							if i == 1 {
								serverScalar(t, w, "SELECT v FROM sink2", "-9")
							}
							if explicit {
								serverScalar(t, peer, "SELECT count(*) FROM pg_rewrite WHERE ev_class='src'::regclass", "0")
							}
						}
						if explicit {
							serverWantCode(t, w.query(t, "ROLLBACK"), "")
							serverScalar(t, w, "SELECT count(*) FROM src", "0")
							serverScalar(t, w, "SELECT count(*) FROM sink1", "0")
							serverScalar(t, w, "SELECT count(*) FROM sink2", "0")
						} else {
							serverWantCode(t, w.query(t, "DELETE FROM src; DELETE FROM sink1; DELETE FROM sink2"), "")
						}
						// A single Q owns every prefix, including catalog changes and action rows.
						frames := w.query(t, "INSERT INTO src VALUES(1,-10); "+queries[0]+"; INSERT INTO src VALUES(2,-20); SELECT 1/0")
						serverNotificationError(t, frames, "22012", "division by zero", "")
						serverWantReady(t, frames, 'I')
						serverScalar(t, w, "SELECT count(*) FROM src", "0")
						serverScalar(t, peer, "SELECT count(*) FROM pg_rewrite WHERE ev_class='src'::regclass", "0")
						serverScalar(t, w, "SELECT count(*) FROM sink1", "0")
						serverWantCode(t, w.query(t, queries[0]+"; INSERT INTO src VALUES(3,-30)"), "")
						serverScalar(t, peer, "SELECT v FROM sink1", "30")
						serverNotificationError(t, w.query(t, queries[1]+"; "+queries[2]+"; "+queries[3]+"; INSERT INTO src VALUES(4,40); SELECT 1/0"), "22012", "division by zero", "")
						serverScalar(t, peer, "SELECT rulename FROM pg_rewrite WHERE ev_class='src'::regclass", "r")
						serverWantCode(t, w.query(t, "BEGIN; SAVEPOINT rules; "+queries[1]), "")
						serverWantCode(t, w.query(t, "INSERT INTO src VALUES(4,40)"), "")
						serverScalar(t, w, "SELECT v FROM sink2", "41")
						serverNotificationError(t, w.query(t, "SELECT 1/0"), "22012", "division by zero", "")
						frames = w.query(t, "SELECT 1")
						serverNotificationError(t, frames, "25P02", "current transaction is aborted, commands ignored until end of transaction block", "")
						serverWantReady(t, frames, 'E')
						serverWantCode(t, w.query(t, "ROLLBACK TO rules; RELEASE rules; COMMIT"), "")
						serverScalar(t, peer, "SELECT count(*) FROM sink2", "0")
						serverWantCode(t, w.query(t, "INSERT INTO src VALUES(5,-50)"), "")
						serverScalar(t, w, "SELECT sum(v) FROM sink1", "80")
						// IF EXISTS analysis is quiet; exactly one notice is delivered on Execute.
						frames = serverCycle(t, w, serverParse("missing", "DROP RULE IF EXISTS missing ON src"), serverDescribe('S', "missing"))
						serverWantTags(t, frames, "1tnZ")
						frames = serverCycle(t, w, serverBind("notice", "missing", nil, nil), serverExecute("notice", 0))
						serverWantTags(t, frames, "2NCZ")
						transactionalViewsWireCommand(t, frames, "DROP RULE")
						for _, fr := range frames {
							if fr.tag == 'N' {
								fields := serverFields(fr.body)
								for key, want := range map[byte]string{'S': "NOTICE", 'V': "NOTICE", 'C': "00000", 'M': "rule \"missing\" for relation \"src\" does not exist, skipping", 'P': "", 'D': "", 'H': "", 'W': "", 'p': "", 'q': "", 's': "", 't': "", 'c': "", 'd': "", 'n': ""} {
									if fields[key] != want {
										t.Fatalf("notice field %c got %q want %q", key, fields[key], want)
									}
								}
							}
						}
						serverWantCode(t, w.query(t, "BEGIN; SAVEPOINT duplicate"), "")
						frames = serverCycle(t, w, serverParse("duplicate", queries[0]), serverDescribe('S', "duplicate"))
						serverWantTags(t, frames, "1tnZ")
						serverWantReady(t, frames, 'T')
						frames = serverCycle(t, w, serverBind("duplicate_exec", "duplicate", nil, nil), serverExecute("duplicate_exec", 0))
						serverNotificationError(t, frames, "42710", "rule \"r\" for relation \"src\" already exists", "")
						serverWantReady(t, frames, 'E')
						serverWantCode(t, w.query(t, "ROLLBACK TO duplicate; RELEASE duplicate; COMMIT"), "")
						// Real database/sql Go transactions over the transport, direct and held.
						c, err := b.DB.Conn(f.Ctx)
						if err != nil {
							t.Fatal(err)
						}
						defer c.Close()
						for _, commit := range []bool{false, true} {
							goQueries := []string{
								"CREATE RULE go_rule AS ON INSERT TO src DO ALSO INSERT INTO sink2 VALUES(NEW.id,abs(NEW.v))",
								"CREATE OR REPLACE RULE go_rule AS ON INSERT TO src DO ALSO INSERT INTO sink2 VALUES(NEW.id,NEW.v+1)",
								"ALTER RULE go_rule ON src RENAME TO go_renamed",
								"DROP RULE go_renamed ON src",
							}
							handles := make(map[string]*sql.Stmt)
							if held {
								for _, q := range goQueries {
									s, err := c.PrepareContext(f.Ctx, q)
									if err != nil {
										t.Fatal(err)
									}
									defer s.Close()
									handles[q] = s
								}
							}
							tx, err := c.BeginTx(f.Ctx, nil)
							if err != nil {
								t.Fatal(err)
							}
							defer tx.Rollback()
							run := func(s fixture.Step) {
								var e fixture.Executor = tx
								if h := handles[s.Query]; h != nil {
									e = fixture.HeldStatement{Executor: tx, Statement: h, SQL: s.Query}
								}
								fixture.RunStep(t, f.Ctx, e, b.Recorder, s)
							}
							for i, q := range goQueries {
								if i == 3 {
									run(fixture.Exec("SAVEPOINT drop_mark", 0))
								}
								run(fixture.Exec(q, 0))
								run(fixture.Exec("INSERT INTO src VALUES($1,$2)", 1, 10+i, -10))
							}
							run(fixture.Select("SELECT id,v FROM sink2 ORDER BY id", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(10), int64(10)}, {int64(11), int64(-9)}, {int64(12), int64(-9)}}))
							serverScalar(t, peer, "SELECT count(*) FROM pg_rewrite WHERE ev_class='src'::regclass AND rulename LIKE 'go_%'", "0")
							run(fixture.Exec("ROLLBACK TO drop_mark", 0))
							run(fixture.Exec("RELEASE drop_mark", 0))
							if commit {
								err = tx.Commit()
							} else {
								err = tx.Rollback()
							}
							if err != nil {
								t.Fatal(err)
							}
							count := "0"
							if commit {
								count = "1"
							}
							serverScalar(t, peer, "SELECT count(*) FROM pg_rewrite WHERE ev_class='src'::regclass AND rulename='go_renamed'", count)
						}
						serverScalar(t, w, "SELECT count(*) FROM sink2", "3")
					})
				}
			})
		}
	}
}
