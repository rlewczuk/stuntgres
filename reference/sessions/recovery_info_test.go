package reference_test

import (
	"database/sql"
	"reflect"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

var recoveryNames = []string{"pg_is_in_recovery", "pg_last_wal_receive_lsn", "pg_last_wal_replay_lsn", "pg_last_xact_replay_timestamp", "pg_get_wal_resource_managers"}
var recoveryTypes = []string{"BOOL", "PG_LSN", "PG_LSN", "TIMESTAMPTZ"}
const recoverySQL = "SELECT pg_is_in_recovery(),pg_last_wal_receive_lsn(),pg_last_wal_replay_lsn(),pg_last_xact_replay_timestamp()"

func recoveryScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, err := b.DB.Conn(p.Ctx)
			if err != nil { t.Fatal(err) }
			defer c.Close()
			body(t, p, b, c)
		})
	}
}

func recoveryHeld(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, s fixture.Step) {
	t.Helper()
	stmt, err := c.PrepareContext(p.Ctx, s.Query)
	if err != nil { t.Fatal("prepare recovery inquiry", err) }
	defer stmt.Close()
	held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: s.Query}
	for range 2 { fixture.RunStep(t, p.Ctx, held, b.Recorder, s) }
}

func recoveryValueStep(query string, empty bool) fixture.Step {
	var rows [][]any
	if !empty { rows = [][]any{{false, nil, nil, nil}} }
	return fixture.Select(query, recoveryNames[:4], recoveryTypes, rows)
}

func TestReferenceRecoveryInfoValues(t *testing.T) {
	recoveryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		steps := []fixture.Step{
			recoveryValueStep(recoverySQL, false),
			fixture.Select("SELECT pg_last_wal_receive_lsn() IS NULL AS receive_null,pg_last_wal_replay_lsn() IS NULL AS replay_null,pg_last_xact_replay_timestamp() IS NULL AS timestamp_null,COALESCE(pg_last_wal_receive_lsn(),'0/1'::pg_lsn) AS fallback,COALESCE(pg_last_xact_replay_timestamp(),'2000-01-01 00:00:00+00'::timestamptz) AS ts", []string{"receive_null", "replay_null", "timestamp_null", "fallback", "ts"}, []string{"BOOL", "BOOL", "BOOL", "PG_LSN", "TIMESTAMPTZ"}, [][]any{{true, true, true, "0/1", time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)}}),
			fixture.Select("SELECT pg_catalog.PG_IS_IN_RECOVERY() AS recovering,PG_LAST_WAL_RECEIVE_LSN() AS received,pg_catalog.pg_last_wal_replay_lsn() AS replayed,PG_LAST_XACT_REPLAY_TIMESTAMP() AS replay_time", []string{"recovering", "received", "replayed", "replay_time"}, recoveryTypes, [][]any{{false, nil, nil, nil}}),
		}
		for _, s := range steps {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
			recoveryHeld(t, p, b, c, s)
		}
	})
}

func recoveryMetadata(pg bool, names []string) func(*testing.T, *sql.Rows) {
	return func(t *testing.T, rows *sql.Rows) {
		columns, err := rows.ColumnTypes()
		if err != nil || len(columns) != 4 { t.Fatal(columns, err) }
		scans := []reflect.Type{reflect.TypeFor[bool](), reflect.TypeFor[string](), reflect.TypeFor[string](), reflect.TypeFor[time.Time]()}
		if pg { scans[1], scans[2] = reflect.TypeFor[any](), reflect.TypeFor[any]() }
		for i, c := range columns {
			if c.Name() != names[i] || c.DatabaseTypeName() != recoveryTypes[i] || c.ScanType() != scans[i] { t.Fatalf("column %d: %s/%s/%v", i, c.Name(), c.DatabaseTypeName(), c.ScanType()) }
			if n, ok := c.Nullable(); n || ok { t.Fatal("computed nullability", n, ok) }
			if _, ok := c.Length(); ok { t.Fatal("unexpected length") }
			if _, _, ok := c.DecimalSize(); ok { t.Fatal("unexpected precision") }
		}
	}
}

func TestReferenceRecoveryInfoMetadata(t *testing.T) {
	recoveryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, empty := range []bool{false, true} {
			for _, aliased := range []bool{false, true} {
				query, names := recoverySQL, recoveryNames[:4]
				if aliased {
					query = "SELECT pg_is_in_recovery() AS a,pg_last_wal_receive_lsn() AS b,pg_last_wal_replay_lsn() AS c,pg_last_xact_replay_timestamp() AS d"
					names = []string{"a", "b", "c", "d"}
				}
				if empty { query += " WHERE FALSE" }
				s := recoveryValueStep(query, empty)
				s.Columns = names
				s.CheckMetadata = recoveryMetadata(b.PG, names)
				s.CheckScan = func(t *testing.T, rows *sql.Rows, _ int) {
					var flag bool
					var receive, replay, timestamp any
					if err := rows.Scan(&flag, &receive, &replay, &timestamp); err != nil { t.Fatal(err) }
					if flag || receive != nil || replay != nil || timestamp != nil { t.Fatal("raw recovery values", flag, receive, replay, timestamp) }
				}
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
				recoveryHeld(t, p, b, c, s)
			}
		}
	})
}

func TestReferenceRecoveryInfoFeatureSmoke(t *testing.T) {
	recoveryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, empty := range []bool{false, true} {
			query := recoverySQL
			if empty { query += " WHERE FALSE" }
			s := recoveryValueStep(query, empty)
			s.CheckMetadata = recoveryMetadata(b.PG, recoveryNames[:4])
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT COALESCE(pg_last_wal_receive_lsn(),'0/1'::pg_lsn) AS lsn", []string{"lsn"}, []string{"PG_LSN"}, [][]any{{"0/1"}}))
		meta := fixture.Select("SELECT * FROM pg_get_wal_resource_managers() LIMIT 0", []string{"rm_id", "rm_name", "rm_builtin"}, []string{"INT4", "TEXT", "BOOL"}, nil)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, meta)
		s := recoveryManagersDemand(b.PG)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
	})
}
