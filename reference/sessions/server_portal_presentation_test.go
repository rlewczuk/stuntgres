//go:build !noservertests

package reference_test

import (
	"reflect"
	"strconv"
	"testing"

	"stuntgres/reference/fixture"
)

func serverPresentationFinish(t *testing.T, w *serverWire) {
	t.Helper()
	// Both previous fetches requested exactly one actual row. Neither may
	// look ahead: only this fetch discovers exhaustion and completes SELECT 0.
	frames := serverCycle(t, w, serverExecute("p", 0))
	serverWantTags(t, frames, "CZ")
	serverWantCode(t, frames, "")
	serverWantCompletions(t, frames, "SELECT 0")
	serverWantReady(t, frames, 'T')
	frames = serverCycle(t, w, serverClose('P', "p"))
	serverWantTags(t, frames, "3Z")
	serverWantReady(t, frames, 'T')
	frames = w.query(t, "COMMIT")
	serverWantTags(t, frames, "CZ")
	serverWantCompletions(t, frames, "COMMIT")
	serverWantReady(t, frames, 'I')
	serverScalar(t, w, "SELECT 42", "42")
}

func TestReferenceServerPortalPresentation(t *testing.T) {
	f := fixture.NewServerFixture(t)
	for _, b := range f.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			dsn := f.DSNFor(b)
			t.Run("live_regclass", func(t *testing.T) {
				w := serverAuthenticated(t, dsn)
				peer := serverAuthenticated(t, dsn)
				serverWantCode(t, w.query(t, "SET search_path = public"), "")
				serverWantCode(t, w.query(t, "CREATE TABLE public.server_presentation_target(v integer)"), "")
				serverWantCode(t, w.query(t, "CREATE SEQUENCE public.server_presentation_live_seq START 1"), "")
				identity := w.query(t, "SELECT 'public.server_presentation_target'::regclass::oid")
				serverWantCode(t, identity, "")
				values := serverValues(t, identity)
				if len(values) != 1 || len(values[0]) != 1 {
					t.Fatalf("relation identity rows %v", values)
				}
				oid := values[0][0]
				if n, err := strconv.ParseUint(oid, 10, 32); err != nil || n == 0 {
					t.Fatalf("relation identity %q: %v", oid, err)
				}
				serverWantCode(t, w.query(t, "BEGIN"), "")
				// Same typed REGCLASS OID in both rows; nextval independently
				// proves position/demand and rules out replay after SET.
				source := "SELECT 'public.server_presentation_target'::regclass AS relation_name, 'public.server_presentation_target'::regclass::oid AS identity, nextval('public.server_presentation_live_seq') AS step FROM (VALUES (1),(1)) AS q(v)"
				frames := serverCycle(t, w, serverParse("live", source), serverBind("p", "live", nil, nil))
				serverWantTags(t, frames, "12Z")
				serverWantCode(t, frames, "")
				serverWantReady(t, frames, 'T')
				serverScalar(t, peer, "SELECT is_called FROM public.server_presentation_live_seq", "f")
				columns := []serverColumn{{"relation_name", 0, 0, 2205, 4, -1, 0}, {"identity", 0, 0, 26, 4, -1, 0}, {"step", 0, 0, 20, 8, -1, 0}}
				for i, spelling := range []string{"server_presentation_target", "public.server_presentation_target"} {
					if i == 1 {
						frames = w.query(t, "SET search_path = pg_catalog")
						serverWantTags(t, frames, "CSZ")
						if string(frames[1].body) != "search_path\x00pg_catalog\x00" {
							t.Fatalf("search_path status %q", frames[1].body)
						}
						serverWantCompletions(t, frames, "SET")
						serverWantReady(t, frames, 'T')
					}
					step := strconv.Itoa(i + 1)
					frames = serverCycle(t, w, serverDescribe('P', "p"), serverExecute("p", 1))
					serverWantTags(t, frames, "TDsZ")
					serverWantCode(t, frames, "")
					serverWantValues(t, frames, [][]string{{spelling, oid, step}})
					serverWantCompletions(t, frames)
					serverWantReady(t, frames, 'T')
					if got := serverColumns(t, frames); !reflect.DeepEqual(got, columns) {
						t.Fatalf("live portal metadata %+v want %+v", got, columns)
					}
					serverScalar(t, peer, "SELECT last_value FROM public.server_presentation_live_seq", step)
				}
				serverPresentationFinish(t, w)
				serverScalar(t, peer, "SELECT last_value FROM public.server_presentation_live_seq", "2")
			})
			t.Run("stored_date", func(t *testing.T) {
				w := serverAuthenticated(t, dsn)
				peer := serverAuthenticated(t, dsn)
				serverWantCode(t, w.query(t, "SET DateStyle = 'ISO, MDY'"), "")
				serverWantCode(t, w.query(t, "CREATE SEQUENCE public.server_presentation_store_seq START 1"), "")
				serverWantCode(t, w.query(t, "PREPARE server_presentation_dates AS SELECT d, nextval('public.server_presentation_store_seq') AS step FROM (VALUES (DATE '2024-02-03'),(DATE '2024-11-12')) AS q(d) ORDER BY d"), "")
				serverWantCode(t, w.query(t, "BEGIN"), "")
				frames := serverCycle(t, w, serverParse("stored", "EXECUTE server_presentation_dates"), serverBind("p", "stored", nil, nil))
				serverWantTags(t, frames, "12Z")
				serverWantCode(t, frames, "")
				serverWantReady(t, frames, 'T')
				serverScalar(t, peer, "SELECT is_called FROM public.server_presentation_store_seq", "f")
				columns := []serverColumn{{"d", 0, 0, 1082, 4, -1, 0}, {"step", 0, 0, 20, 8, -1, 0}}
				for i, date := range []string{"2024-02-03", "12/11/2024"} {
					if i == 1 {
						frames = w.query(t, "SET DateStyle = 'SQL, DMY'")
						serverWantTags(t, frames, "CSZ")
						if string(frames[1].body) != "DateStyle\x00SQL, DMY\x00" {
							t.Fatalf("DateStyle status %q", frames[1].body)
						}
						serverWantCompletions(t, frames, "SET")
						serverWantReady(t, frames, 'T')
					}
					frames = serverCycle(t, w, serverDescribe('P', "p"), serverExecute("p", 1))
					serverWantTags(t, frames, "TDsZ")
					serverWantCode(t, frames, "")
					serverWantValues(t, frames, [][]string{{date, strconv.Itoa(i + 1)}})
					serverWantCompletions(t, frames)
					serverWantReady(t, frames, 'T')
					if got := serverColumns(t, frames); !reflect.DeepEqual(got, columns) {
						t.Fatalf("stored portal metadata %+v want %+v", got, columns)
					}
					// Utility output is already stored after the first fetch. Its
					// values and evaluation count stay fixed; only text changes.
					serverScalar(t, peer, "SELECT last_value FROM public.server_presentation_store_seq", "2")
				}
				serverPresentationFinish(t, w)
				serverScalar(t, peer, "SELECT last_value FROM public.server_presentation_store_seq", "2")
			})
		})
	}
}
