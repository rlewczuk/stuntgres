//go:build !noservertests

package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceServerUnnamedCreationFastPaths(t *testing.T) {
	f := fixture.NewServerFixture(t)
	for _, b := range f.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			dsn := f.DSNFor(b)
			for _, existing := range []bool{false, true} {
				for _, probe := range []bool{false, true} {
					name := "nodata"
					if existing {
						name = "existing"
					}
					if probe {
						name += "_probe"
					} else {
						name += "_commit"
					}
					t.Run(name, func(t *testing.T) {
						peer := serverAuthenticated(t, dsn)
						destination := "server_creation_" + name
						sequence := "server_creation_seq_" + name
						serverWantCode(t, peer.query(t, "CREATE SEQUENCE "+sequence+" START 1"), "")
						defer func() { serverWantCode(t, peer.query(t, "DROP SEQUENCE "+sequence), "") }()
						defer func() { serverWantCode(t, peer.query(t, "DROP TABLE IF EXISTS "+destination), "") }()
						if existing {
							serverWantCode(t, peer.query(t, "CREATE TABLE "+destination+"(v integer); INSERT INTO "+destination+" VALUES (99)"), "")
						}
						serverScalar(t, peer, "SELECT is_called FROM "+sequence, "f")
						w := serverAuthenticated(t, dsn)
						defer func() { serverWantCode(t, w.query(t, "ROLLBACK"), "") }()
						// Retain a real implicit cycle without Sync. Both portals advance once;
						// the named sibling must later resume at row2, not replay row1.
						for _, packet := range [][]byte{serverParse("rows", "SELECT v FROM (VALUES (1),(2)) AS q(v) ORDER BY v"), serverBind("", "rows", nil, nil), serverBind("named", "rows", nil, nil), serverExecute("", 1), serverPacket('H', nil)} {
							w.send(t, packet)
						}
						frames := serverThrough(t, w, 's')
						serverWantTags(t, frames, "122Ds")
						serverWantValues(t, frames, [][]string{{"1"}})
						w.send(t, serverExecute("named", 1))
						w.send(t, serverPacket('H', nil))
						frames = serverThrough(t, w, 's')
						serverWantTags(t, frames, "Ds")
						serverWantValues(t, frames, [][]string{{"1"}})
						sql := "CREATE TABLE " + destination + " AS SELECT nextval('" + sequence + "') AS v FROM (VALUES (1),(1)) AS q(v) WITH NO DATA"
						if existing {
							sql = "CREATE TABLE IF NOT EXISTS " + destination + " AS SELECT nextval('" + sequence + "') AS v FROM (VALUES (1),(1)) AS q(v)"
						}
						frames = w.query(t, sql)
						serverWantCode(t, frames, "")
						serverWantReady(t, frames, 'T')
						serverWantCompletions(t, frames, "CREATE TABLE AS")
						if existing {
							serverWantTags(t, frames, "NCZ")
							fields := serverFields(frames[0].body)
							for key, want := range map[byte]string{'S': "NOTICE", 'V': "NOTICE", 'C': "42P07", 'M': "relation \"" + destination + "\" already exists, skipping", 'P': "", 'D': "", 'H': "", 'W': "", 'p': "", 'q': "", 's': "", 't': "", 'c': "", 'd': "", 'n': ""} {
								if fields[key] != want {
									t.Fatalf("skip notice field %c=%q want %q: %v", key, fields[key], want, fields)
								}
							}
						} else {
							serverWantTags(t, frames, "CZ")
						}
						before := "0"
						if existing {
							before = "1"
						}
						serverScalar(t, peer, "SELECT count(*) FROM pg_class WHERE relname='"+destination+"'", before)
						serverScalar(t, peer, "SELECT is_called FROM "+sequence, "f")
						w.send(t, serverExecute("named", 0))
						w.send(t, serverPacket('H', nil))
						frames = serverThrough(t, w, 'C')
						serverWantTags(t, frames, "DC")
						serverWantValues(t, frames, [][]string{{"2"}})
						serverWantCompletions(t, frames, "SELECT 1")
						if probe {
							// Missing-name diagnostics abort implicit work. Prove replacement before
							// Sync in this arm, and publication independently in the clean arm.
							w.send(t, serverDescribe('P', ""))
							w.send(t, serverPacket('H', nil))
							frames = []serverFrame{w.frame(t)}
							serverWantTags(t, frames, "E")
							serverNotificationError(t, frames, "34000", "portal \"\" does not exist", "")
						}
						w.send(t, serverPacket('S', nil))
						frames = w.ready(t)
						serverWantTags(t, frames, "Z")
						serverWantReady(t, frames, 'I')
						after := "1"
						if probe && !existing {
							after = "0"
						}
						serverScalar(t, peer, "SELECT count(*) FROM pg_class WHERE relname='"+destination+"'", after)
						serverScalar(t, peer, "SELECT is_called FROM "+sequence, "f")
						if existing {
							serverWantValues(t, peer.query(t, "SELECT v FROM "+destination), [][]string{{"99"}})
						} else if !probe {
							frames = peer.query(t, "SELECT v FROM "+destination)
							serverWantTags(t, frames, "TCZ")
							serverWantCode(t, frames, "")
							serverWantCompletions(t, frames, "SELECT 0")
							serverWantReady(t, frames, 'I')
							if len(serverRawRows(t, frames)) != 0 {
								t.Fatal("WITH NO DATA demanded rows")
							}
							columns := serverColumns(t, frames)
							if len(columns) != 1 || columns[0].name != "v" || columns[0].table == 0 || columns[0].attribute != 1 || columns[0].oid != 20 || columns[0].length != 8 || columns[0].typmod != -1 || columns[0].format != 0 {
								t.Fatalf("WITH NO DATA schema %+v", columns)
							}
						}
						serverScalar(t, w, "SELECT 42", "42")
					})
				}
			}
		})
	}
}
