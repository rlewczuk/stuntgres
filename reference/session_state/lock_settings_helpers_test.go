package reference_test

import (
	"database/sql"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

var lockSettings = []struct{ name, raw, show, context, min, description, extra string }{
	{"deadlock_timeout", "1000", "1s", "superuser", "1", "Sets the time to wait on a lock before checking for deadlock.", ""},
	{"max_locks_per_transaction", "64", "64", "postmaster", "10", "Sets the maximum number of locks per transaction.", `The shared lock table is sized on the assumption that at most "max_locks_per_transaction" objects per server process or prepared transaction will need to be locked at any one time.`},
	{"max_pred_locks_per_page", "2", "2", "sighup", "0", "Sets the maximum number of predicate-locked tuples per page.", "If more than this number of tuples on the same page are locked by a connection, those locks are replaced by a page-level lock."},
	{"max_pred_locks_per_relation", "-2", "-2", "sighup", "-2147483648", "Sets the maximum number of predicate-locked pages and tuples per relation.", "If more than this total of pages and tuples in the same relation are locked by a connection, those locks are replaced by a relation-level lock."},
	{"max_pred_locks_per_transaction", "64", "64", "postmaster", "10", "Sets the maximum number of predicate locks per transaction.", `The shared predicate lock table is sized on the assumption that at most "max_pred_locks_per_transaction" objects per server process or prepared transaction will need to be locked at any one time.`},
}

const lockPredicate = "name IN ('deadlock_timeout','max_locks_per_transaction','max_pred_locks_per_page','max_pred_locks_per_relation','max_pred_locks_per_transaction')"

func (s *physicalCase) lockInventory(prepared bool, raw, source string) {
	s.t.Helper()
	var want [][]any
	for _, d := range lockSettings {
		var unit, extra any
		value, origin := d.raw, "default"
		if d.name == "deadlock_timeout" { unit, value, origin = "ms", raw, source }
		if d.extra != "" { extra = d.extra }
		want = append(want, []any{d.name, value, unit, "Lock Management", d.description, extra, d.context, "integer", origin, d.min, "2147483647", nil, d.raw, d.raw, nil, nil, false})
	}
	step := fixture.Select("SELECT "+physicalColumnsSQL+" FROM pg_settings WHERE "+lockPredicate+" ORDER BY name", physicalColumns, physicalTypes, want)
	step.Prepared = prepared
	step.CheckMetadata = func(t *testing.T, rows *sql.Rows) { statisticsSettingsCheckMetadata(t, rows, s.pg, physicalColumns, physicalTypes) }
	s.run(step)
}

func (s *physicalCase) lockReads(prepared bool, display string) {
	s.t.Helper()
	for _, d := range lockSettings {
		value := d.show
		if d.name == "deadlock_timeout" { value = display }
		for _, query := range []struct{ sql, column string }{{"SHOW "+d.name, d.name}, {"SELECT current_setting("+physicalLiteral(d.name)+")", "current_setting"}} {
			step := clientText(query.sql, query.column, value)
			step.Prepared = prepared
			step.CheckMetadata = func(t *testing.T, rows *sql.Rows) { settingsTextMetadata(t, rows, s.pg, query.column == "current_setting") }
			s.run(step)
		}
	}
}

func lockContext(name, context string) fixture.Diagnostic {
	if context == "postmaster" { return physicalRestart(name) }
	return physicalDiagnostic("55P02", fmt.Sprintf("parameter %q cannot be changed now", name))
}

func (s *physicalCase) lockSentinel() {
	s.t.Helper()
	s.run(fixture.Select("SELECT v FROM lock_settings_sentinel", []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}}))
}

func (s *physicalCase) lockPeer() *physicalCase {
	s.t.Helper()
	pool := s.p.Memory
	if s.pg { pool = s.p.Postgres }
	conn, err := pool.Conn(s.p.Ctx)
	if err != nil { s.t.Fatal(err) }
	s.t.Cleanup(func() { conn.Close() })
	return &physicalCase{s.t, s.p, conn, s.pg, s.recorder}
}
