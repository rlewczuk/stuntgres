package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePgStatStatementsCountersSmoke(t *testing.T) {
	pgssCase(t, true, func(s *pgssScenario) {
		pgssCounterSetup(s)
		table := "pgss_part.pgss_counter_smoke"
		s.exec("CREATE TABLE " + table + "(k integer,v text)")
		insert := "INSERT INTO " + table + " VALUES($1::integer,$2::text),($3::integer,$4::text)"
		s.counts(insert, 0, 0, 0)
		s.work(fixture.Exec(insert, 2, int64(1), "one", int64(2), "two"))
		pgssCounterCheck(s, insert, 1, 2, pgssCounterWant{dirty: 2, records: 2, bytes: 144, pgWAL: true, serial: true})
		q := "SELECT k,v FROM " + table + " ORDER BY k"
		data := fixture.Select(q, []string{"k", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "one"}, {int64(2), "two"}})
		s.work(data)
		pgssCounterCheck(s, q, 1, 2, pgssCounterWant{read: 1, pgShared: true, serial: true})
		held, err := s.worker.PrepareContext(s.p.Ctx, q)
		if err != nil {
			s.t.Fatal(err)
		}
		defer held.Close()
		fixture.RunStep(s.t, s.p.Ctx, fixture.HeldStatement{Executor: s.worker, Statement: held, SQL: q}, s.b.Recorder, data)
		pgssCounterCheck(s, q, 2, 4, pgssCounterWant{read: 1, hit: 1, pgShared: true, serial: true})
		update := "UPDATE " + table + " SET v=$1::text WHERE k=$2::integer"
		s.work(fixture.Prepared(fixture.Exec(update, 1, nil, int64(1))))
		pgssCounterCheck(s, update, 1, 1, pgssCounterWant{anySharedSplit: true, sharedRequests: 1, dirty: 1, records: 1, bytes: 112, pgShared: true, pgWAL: true, serial: true})
		data.Rows = [][]any{{int64(1), nil}, {int64(2), "two"}}
		s.both(data)
		s.both(fixture.Select("SELECT * FROM pgss_part.pg_stat_statements(NULL::boolean)", pgssNames, pgssTypes, nil))
		s.work(fixture.Exec("CREATE TEMP TABLE pgss_counter_smoke_temp(k integer,v text)", 0))
		tempInsert := "INSERT INTO pgss_counter_smoke_temp VALUES($1::integer,$2::text)"
		s.work(fixture.Exec(tempInsert, 1, int64(8), "local"))
		pgssCounterCheck(s, tempInsert, 1, 1, pgssCounterWant{localDirty: 1, pgNoWAL: true, serial: true})
		tempRead := "SELECT k,v FROM pgss_counter_smoke_temp"
		s.work(fixture.Prepared(fixture.Select(tempRead, []string{"k", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(8), "local"}})))
		pgssCounterCheck(s, tempRead, 1, 1, pgssCounterWant{localHit: 1, pgLocal: true, pgNoWAL: true, serial: true})
		pgssCounterRoundtrip(s, table, data, 224) // 2*R(NULL row40)+2*R(text row72).
	})
}
