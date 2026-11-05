package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePgStatStatementsCountersTransactionRotation(t *testing.T) {
	for _, boundary := range []struct{ name, sql string }{
		{"commit", "COMMIT"},
		{"rollback", "ROLLBACK"},
	} {
		for _, prepared := range []bool{false, true} {
			route := "direct"
			if prepared {
				route = "prepared"
			}
			t.Run(boundary.name+"/"+route, func(t *testing.T) {
				pgssCase(t, true, func(s *pgssScenario) {
					pgssCounterSetup(s)
					base := "pgss_part.pgss_counter_rotate_" + boundary.name + "_" + route
					inner, outer := base+"_inner", base+"_outer"
					s.exec("CREATE TABLE " + base + "(k integer,v text)")
					s.exec("CREATE PROCEDURE " + inner + "() LANGUAGE plpgsql AS $$BEGIN " + boundary.sql + "; INSERT INTO " + base + " VALUES(1,'inner'); END$$")
					s.exec("CREATE PROCEDURE " + outer + "() LANGUAGE plpgsql AS $$BEGIN CALL " + inner + "(); INSERT INTO " + base + " VALUES(2,'outer'); END$$")
					s.work(fixture.Exec("SET pg_stat_statements.track='top'", 0))
					s.work(fixture.Exec("SET pg_stat_statements.track_utility=on", 0))
					call, child := "CALL "+outer+"()", "CALL "+inner+"()"
					insert := "INSERT INTO " + base + "%"
					s.counts(call, 0, 0, 0)
					s.counts(child, 0, 0, 0)
					s.counts(insert, 0, 0, 0)

					// A top-level extended-protocol CALL also permits procedural
					// transaction control: the existing PLpgSQLTxControlChainAndFrames
					// family covers both routes. Do not wrap this in BEGIN, SQL EXECUTE,
					// or a procedure SET clause, which changes that admission contract.
					step := fixture.Exec(call, 0)
					if prepared {
						step = fixture.Prepared(step)
					}
					s.work(step)
					s.both(fixture.Select("SELECT k,v FROM "+base+" ORDER BY k", []string{"k", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "inner"}, {int64(2), "outer"}}))
					s.entry(call, 1, 0)
					s.counts(child, 0, 0, 0)
					s.counts(insert, 0, 0, 0)

					// Both accepted non-NULL (integer,text) images are 72 bytes:
					// 24 header + 8 bitmap + 8 integer slot + 32 text slot. Rotation
					// must retain the outer utility's inclusive accounting interval,
					// even though neither the inner CALL nor its INSERT is tracked.
					// PostgreSQL also supplies genuine positive shared read work;
					// native VALUES insertion opens no storage source. Both retain
					// positive WAL work without equating physical/image magnitudes.
					pgssCounterCheck(s, call, 1, 0, pgssCounterWant{dirty: 2, records: 2, bytes: 144, pgShared: s.b.PG, pgWAL: true, serial: true})
				})
			})
		}
	}
}
