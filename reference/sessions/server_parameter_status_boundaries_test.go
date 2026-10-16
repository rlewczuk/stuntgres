//go:build !noservertests

package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func serverParameterBoundaryStart(t *testing.T, dsn string) *serverWire {
	t.Helper()
	w := serverDial(t, dsn)
	frames := w.login(t, dsn, 196608, "options", "-c search_path=public -c DateStyle=ISO,MDY")
	serverWantCode(t, frames, "")
	serverWantReady(t, frames, 'I')
	serverWantStatus(t, frames, "search_path", "public")
	serverWantStatus(t, frames, "DateStyle", "ISO, MDY")
	return w
}

func serverBoundaryDateStatus(t *testing.T, frames []serverFrame, want string) {
	t.Helper()
	serverWantStatus(t, frames, "DateStyle", want)
	count := 0
	for _, frame := range frames {
		if frame.tag == 'S' {
			count++
			if string(frame.body) != "DateStyle\x00"+want+"\x00" {
				t.Fatalf("ParameterStatus body %q want DateStyle=%q", frame.body, want)
			}
		}
	}
	if count != 1 {
		t.Fatalf("ParameterStatus count %d want 1", count)
	}
}

func TestReferenceServerParameterStatusBoundaries(t *testing.T) {
	f := fixture.NewServerFixture(t)
	for _, b := range f.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			dsn := f.DSNFor(b)
			t.Run("startup_and_multi_query", func(t *testing.T) {
				w := serverParameterBoundaryStart(t, dsn)
				frames := w.query(t, "SET DateStyle = 'SQL, DMY'; SET DateStyle = 'ISO, DMY'; SET DateStyle = 'SQL, DMY'")
				serverWantTags(t, frames, "CCCSZ")
				serverWantCode(t, frames, "")
				serverWantCompletions(t, frames, "SET", "SET", "SET")
				serverBoundaryDateStatus(t, frames, "SQL, DMY")
				serverWantReady(t, frames, 'I')
				// An intermediate change back to ISO is never reported: the
				// final value equals the last value actually sent to this client.
				frames = w.query(t, "SET DateStyle = 'ISO, MDY'; SET DateStyle = 'SQL, DMY'")
				serverWantTags(t, frames, "CCZ")
				serverWantCode(t, frames, "")
				serverWantCompletions(t, frames, "SET", "SET")
				serverWantReady(t, frames, 'I')
				serverScalar(t, w, "SHOW DateStyle", "SQL, DMY")
			})
			t.Run("extended_flush_then_sync_and_rollback", func(t *testing.T) {
				w := serverParameterBoundaryStart(t, dsn)
				frames := w.query(t, "BEGIN")
				serverWantTags(t, frames, "CZ")
				serverWantReady(t, frames, 'T')
				for _, packet := range [][]byte{serverParse("set_style", "SET DateStyle = 'SQL, DMY'"), serverBind("p", "set_style", nil, nil), serverExecute("p", 0), serverPacket('H', nil)} {
					w.send(t, packet)
				}
				frames = serverThrough(t, w, 'C')
				serverWantTags(t, frames, "12C")
				serverWantCode(t, frames, "")
				serverWantCompletions(t, frames, "SET")
				// A second real frontend exchange fences the completed Flush.
				// No sleeps/read-timeouts are needed to prove that neither SET
				// nor Flush nor subsequent Parse/Describe has emitted S or Z.
				for _, packet := range [][]byte{serverParse("fence", "SELECT 42"), serverDescribe('S', "fence"), serverPacket('H', nil)} {
					w.send(t, packet)
				}
				frames = serverThrough(t, w, 'T')
				serverWantTags(t, frames, "1tT")
				serverWantCode(t, frames, "")
				w.send(t, serverPacket('S', nil))
				frames = w.ready(t)
				serverWantTags(t, frames, "SZ")
				serverBoundaryDateStatus(t, frames, "SQL, DMY")
				serverWantReady(t, frames, 'T')
				serverScalar(t, w, "SELECT DATE '2024-02-03'", "03/02/2024")
				frames = w.query(t, "ROLLBACK")
				serverWantTags(t, frames, "CSZ")
				serverWantCompletions(t, frames, "ROLLBACK")
				serverBoundaryDateStatus(t, frames, "ISO, MDY")
				serverWantReady(t, frames, 'I')
				serverScalar(t, w, "SELECT DATE '2024-02-03'", "2024-02-03")
			})
			t.Run("error_restores_before_ready", func(t *testing.T) {
				w := serverParameterBoundaryStart(t, dsn)
				frames := w.query(t, "BEGIN; SET DateStyle = 'SQL, DMY'")
				serverWantTags(t, frames, "CCSZ")
				serverWantCode(t, frames, "")
				serverWantCompletions(t, frames, "BEGIN", "SET")
				serverBoundaryDateStatus(t, frames, "SQL, DMY")
				serverWantReady(t, frames, 'T')
				frames = w.query(t, "SET DateStyle = 'German, DMY'; SELECT 1/0")
				serverWantTags(t, frames, "CESZ")
				serverWantCompletions(t, frames, "SET")
				serverNotificationError(t, frames, "22012", "division by zero", "")
				// Abort restores the outer setting before the error's readiness
				// report. The unreported German intermediate never reaches S.
				serverBoundaryDateStatus(t, frames, "ISO, MDY")
				serverWantReady(t, frames, 'E')
				frames = w.query(t, "ROLLBACK")
				serverWantTags(t, frames, "CZ")
				serverWantCompletions(t, frames, "ROLLBACK")
				serverWantReady(t, frames, 'I')
				serverScalar(t, w, "SHOW DateStyle", "ISO, MDY")
				serverScalar(t, w, "SELECT 42", "42")
			})
		})
	}
}
