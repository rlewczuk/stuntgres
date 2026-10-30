package reference_test

import (
	"database/sql"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

var recoveryControlNames = []string{"pg_is_wal_replay_paused", "pg_get_wal_replay_pause_state", "pg_promote", "pg_wal_replay_pause", "pg_wal_replay_resume"}
var recoveryControlTypes = []string{"BOOL", "TEXT", "BOOL", "VOID", "VOID"}
const recoveryControlProjection = "SELECT pg_is_wal_replay_paused(),pg_get_wal_replay_pause_state(),pg_promote(),pg_wal_replay_pause(),pg_wal_replay_resume() WHERE FALSE"

func recoveryControlError(query string) fixture.Step {
	s := fixture.ImplicitError(query, fixture.Diagnostic{Code: "55000", Message: "recovery is not in progress", Hint: "Recovery control functions can only be executed during recovery."})
	s.SelectRows, s.ErrorStage = true, "execute"
	return s
}
func recoveryControlRoutes(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, s fixture.Step) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
	recoveryHeld(t, p, b, c, s)
}
func recoveryControlPrimary(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT pg_is_in_recovery()", []string{"pg_is_in_recovery"}, []string{"BOOL"}, [][]any{{false}}))
}
func TestReferenceRecoveryControlPrimary(t *testing.T) {
	calls := []string{"pg_promote(false)", "pg_promote(true,1)", "pg_promote(false,0)", "pg_promote(true,-1)", "pg_promote(wait_seconds=>1)", "pg_promote(wait=>false)", "pg_promote(wait_seconds=>1,wait=>false)", "pg_promote(false,wait_seconds=>1)", "pg_promote(wait_seconds:=1,wait:=false)", "pg_catalog.PG_PROMOTE(WAIT_SECONDS=>1,WAIT=>false)"}
	for _, name := range recoveryControlNames { calls = append(calls, name+"()") }
	for _, call := range calls {
		t.Run(call, func(t *testing.T) {
			recoveryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				recoveryControlRoutes(t, p, b, c, recoveryControlError("SELECT "+call))
				recoveryControlPrimary(t, p, b, c)
				fixture.RunStep(t, p.Ctx, c, b.Recorder, recoveryControlError("SELECT pg_is_wal_replay_paused()"))
			})
		})
	}
}

func recoveryControlMetadata(pg bool, names []string) func(*testing.T, *sql.Rows) {
	return func(t *testing.T, rows *sql.Rows) {
		columns, err := rows.ColumnTypes()
		if err != nil || len(columns) != 5 { t.Fatal(columns, err) }
		scans := []reflect.Type{reflect.TypeFor[bool](), reflect.TypeFor[string](), reflect.TypeFor[bool](), reflect.TypeFor[string](), reflect.TypeFor[string]()}
		if pg { scans[3], scans[4] = reflect.TypeFor[any](), reflect.TypeFor[any]() }
		for i, c := range columns {
			if c.Name() != names[i] || c.DatabaseTypeName() != recoveryControlTypes[i] || c.ScanType() != scans[i] { t.Fatalf("column %d: %s/%s/%v", i, c.Name(), c.DatabaseTypeName(), c.ScanType()) }
			if n, ok := c.Nullable(); n || ok { t.Fatal("computed nullability", n, ok) }
			length, lengthKnown := c.Length()
			wantLength, wantLengthKnown := int64(0), false
			if recoveryControlTypes[i] == "TEXT" { wantLength, wantLengthKnown = 9223372036854775807, true }
			if length != wantLength || lengthKnown != wantLengthKnown { t.Fatalf("column %d %s Length=%d/%t, want=%d/%t", i, c.DatabaseTypeName(), length, lengthKnown, wantLength, wantLengthKnown) }
			if _, _, ok := c.DecimalSize(); ok { t.Fatal("unexpected precision") }
		}
	}
}
func recoveryControlNull(query string, args ...any) fixture.Step {
	s := fixture.Select(query, []string{"promoted"}, []string{"BOOL"}, [][]any{{nil}}, args...)
	s.CheckScan = func(t *testing.T, rows *sql.Rows, _ int) {
		var raw any
		if err := rows.Scan(&raw); err != nil || raw != nil { t.Fatal("raw typed NULL", raw, err) }
	}
	s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
		columns, err := rows.ColumnTypes()
		if err != nil || len(columns) != 1 { t.Fatal(columns, err) }
		c := columns[0]
		if c.Name() != "promoted" || c.DatabaseTypeName() != "BOOL" || c.ScanType() != reflect.TypeFor[bool]() { t.Fatal("strict NULL descriptor", c) }
		if _, ok := c.Length(); ok { t.Fatal("BOOL length") }
		if _, _, ok := c.DecimalSize(); ok { t.Fatal("BOOL precision") }
	}
	return s
}
func TestReferenceRecoveryControlNullsMetadata(t *testing.T) {
	recoveryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, call := range []string{"pg_promote(NULL)", "pg_promote(true,NULL)", "pg_promote(wait_seconds=>NULL)", "pg_promote(NULL,0)", "pg_promote(wait_seconds=>0,wait=>NULL)", "pg_promote(NULL::boolean)", "pg_promote(true,NULL::integer)"} {
			recoveryControlRoutes(t, p, b, c, recoveryControlNull("SELECT "+call+" AS promoted"))
		}
		for _, s := range []fixture.Step{
			recoveryControlNull("SELECT pg_promote($1::boolean,1) AS promoted", nil),
			recoveryControlNull("SELECT pg_promote(true,$1::integer) AS promoted", nil),
			fixture.Select("SELECT COALESCE(pg_promote(NULL),false) AS promoted", []string{"promoted"}, []string{"BOOL"}, [][]any{{false}}),
		} { recoveryControlRoutes(t, p, b, c, s) }
		for _, alias := range []bool{false, true} {
			query, names := recoveryControlProjection, recoveryControlNames
			if alias { query = "SELECT pg_is_wal_replay_paused() AS a,pg_get_wal_replay_pause_state() AS b,pg_promote() AS c,pg_wal_replay_pause() AS d,pg_wal_replay_resume() AS e WHERE FALSE"; names = []string{"a", "b", "c", "d", "e"} }
			s := fixture.Select(query, names, recoveryControlTypes, nil)
			s.CheckMetadata = recoveryControlMetadata(b.PG, names)
			recoveryControlRoutes(t, p, b, c, s)
		}
	})
}
