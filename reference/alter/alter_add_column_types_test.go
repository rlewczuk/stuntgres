package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceAlterAddColumnTypes(t *testing.T) {
	alterAddReferenceScenario(t, func(p *fixture.Pair, backend fixture.Backend) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s) }
		run(fixture.Exec("CREATE TABLE t(id INT)", 0))
		run(fixture.Exec("INSERT INTO t VALUES(1),(2)", 2))
		run(fixture.Exec(`ALTER TABLE t ADD small SMALLINT DEFAULT 12, ADD big BIGINT DEFAULT 9223372036854775807, ADD flag BOOL DEFAULT TRUE, ADD bytes BYTEA DEFAULT '\x0041ff'::BYTEA, ADD number NUMERIC(4,1) DEFAULT 12.34, ADD fixed CHAR(3) DEFAULT 'x', ADD varying VARCHAR(3) DEFAULT 'xy'`, 0))
		names := []string{"id", "small", "big", "flag", "bytes", "number", "fixed", "varying"}
		types := []string{"INT4", "INT2", "INT8", "BOOL", "BYTEA", "NUMERIC", "BPCHAR", "VARCHAR"}
		rows := [][]any{{int64(1), int64(12), int64(9223372036854775807), true, string([]byte{0, 65, 255}), "12.3", "x  ", "xy"}, {int64(2), int64(12), int64(9223372036854775807), true, string([]byte{0, 65, 255}), "12.3", "x  ", "xy"}}
		run(fixture.Select("SELECT * FROM t ORDER BY id", names, types, rows))
		for _, prepared := range []bool{false, true} {
			fixture.TableDDLMetadata(t, p.Ctx, backend.DB, backend.PG, prepared, "SELECT * FROM t", names, types, []bool{true, true, true, true, true, true, true, true}, 2)
		}
		run(fixture.Exec("ALTER TABLE t ADD stamp TIMESTAMP(2) DEFAULT TIMESTAMP '2004-07-01 12:00:00.126', ADD clock TIME(2) DEFAULT TIME '12:00:00.126', ADD day DATE DEFAULT DATE '2004-07-01'", 0))
		run(fixture.Select("SELECT stamp::TEXT AS stamp,clock::TEXT AS clock,day::TEXT AS day FROM t ORDER BY id", []string{"stamp", "clock", "day"}, []string{"TEXT", "TEXT", "TEXT"}, [][]any{{"2004-07-01 12:00:00.13", "12:00:00.13", "2004-07-01"}, {"2004-07-01 12:00:00.13", "12:00:00.13", "2004-07-01"}}))
	})
}
