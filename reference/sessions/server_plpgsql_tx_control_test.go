//go:build !noservertests

package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceServerPLpgSQLTxControlContext(t *testing.T) {
	f := fixture.NewServerFixture(t)
	for _, b := range f.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			dsn := f.DSNFor(b)
			w := serverAuthenticated(t, dsn)
			defer w.Close()
			peer := serverAuthenticated(t, dsn)
			defer peer.Close()
			serverWantCode(t, w.query(t, "CREATE TABLE tx_wire(v integer)"), "")
			serverWantCode(t, w.query(t, "CREATE PROCEDURE tx_wire_p(IN n integer) LANGUAGE plpgsql AS $$BEGIN INSERT INTO tx_wire VALUES(n+1);COMMIT;INSERT INTO tx_wire VALUES(n+2);ROLLBACK;INSERT INTO tx_wire VALUES(n+3);END$$"), "")
			serverWantTags(t, serverCycle(t, w, serverParse("call", "CALL tx_wire_p($1::integer)", 23)), "1Z")
			serverScalar(t, peer, "SELECT count(*) FROM tx_wire", "0")
			for _, base := range []string{"0", "10", "20"} {
				serverWantCode(t, w.query(t, "TRUNCATE tx_wire"), "")
				frames := serverCycle(t, w, serverBind("call_portal", "call", nil, [][]byte{[]byte(base)}), serverDescribe('P', "call_portal"), serverExecute("call_portal", 0))
				serverWantCode(t, frames, "")
				serverWantReady(t, frames, 'I')
				serverWantCompletions(t, frames, "CALL")
				serverWantTags(t, frames, "2nCZ")
				serverScalar(t, peer, "SELECT count(*) FROM tx_wire", "2")
				want := map[string][][]string{"0": {{"1"}, {"3"}}, "10": {{"11"}, {"13"}}, "20": {{"21"}, {"23"}}}[base]
				serverWantValues(t, peer.query(t, "SELECT v FROM tx_wire ORDER BY v"), want)
			}
			serverWantCode(t, w.query(t, "TRUNCATE tx_wire"), "")
			inline := "DO $$BEGIN INSERT INTO tx_wire VALUES(1);COMMIT;INSERT INTO tx_wire VALUES(2);ROLLBACK;INSERT INTO tx_wire VALUES(3);END$$"
			serverWantTags(t, serverCycle(t, w, serverParse("inline", inline), serverDescribe('S', "inline")), "1tnZ")
			serverScalar(t, peer, "SELECT count(*) FROM tx_wire", "0")
			for _, packet := range [][]byte{serverBind("inline_portal", "inline", nil, nil), serverDescribe('P', "inline_portal"), serverPacket('H', nil)} {
				w.send(t, packet)
			}
			serverWantTags(t, serverThrough(t, w, 'n'), "2n")
			serverScalar(t, peer, "SELECT count(*) FROM tx_wire", "0")
			inlineFrames := serverCycle(t, w, serverExecute("inline_portal", 0))
			serverWantTags(t, inlineFrames, "CZ")
			serverWantCompletions(t, inlineFrames, "DO")
			serverWantReady(t, inlineFrames, 'I')
			serverWantValues(t, peer.query(t, "SELECT v FROM tx_wire ORDER BY v"), [][]string{{"1"}, {"3"}})
			serverWantCode(t, w.query(t, "BEGIN"), "")
			inlineFrames = serverCycle(t, w, serverBind("inline_portal", "inline", nil, nil), serverExecute("inline_portal", 0))
			serverNotificationError(t, inlineFrames, "2D000", "invalid transaction termination", "", "PL/pgSQL function inline_code_block line 1 at COMMIT")
			serverWantReady(t, inlineFrames, 'E')
			serverWantCode(t, w.query(t, "ROLLBACK"), "")
			for _, stage := range []string{"bound", "completed", "suspended"} {
				t.Run(stage, func(t *testing.T) {
					serverWantCode(t, w.query(t, "TRUNCATE tx_wire"), "")
					prior := "prior_" + stage
					packets := [][]byte{serverParse(prior, "SELECT x FROM generate_series(1,2) x"), serverBind("prior_portal", prior, nil, nil)}
					if stage != "bound" {
						limit := uint32(0)
						if stage == "suspended" {
							limit = 1
						}
						packets = append(packets, serverExecute("prior_portal", limit))
					}
					packets = append(packets, serverBind("call_portal", "call", nil, [][]byte{[]byte("0")}), serverExecute("call_portal", 0))
					frames := serverCycle(t, w, packets...)
					if stage == "bound" {
						serverWantCode(t, frames, "")
						serverScalar(t, peer, "SELECT count(*) FROM tx_wire", "2")
					} else {
						serverNotificationError(t, frames, "2D000", "invalid transaction termination", "", "PL/pgSQL function tx_wire_p(integer) line 1 at COMMIT")
						serverScalar(t, peer, "SELECT count(*) FROM tx_wire", "0")
					}
					serverWantReady(t, frames, 'I')
				})
			}
			serverWantCode(t, w.query(t, "TRUNCATE tx_wire"), "")
			frames := serverCycle(t, w, serverBind("call_portal", "call", nil, [][]byte{[]byte("0")}), serverExecute("call_portal", 0), serverParse("future", "SELECT 42"), serverBind("future_portal", "future", nil, nil), serverExecute("future_portal", 0))
			serverWantCode(t, frames, "")
			serverWantValues(t, frames, [][]string{{"42"}})
			serverWantCompletions(t, frames, "CALL", "SELECT 1")
			serverScalar(t, peer, "SELECT count(*) FROM tx_wire", "2")
			for _, q := range []string{"CALL tx_wire_p(0);SELECT 42", "SELECT 42;CALL tx_wire_p(0)", "COMMIT;CALL tx_wire_p(0)"} {
				serverWantCode(t, w.query(t, "TRUNCATE tx_wire"), "")
				serverNotificationError(t, w.query(t, q), "2D000", "invalid transaction termination", "", "PL/pgSQL function tx_wire_p(integer) line 1 at COMMIT")
				serverScalar(t, peer, "SELECT count(*) FROM tx_wire", "0")
				serverNotificationError(t, serverCycle(t, w, serverParse("multiple", q)), "42601", "cannot insert multiple commands into a prepared statement", "")
			}
			for _, stage := range []string{"bound", "completed", "suspended"} {
				cross := "cross_" + stage
				packets := [][]byte{serverParse(cross, "SELECT x FROM generate_series(1,2) x"), serverBind("cross_portal", cross, nil, nil)}
				for _, packet := range packets {
					w.send(t, packet)
				}
				w.send(t, serverPacket('H', nil))
				serverWantTags(t, serverThrough(t, w, '2'), "12")
				if stage != "bound" {
					limit := uint32(0)
					end := byte('C')
					if stage == "suspended" {
						limit = 1
						end = 's'
					}
					w.send(t, serverExecute("cross_portal", limit))
					w.send(t, serverPacket('H', nil))
					serverThrough(t, w, end)
				}
				frames := w.query(t, "CALL tx_wire_p(0)")
				if stage == "bound" {
					serverWantCode(t, frames, "")
				} else {
					serverNotificationError(t, frames, "2D000", "invalid transaction termination", "", "PL/pgSQL function tx_wire_p(integer) line 1 at COMMIT")
				}
				serverCycle(t, w)
			}
		})
	}
}
