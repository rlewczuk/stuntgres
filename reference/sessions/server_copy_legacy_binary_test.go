//go:build !noservertests

package reference_test

import (
	"bytes"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

// The payload is independently framed, never obtained from a COPY export or
// pgx encoder: int4/text/bool, with NULL distinct from an empty text field.
func serverLegacyBinaryPayload() []byte {
	return serverCopyBinary([][]byte{{0, 0, 0, 42}, []byte("hi"), {1}}, [][]byte{nil, {}, {0}})
}

func serverLegacyBinaryRows() [][][]byte {
	return [][][]byte{{[]byte("42"), []byte("hi"), []byte("t")}, {nil, {}, []byte("f")}}
}

func serverLegacyBinaryCleanup(t *testing.T, dsn, drop string, wires ...*serverWire) {
	t.Helper()
	// Retire every case socket before DROP can wait on its COPY or transaction.
	// A fresh peer cannot inherit queued responses or a failed transaction.
	for _, w := range wires {
		w.Close()
	}
	cleanup := serverAuthenticated(t, dsn)
	defer cleanup.Close()
	serverWantCode(t, cleanup.query(t, drop), "")
}

func serverLegacyBinaryFragments(t *testing.T, w *serverWire, payload []byte, varying bool) {
	t.Helper()
	for offset, step := 0, 0; offset < len(payload); step++ {
		n := 1
		if varying {
			n = []int{1, 3, 2, 7, 1, 4}[step%6]
		}
		n = min(n, len(payload)-offset)
		w.send(t, serverPacket('d', payload[offset:offset+n]))
		offset += n
	}
}

// Observe CommandComplete before sending Sync, rather than accepting an early
// ReadyForQuery hidden inside a combined completion/readiness assertion.
func serverLegacyBinaryFinish(t *testing.T, w *serverWire, extended bool, count int, ready byte) {
	t.Helper()
	w.send(t, serverPacket('c', nil))
	w.send(t, serverPacket('H', nil))
	frames := serverThrough(t, w, 'C')
	serverWantTags(t, frames, "C")
	serverWantCompletions(t, frames, fmt.Sprintf("COPY %d", count))
	if extended {
		w.send(t, serverPacket('S', nil))
	}
	frames = w.ready(t)
	serverWantTags(t, frames, "Z")
	serverWantReady(t, frames, ready)
}

func serverLegacyBinaryStart(t *testing.T, w *serverWire, sql string, extended bool, columns int) {
	t.Helper()
	if extended {
		for _, packet := range [][]byte{serverParse("", sql), serverBind("", "", nil, nil), serverExecute("", 0), serverPacket('H', nil)} {
			w.send(t, packet)
		}
		// Fail on an immediate Parse/Bind error instead of waiting for a
		// completion that cannot arrive before the recovery Sync.
		serverWantTags(t, []serverFrame{w.frame(t)}, "1")
		serverWantTags(t, []serverFrame{w.frame(t)}, "2")
	} else {
		w.send(t, serverPacket('Q', serverCString(nil, sql)))
	}
	serverLegacyBinaryResponse(t, w, columns)
}

func serverLegacyBinaryNamedStart(t *testing.T, w *serverWire, portal string, columns int) {
	t.Helper()
	w.send(t, serverBind(portal, "legacy_copy", nil, nil))
	w.send(t, serverExecute(portal, 0))
	w.send(t, serverPacket('H', nil))
	serverWantTags(t, []serverFrame{w.frame(t)}, "2")
	serverLegacyBinaryResponse(t, w, columns)
}

func serverLegacyBinaryResponse(t *testing.T, w *serverWire, columns int) {
	t.Helper()
	frame := w.frame(t)
	want := server16([]byte{1}, columns)
	for range columns {
		want = server16(want, 1)
	}
	if frame.tag != 'G' || !bytes.Equal(frame.body, want) {
		t.Fatalf("CopyInResponse %c %x want G %x", frame.tag, frame.body, want)
	}
}

func TestReferenceServerCopyLegacyBinary(t *testing.T) {
	f := fixture.NewServerFixture(t)
	for _, backend := range f.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			for _, extended := range []bool{false, true} {
				for _, option := range []string{" BINARY", " WITH BiNaRy"} {
					t.Run(fmt.Sprintf("extended=%t/%s", extended, option), func(t *testing.T) {
						w := serverAuthenticated(t, f.DSNFor(backend))
						serverWantCode(t, w.query(t, `CREATE TABLE public."Legacy Wire Rows"("i" int4,"t" text,"b" bool)`), "")
						defer serverLegacyBinaryCleanup(t, f.DSNFor(backend), `DROP TABLE public."Legacy Wire Rows"`, w)
						serverLegacyBinaryStart(t, w, `COPY public."Legacy Wire Rows"("i","t","b") FROM STDIN`+option, extended, 3)
						serverLegacyBinaryFragments(t, w, serverLegacyBinaryPayload(), extended)
						serverLegacyBinaryFinish(t, w, extended, 2, 'I')
						frames := w.query(t, `SELECT i,t,b FROM public."Legacy Wire Rows" ORDER BY i NULLS LAST`)
						serverWantRaw(t, frames, serverLegacyBinaryRows())
						cols := serverColumns(t, frames)
						if len(cols) != 3 {
							t.Fatalf("columns: %+v", cols)
						}
						for i, want := range []struct {
							name string
							oid  uint32
						}{{"i", 23}, {"t", 25}, {"b", 16}} {
							if cols[i].name != want.name || cols[i].oid != want.oid || cols[i].format != 0 {
								t.Fatalf("column %d: %+v want %+v text format", i, cols[i], want)
							}
						}
						serverScalar(t, w, "SELECT 42", "42")
					})
				}
			}
		})
	}
}

