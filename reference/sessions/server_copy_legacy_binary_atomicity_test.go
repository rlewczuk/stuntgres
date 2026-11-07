//go:build !noservertests

package reference_test

import (
	"fmt"
	"strconv"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

// Match every logical ErrorResponse field, including absent fields. PostgreSQL
// source file, line and routine describe its implementation, not SQL behavior.
func serverLegacyBinaryDiagnostic(t *testing.T, frames []serverFrame, want fixture.Diagnostic) {
	t.Helper()
	var diagnostics []fixture.Diagnostic
	for _, frame := range frames {
		if frame.tag != 'E' {
			continue
		}
		fields := serverFields(frame.body)
		diagnostics = append(diagnostics, fixture.Diagnostic{
			Severity: fields['S'], SeverityUnlocalized: fields['V'],
			Code: fields['C'], Message: fields['M'], Detail: fields['D'], Hint: fields['H'],
			Position: fields['P'], InternalPosition: fields['p'], InternalQuery: fields['q'],
			Context: fields['W'], Schema: fields['s'], Table: fields['t'], Column: fields['c'],
			DataType: fields['d'], Constraint: fields['n'],
		})
	}
	if len(diagnostics) != 1 || diagnostics[0] != want {
		t.Fatalf("complete COPY diagnostics = %#v; want %#v", diagnostics, want)
	}
}

func TestReferenceServerCopyLegacyBinaryAtomicity(t *testing.T) {
	f := fixture.NewServerFixture(t)
	for _, backend := range f.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			for _, extended := range []bool{false, true} {
				for _, option := range []string{" BINARY", " WITH BINARY"} {
					for _, fail := range []bool{false, true} {
						t.Run(fmt.Sprintf("extended=%t/%s/copyfail=%t", extended, option, fail), func(t *testing.T) {
							w := serverAuthenticated(t, f.DSNFor(backend))
							peer := serverAuthenticated(t, f.DSNFor(backend))
							serverWantCode(t, w.query(t, "CREATE TABLE legacy_wire_atomic(v int4); INSERT INTO legacy_wire_atomic VALUES (99)"), "")
							defer serverLegacyBinaryCleanup(t, f.DSNFor(backend), "DROP TABLE legacy_wire_atomic", w, peer)
							serverWantTags(t, serverCycle(t, w, serverParse("held_select", "SELECT 42")), "1Z")
							serverWantCode(t, w.query(t, "BEGIN"), "")
							serverLegacyBinaryStart(t, w, "COPY legacy_wire_atomic FROM STDIN"+option, extended, 1)
							var frames []serverFrame
							want := fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22P03", Message: "incorrect binary data format", Context: "COPY legacy_wire_atomic, line 2, column v"}
							if fail {
								// A complete successful binary stream, including trailer, must
								// still be vetoed by CopyFail rather than published early.
								serverLegacyBinaryFragments(t, w, serverCopyBinary([][]byte{{0, 0, 0, 1}}), true)
								w.send(t, serverPacket('f', serverCString(nil, "deliberate")))
								frames = serverThrough(t, w, 'E')
								want = fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "57014", Message: "COPY from stdin failed: deliberate", Context: "COPY legacy_wire_atomic, line 2"}
							} else {
								// Row one is valid; row two supplies a complete five-byte
								// int4 field. Require rejection before trailer or CopyDone.
								payload := serverCopyBinary([][]byte{{0, 0, 0, 1}}, [][]byte{{0, 0, 0, 2, 0}})
								serverLegacyBinaryFragments(t, w, payload[:len(payload)-2], true)
								frames = serverThrough(t, w, 'E')
								// Remaining frontend COPY packets are discarded during recovery.
								w.send(t, serverPacket('d', []byte{255, 255}))
								w.send(t, serverPacket('c', nil))
							}
							serverWantTags(t, frames, "E")
							if extended {
								w.send(t, serverPacket('S', nil))
							}
							frames = append(frames, w.ready(t)...)
							serverWantTags(t, frames, "EZ")
							serverWantReady(t, frames, 'E')
							serverWantCompletions(t, frames)
							serverWantRaw(t, peer.query(t, "SELECT v FROM legacy_wire_atomic ORDER BY v"), [][][]byte{{[]byte("99")}})
							recovered := w.query(t, "ROLLBACK")
							serverWantTags(t, recovered, "CZ")
							serverWantCompletions(t, recovered, "ROLLBACK")
							serverWantReady(t, recovered, 'I')
							serverWantRaw(t, w.query(t, "SELECT v FROM legacy_wire_atomic ORDER BY v"), [][][]byte{{[]byte("99")}})
							serverScalar(t, w, "SELECT 42", "42")
							// Reuse the genuine held SELECT after error/Sync/rollback;
							// no reparse masks damaged extended-protocol state.
							recovered = serverCycle(t, w, serverBind("recovered", "held_select", nil, nil), serverExecute("recovered", 0))
							serverWantTags(t, recovered, "2DCZ")
							serverWantRaw(t, recovered, [][][]byte{{[]byte("42")}})
							serverWantReady(t, recovered, 'I')
							serverLegacyBinaryDiagnostic(t, frames, want)
						})
					}
					t.Run(fmt.Sprintf("extended=%t/%s/sql_prepare_rejected", extended, option), func(t *testing.T) {
						w := serverAuthenticated(t, f.DSNFor(backend))
						query := "PREPARE legacy_sql_prepare AS COPY legacy_prepare_target FROM STDIN" + option
						var frames []serverFrame
						if extended {
							frames = serverCycle(t, w, serverParse("not_a_copy_prepare", query))
						} else {
							frames = w.query(t, query)
						}
						serverWantTags(t, frames, "EZ")
						serverWantReady(t, frames, 'I')
						serverWantCompletions(t, frames)
						serverScalar(t, w, "SELECT 42", "42")
						serverLegacyBinaryDiagnostic(t, frames, fixture.Diagnostic{
							Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42601",
							Message: `syntax error at or near "COPY"`, Position: strconv.Itoa(strings.Index(query, "COPY") + 1),
						})
					})
				}
			}
		})
	}
}
