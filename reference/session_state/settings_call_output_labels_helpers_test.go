package reference_test

import (
	"context"
	"database/sql"
	"reflect"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

// Each backend owns its subtest, so missing native syntax cannot prevent the
// independent PostgreSQL scenario from running during oracle discovery.
func settingsLabelScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn, *sql.Conn)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			owner, err := backend.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { owner.Close() })
			peer, err := backend.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { peer.Close() })
			body(t, p, backend, owner, peer)
		})
	}
}

func settingsLabelValue(pg bool, query, name string, want any, args ...any) fixture.Step {
	s := fixture.Select(query, []string{name}, []string{"TEXT"}, [][]any{{want}}, args...)
	s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
		settingsLabelMetadata(t, rows, pg, name, strings.HasPrefix(query, "SELECT"))
	}
	return s
}

func settingsLabelMetadata(t *testing.T, rows *sql.Rows, pg bool, name string, nullable bool) {
	t.Helper()
	settingsTextMetadata(t, rows, pg, nullable)
	columns, err := rows.Columns()
	if err != nil || !reflect.DeepEqual(columns, []string{name}) {
		t.Fatal(columns, err)
	}
	types, err := rows.ColumnTypes()
	if err != nil || len(types) != 1 || types[0].Name() != name {
		t.Fatal(types, err)
	}
	// pq retains its own returned slice; the native adapter promises detached
	// names. Never mutate pq's slice and invent that additional contract for it.
	if !pg {
		columns[0] = "changed-by-caller"
		again, err := rows.Columns()
		if err != nil || !reflect.DeepEqual(again, []string{name}) || types[0].Name() != name {
			t.Fatal(again, err)
		}
	}
}

func settingsLabelRoute(t *testing.T, ctx context.Context, conn *sql.Conn, recorder *fixture.Recorder, route, query string) func(fixture.Step) {
	t.Helper()
	var executor fixture.Executor = conn
	if route == "held" {
		stmt, err := conn.PrepareContext(ctx, query)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := stmt.Close(); err != nil {
				t.Error(err)
			}
		})
		executor = fixture.HeldStatement{Executor: conn, Statement: stmt, SQL: query}
	}
	return func(s fixture.Step) {
		s.Prepared = route == "fresh"
		fixture.RunStep(t, ctx, executor, recorder, s)
	}
}

func settingsLabelFailure(query string, diagnostic fixture.Diagnostic, args ...any) fixture.Step {
	s := fixture.ImplicitError(query, diagnostic, args...)
	s.SelectRows, s.ErrorStage = true, "execute"
	return s
}

func settingsLabelState(t *testing.T, p *fixture.Pair, b fixture.Backend, owner, peer *sql.Conn, want, peerWant string) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, owner, b.Recorder, settingsLabelValue(b.PG, "SHOW application_name", "application_name", want))
	fixture.RunStep(t, p.Ctx, peer, b.Recorder, settingsLabelValue(b.PG, "SHOW application_name", "application_name", peerWant))
}