func TestReferenceServerCopyLegacyBinaryPrepared(t *testing.T) {
	f := fixture.NewServerFixture(t)
	for _, backend := range f.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			for _, option := range []string{" BINARY", " WITH BINARY"} {
				t.Run(option, func(t *testing.T) {
					w := serverAuthenticated(t, f.DSNFor(backend))
					serverWantCode(t, w.query(t, "CREATE TABLE legacy_wire_prepared(i int4,t text,b bool)"), "")
					defer serverLegacyBinaryCleanup(t, f.DSNFor(backend), "DROP TABLE legacy_wire_prepared", w)
					// Exactly one Parse for the COPY definition, before either portal.
					frames := serverCycle(t, w, serverParse("legacy_copy", "COPY legacy_wire_prepared FROM STDIN"+option))
					serverWantTags(t, frames, "1Z")
					serverWantReady(t, frames, 'I')
					serverWantRaw(t, serverCycle(t, w, serverParse("ordinary", "SELECT 42"), serverBind("before", "ordinary", nil, nil), serverExecute("before", 0)), [][][]byte{{[]byte("42")}})
					for _, portal := range []string{"first_portal", "fresh_portal"} {
						serverLegacyBinaryNamedStart(t, w, portal, 3)
						serverLegacyBinaryFragments(t, w, serverLegacyBinaryPayload(), true)
						serverLegacyBinaryFinish(t, w, true, 2, 'I')
					}
					want := [][][]byte{{[]byte("42"), []byte("hi"), []byte("t")}, {[]byte("42"), []byte("hi"), []byte("t")}, {nil, {}, []byte("f")}, {nil, {}, []byte("f")}}
					serverWantRaw(t, w.query(t, "SELECT i,t,b FROM legacy_wire_prepared ORDER BY i NULLS LAST"), want)
					// The unrelated held SELECT also survives both COPY cycles.
					serverWantRaw(t, serverCycle(t, w, serverBind("after", "ordinary", nil, nil), serverExecute("after", 0)), [][][]byte{{[]byte("42")}})
					serverWantTags(t, serverCycle(t, w, serverClose('S', "legacy_copy"), serverClose('S', "ordinary")), "33Z")
				})
			}
		})
	}
}

func TestReferenceServerCopyLegacyBinaryTransactions(t *testing.T) {
	f := fixture.NewServerFixture(t)
	for _, backend := range f.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			for _, extended := range []bool{false, true} {
				for _, option := range []string{" BINARY", " WITH BINARY"} {
					t.Run(fmt.Sprintf("extended=%t/%s", extended, option), func(t *testing.T) {
						w := serverAuthenticated(t, f.DSNFor(backend))
						peer := serverAuthenticated(t, f.DSNFor(backend))
						serverWantCode(t, w.query(t, "CREATE TABLE legacy_wire_tx(v int4); INSERT INTO legacy_wire_tx VALUES (99)"), "")
						defer serverLegacyBinaryCleanup(t, f.DSNFor(backend), "DROP TABLE legacy_wire_tx", w, peer)
						for _, terminal := range []string{"ROLLBACK", "COMMIT"} {
							frames := w.query(t, "BEGIN")
							serverWantReady(t, frames, 'T')
							serverWantCode(t, frames, "")
							serverLegacyBinaryStart(t, w, "COPY legacy_wire_tx FROM STDIN"+option, extended, 1)
							serverLegacyBinaryFragments(t, w, serverCopyBinary([][]byte{{0, 0, 0, 1}}), true)
							serverLegacyBinaryFinish(t, w, extended, 1, 'T')
							serverWantRaw(t, w.query(t, "SELECT v FROM legacy_wire_tx ORDER BY v"), [][][]byte{{[]byte("1")}, {[]byte("99")}})
							serverWantRaw(t, peer.query(t, "SELECT v FROM legacy_wire_tx ORDER BY v"), [][][]byte{{[]byte("99")}})
							frames = w.query(t, terminal)
							serverWantTags(t, frames, "CZ")
							serverWantCompletions(t, frames, terminal)
							serverWantReady(t, frames, 'I')
							want := [][][]byte{{[]byte("99")}}
							if terminal == "COMMIT" {
								want = [][][]byte{{[]byte("1")}, {[]byte("99")}}
							}
							serverWantRaw(t, w.query(t, "SELECT v FROM legacy_wire_tx ORDER BY v"), want)
							serverWantRaw(t, peer.query(t, "SELECT v FROM legacy_wire_tx ORDER BY v"), want)
						}
					})
				}
			}
		})
	}
}
