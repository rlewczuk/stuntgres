//go:build !noservertests

package reference_test

import (
	"strconv"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceServerWrappedPortalCounts(t *testing.T) {
	f := fixture.NewServerFixture(t)
	for _, b := range f.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			for _, returning := range []bool{false, true} {
				name := "select"
				if returning {
					name = "insert_returning"
				}
				t.Run(name, func(t *testing.T) {
					dsn := f.DSNFor(b)
					w := serverAuthenticated(t, dsn)
					peer := serverAuthenticated(t, dsn)
					defer func() { serverWantCode(t, w.query(t, "ROLLBACK"), "") }()
					prepared := "server_wrapped_select"
					var table uint32
					var attribute int16
					if returning {
						serverWantCode(t, w.query(t, "CREATE TABLE server_wrapped_returns(id integer)"), "")
						serverWantCode(t, w.query(t, "PREPARE server_wrapped_insert AS INSERT INTO server_wrapped_returns VALUES (1),(2),(3) RETURNING id"), "")
						prepared = "server_wrapped_insert"
						// Generated relation identity is checked relationally; names, type, physical
						// length, typmod, format, attribute and wire values are independent goldens.
						identity := w.query(t, "SELECT 'server_wrapped_returns'::regclass::oid")
						serverWantCode(t, identity, "")
						values := serverValues(t, identity)
						if len(values) != 1 || len(values[0]) != 1 {
							t.Fatalf("relation identity rows %v", values)
						}
						n, err := strconv.ParseUint(values[0][0], 10, 32)
						if err != nil || n == 0 {
							t.Fatalf("relation identity %v: %v", values, err)
						}
						table = uint32(n)
						attribute = 1
					} else {
						serverWantCode(t, w.query(t, "PREPARE server_wrapped_select AS SELECT id FROM (VALUES (1),(2),(3)) AS q(id) ORDER BY id"), "")
					}
					serverWantCode(t, w.query(t, "BEGIN"), "")
					frames := serverCycle(t, w, serverParse("wire", "EXECUTE "+prepared), serverDescribe('S', "wire"), serverBind("p", "wire", nil, nil), serverDescribe('P', "p"))
					serverWantTags(t, frames, "1tT2TZ")
					serverWantCode(t, frames, "")
					serverWantReady(t, frames, 'T')
					descriptions := serverDescriptions(t, frames)
					if len(descriptions) != 2 {
						t.Fatalf("descriptions %v", descriptions)
					}
					expected := serverColumn{"id", table, attribute, 23, 4, -1, 0}
					for _, columns := range descriptions {
						if len(columns) != 1 || columns[0] != expected {
							t.Fatalf("wrapped result metadata %+v want %+v", columns, expected)
						}
					}
					for _, fr := range frames {
						if fr.tag == 't' && string(fr.body) != "\x00\x00" {
							t.Fatalf("wrapper formal description %x", fr.body)
						}
					}
					limit := uint32(2)
					wantFirst := [][]string{{"1"}, {"2"}}
					wantFirstTags := "DDsZ"
					if returning {
						limit = 1
						wantFirst = wantFirst[:1]
						wantFirstTags = "DsZ"
					}
					frames = serverCycle(t, w, serverExecute("p", limit))
					serverWantTags(t, frames, wantFirstTags)
					serverWantCode(t, frames, "")
					serverWantValues(t, frames, wantFirst)
					serverWantCompletions(t, frames)
					serverWantReady(t, frames, 'T')
					if returning {
						serverScalar(t, w, "SELECT count(*) FROM server_wrapped_returns", "3")
						serverScalar(t, peer, "SELECT count(*) FROM server_wrapped_returns", "0")
					}
					frames = serverCycle(t, w, serverExecute("p", 0))
					serverWantCode(t, frames, "")
					serverWantReady(t, frames, 'T')
					if returning {
						serverWantTags(t, frames, "DDCZ")
						serverWantValues(t, frames, [][]string{{"2"}, {"3"}})
						serverWantCompletions(t, frames, "INSERT 0 2")
					} else {
						serverWantTags(t, frames, "DCZ")
						serverWantValues(t, frames, [][]string{{"3"}})
						serverWantCompletions(t, frames, "SELECT 1")
					}
					frames = serverCycle(t, w, serverExecute("p", 0))
					serverWantTags(t, frames, "CZ")
					serverWantCode(t, frames, "")
					serverWantReady(t, frames, 'T')
					if len(serverRawRows(t, frames)) != 0 {
						t.Fatal("exhausted wrapper replayed rows")
					}
					if returning {
						serverWantCompletions(t, frames, "INSERT 0 0")
						serverScalar(t, w, "SELECT count(*) FROM server_wrapped_returns", "3")
						serverScalar(t, peer, "SELECT count(*) FROM server_wrapped_returns", "0")
					} else {
						serverWantCompletions(t, frames, "SELECT 0")
					}
					frames = serverCycle(t, w, serverClose('P', "p"))
					serverWantTags(t, frames, "3Z")
					serverWantReady(t, frames, 'T')
					frames = w.query(t, "COMMIT")
					serverWantTags(t, frames, "CZ")
					serverWantCode(t, frames, "")
					serverWantCompletions(t, frames, "COMMIT")
					serverWantReady(t, frames, 'I')
					if returning {
						serverWantValues(t, peer.query(t, "SELECT id FROM server_wrapped_returns ORDER BY id"), [][]string{{"1"}, {"2"}, {"3"}})
					}
					serverScalar(t, w, "SELECT 42", "42")
				})
			}
		})
	}
}
