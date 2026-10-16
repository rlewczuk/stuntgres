//go:build !noservertests

package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func serverNestedCopyStart(t *testing.T, w *serverWire, extended bool) {
	t.Helper()
	if extended {
		// "wrapper" is already a published statement, not the new outer
		// portal's statement name. The extra EXECUTE level is intentional.
		for _, packet := range [][]byte{serverParse("outer", "EXECUTE wrapper"), serverBind("p", "outer", nil, nil), serverExecute("p", 0), serverPacket('H', nil)} {
			w.send(t, packet)
		}
		serverWantTags(t, serverThrough(t, w, '2'), "12")
	} else {
		w.send(t, serverPacket('Q', serverCString(nil, "EXECUTE wrapper")))
	}
	response := w.frame(t)
	if response.tag != 'H' || string(response.body) != "\x00\x00\x01\x00\x00" {
		t.Fatalf("nested COPY response %c %x", response.tag, response.body)
	}
}

func TestReferenceServerNestedPreparedExecute(t *testing.T) {
	f := fixture.NewServerFixture(t)
	for _, b := range f.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			dsn := f.DSNFor(b)
			peer := serverAuthenticated(t, dsn)
			for _, extended := range []bool{false, true} {
				route := "simple"
				if extended {
					route = "extended"
				}
				t.Run(route+"/copy_once", func(t *testing.T) {
					w := serverAuthenticated(t, dsn)
					sequence := "server_nested_copy_once_" + route
					serverWantCode(t, w.query(t, "CREATE SEQUENCE "+sequence+" START 1"), "")
					serverWantTags(t, serverCycle(t, w, serverParse("cp", "COPY (SELECT nextval('"+sequence+"') AS n) TO STDOUT")), "1Z")
					serverWantTags(t, serverCycle(t, w, serverParse("wrapper", "EXECUTE cp")), "1Z")
					serverScalar(t, peer, "SELECT is_called FROM "+sequence, "f")
					if extended {
						serverWantCode(t, w.query(t, "BEGIN"), "")
					}
					serverNestedCopyStart(t, w, extended)
					serverPreparedCopyOutput(t, w, []byte("1\n"), "COPY 1")
					if extended {
						w.send(t, serverPacket('S', nil))
					}
					frames := w.ready(t)
					serverWantTags(t, frames, "Z")
					state := byte('I')
					if extended {
						state = 'T'
					}
					serverWantReady(t, frames, state)
					serverScalar(t, peer, "SELECT last_value FROM "+sequence, "1")
					if extended {
						frames = serverCycle(t, w, serverClose('P', "p"))
						serverWantTags(t, frames, "3Z")
						serverWantReady(t, frames, 'T')
						frames = w.query(t, "COMMIT")
						serverWantTags(t, frames, "CZ")
						serverWantCompletions(t, frames, "COMMIT")
						serverWantReady(t, frames, 'I')
					}
					serverScalar(t, w, "SELECT 42", "42")
					serverScalar(t, peer, "SELECT last_value FROM "+sequence, "1")
				})
				t.Run(route+"/copy_runtime_failure", func(t *testing.T) {
					w := serverAuthenticated(t, dsn)
					sequence := "server_nested_copy_failure_" + route
					serverWantCode(t, w.query(t, "CREATE SEQUENCE "+sequence+" START 1"), "")
					// Identical source rows need no portable source-order assumption.
					source := "COPY (SELECT 10/(2-nextval('" + sequence + "')) FROM (VALUES (1),(1)) AS q(v)) TO STDOUT"
					serverWantTags(t, serverCycle(t, w, serverParse("cp", source)), "1Z")
					serverWantTags(t, serverCycle(t, w, serverParse("wrapper", "EXECUTE cp")), "1Z")
					serverScalar(t, peer, "SELECT is_called FROM "+sequence, "f")
					if extended {
						serverWantCode(t, w.query(t, "BEGIN"), "")
					}
					serverNestedCopyStart(t, w, extended)
					frames := serverThrough(t, w, 'E')
					serverWantTags(t, frames, "dE")
					if string(frames[0].body) != "10\n" {
						t.Fatalf("nested COPY prefix %q", frames[0].body)
					}
					serverNotificationError(t, frames, "22012", "division by zero", "")
					serverWantCompletions(t, frames)
					serverScalar(t, peer, "SELECT last_value FROM "+sequence, "2")
					if extended {
						w.send(t, serverPacket('S', nil))
					}
					frames = append(frames, w.ready(t)...)
					serverWantTags(t, frames, "dEZ")
					state := byte('I')
					if extended {
						state = 'E'
					}
					serverWantReady(t, frames, state)
					serverWantCompletions(t, frames)
					if extended {
						frames = w.query(t, "ROLLBACK")
						serverWantTags(t, frames, "CZ")
						serverWantCompletions(t, frames, "ROLLBACK")
						serverWantReady(t, frames, 'I')
					}
					serverScalar(t, w, "SELECT 42", "42")
					serverScalar(t, peer, "SELECT last_value FROM "+sequence, "2")
				})
				t.Run(route+"/noncopy_select", func(t *testing.T) {
					w := serverAuthenticated(t, dsn)
					sequence := "server_nested_select_" + route
					serverWantCode(t, w.query(t, "CREATE SEQUENCE "+sequence+" START 1"), "")
					source := "SELECT nextval('" + sequence + "') AS n FROM (VALUES (1),(1),(1)) AS q(v)"
					serverWantTags(t, serverCycle(t, w, serverParse("base", source)), "1Z")
					serverWantTags(t, serverCycle(t, w, serverParse("wrapper", "EXECUTE base")), "1Z")
					serverScalar(t, peer, "SELECT is_called FROM "+sequence, "f")
					var frames []serverFrame
					if extended {
						serverWantCode(t, w.query(t, "BEGIN"), "")
						frames = serverCycle(t, w, serverParse("outer", "EXECUTE wrapper"), serverBind("p", "outer", nil, nil), serverDescribe('P', "p"), serverExecute("p", 2))
						serverWantTags(t, frames, "12TDDsZ")
						serverWantValues(t, frames, [][]string{{"1"}, {"2"}})
						serverWantReady(t, frames, 'T')
						serverWantCompletions(t, frames)
					} else {
						frames = w.query(t, "EXECUTE wrapper")
						serverWantTags(t, frames, "TDDDCZ")
						serverWantValues(t, frames, [][]string{{"1"}, {"2"}, {"3"}})
						serverWantCompletions(t, frames, "SELECT 3")
						serverWantReady(t, frames, 'I')
					}
					serverWantCode(t, frames, "")
					columns := serverColumns(t, frames)
					expected := serverColumn{"n", 0, 0, 20, 8, -1, 0}
					if len(columns) != 1 || columns[0] != expected {
						t.Fatalf("nested SELECT metadata %+v want %+v", columns, expected)
					}
					// EXECUTE utility output is store-backed: the whole SELECT ran
					// once even if the outer portal delivered only its first two rows.
					serverScalar(t, peer, "SELECT last_value FROM "+sequence, "3")
					if extended {
						frames = serverCycle(t, w, serverExecute("p", 0))
						serverWantTags(t, frames, "DCZ")
						serverWantValues(t, frames, [][]string{{"3"}})
						serverWantCompletions(t, frames, "SELECT 1")
						serverWantReady(t, frames, 'T')
						frames = serverCycle(t, w, serverExecute("p", 0))
						serverWantTags(t, frames, "CZ")
						serverWantCompletions(t, frames, "SELECT 0")
						serverWantReady(t, frames, 'T')
						serverWantTags(t, serverCycle(t, w, serverClose('P', "p")), "3Z")
						frames = w.query(t, "COMMIT")
						serverWantTags(t, frames, "CZ")
						serverWantCompletions(t, frames, "COMMIT")
						serverWantReady(t, frames, 'I')
					}
					serverScalar(t, w, "SELECT 42", "42")
					serverScalar(t, peer, "SELECT last_value FROM "+sequence, "3")
				})
				t.Run(route+"/parameter_frames", func(t *testing.T) {
					w := serverAuthenticated(t, dsn)
					sequence := "server_nested_parameters_" + route
					serverWantCode(t, w.query(t, "CREATE SEQUENCE "+sequence+" START 1"), "")
					serverWantTags(t, serverCycle(t, w, serverParse("base", "SELECT $1 AS n, $1+1 AS next_n", 23)), "1Z")
					// The utility retains its own explicit formal and evaluates
					// the outer volatile actual once. It does not promote that
					// value into a nested utility's lexical $1 namespace.
					frames := serverCycle(t, w, serverParse("wrapper", "EXECUTE base($1)", 23), serverDescribe('S', "wrapper"))
					serverWantTags(t, frames, "1tTZ")
					serverWantCode(t, frames, "")
					for _, frame := range frames {
						if frame.tag == 't' && string(frame.body) != "\x00\x01\x00\x00\x00\x17" {
							t.Fatalf("wrapper formal types %x", frame.body)
						}
					}
					columns := serverColumns(t, frames)
					expected := []serverColumn{{"n", 0, 0, 23, 4, -1, 0}, {"next_n", 0, 0, 23, 4, -1, 0}}
					if len(columns) != 2 || columns[0] != expected[0] || columns[1] != expected[1] {
						t.Fatalf("nested utility metadata %+v want %+v", columns, expected)
					}
					serverScalar(t, peer, "SELECT is_called FROM "+sequence, "f")
					invocation := "EXECUTE wrapper(nextval('" + sequence + "')::integer)"
					if extended {
						serverWantCode(t, w.query(t, "BEGIN"), "")
						frames = serverCycle(t, w, serverParse("outer", invocation), serverBind("p", "outer", nil, nil), serverDescribe('P', "p"), serverExecute("p", 0))
						serverWantTags(t, frames, "12TEZ")
						serverWantReady(t, frames, 'E')
					} else {
						frames = w.query(t, invocation)
						serverWantTags(t, frames, "EZ")
						serverWantReady(t, frames, 'I')
					}
					serverNotificationError(t, frames, "42P02", "there is no parameter $1", "14")
					serverWantValues(t, frames, nil)
					serverWantCompletions(t, frames)
					serverScalar(t, peer, "SELECT last_value FROM "+sequence, "1")
					if extended {
						frames = w.query(t, "ROLLBACK")
						serverWantTags(t, frames, "CZ")
						serverWantCompletions(t, frames, "ROLLBACK")
						serverWantReady(t, frames, 'I')
					}
					serverScalar(t, w, "SELECT 42", "42")
					serverScalar(t, peer, "SELECT last_value FROM "+sequence, "1")
				})
			}
		})
	}
}
