package reference_test

import (
	"math/big"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePgStatStatementsCountersMetadata(t *testing.T) {
	pgssCase(t, true, func(s *pgssScenario) {
		pgssCounterSetup(s)
		s.exec("CREATE TABLE pgss_part.pgss_counter_metadata(k integer,v text)")
		insert := "INSERT INTO pgss_part.pgss_counter_metadata VALUES($1::integer,$2::text),($3::integer,$4::text)"
		s.counts(insert, 0, 0, 0)
		s.work(fixture.Exec(insert, 2, int64(1), "a", int64(2), "b"))
		pgssCounterCheck(s, insert, 1, 2, pgssCounterWant{dirty: 2, records: 2, bytes: 144, pgWAL: true, serial: true})
		vector := pgssCounterSnapshot(s, insert)
		falseSQL := "SELECT " + strings.Join(pgssNames[18:50], ",") + " FROM pgss_part.pg_stat_statements(false) WHERE queryid=(SELECT queryid FROM pgss_part.pg_stat_statements WHERE query=$1 AND userid=(SELECT oid FROM pg_roles WHERE rolname=current_user) AND dbid=(SELECT oid FROM pg_database WHERE datname=current_database())) AND userid=(SELECT oid FROM pg_roles WHERE rolname=current_user) AND dbid=(SELECT oid FROM pg_database WHERE datname=current_database())"
		s.both(fixture.Select(falseSQL, pgssNames[18:50], pgssTypes[18:50], vector, insert))
		for _, show := range []string{"true", "false"} {
			q := "SELECT wal_bytes FROM pgss_part.pg_stat_statements(" + show + ") WHERE queryid=(SELECT queryid FROM pgss_part.pg_stat_statements WHERE query=$1 AND userid=(SELECT oid FROM pg_roles WHERE rolname=current_user) AND dbid=(SELECT oid FROM pg_database WHERE datname=current_database()))"
			held := pgssResetHeld(s, q)
			for _, x := range []fixture.Executor{s.observer, held} {
				rows, err := x.QueryContext(s.p.Ctx, q, insert)
				if err != nil {
					s.t.Fatal(err)
				}
				metadata, err := rows.ColumnTypes()
				if err != nil {
					s.t.Fatal(err)
				}
				if len(metadata) != 1 || metadata[0].Name() != "wal_bytes" || metadata[0].DatabaseTypeName() != "NUMERIC" {
					s.t.Fatalf("wal_bytes descriptor=%v", metadata)
				}
				fixture.ScalarAssertMetadata(s.t, s.b.PG, metadata)
				if !rows.Next() {
					s.t.Fatalf("missing measured numeric: %v", rows.Err())
				}
				var decimal string
				if err := rows.Scan(&decimal); err != nil {
					s.t.Fatal(err)
				}
				value, ok := new(big.Int).SetString(decimal, 10)
				if !ok || value.Sign() <= 0 || (!s.b.PG && decimal != "144") {
					s.t.Fatalf("WAL NUMERIC=%q, want positive exact integer (native 144)", decimal)
				}
				if rows.Next() || rows.Err() != nil {
					s.t.Fatalf("numeric row multiplicity/error: %v", rows.Err())
				}
				rows.Close()
			}
		}
		for _, q := range []string{"SELECT * FROM pgss_part.pg_stat_statements(NULL::boolean)", "SELECT * FROM pgss_part.pg_stat_statements($1::boolean)"} {
			step := fixture.Select(q, pgssNames, pgssTypes, nil)
			if q == "SELECT * FROM pgss_part.pg_stat_statements($1::boolean)" {
				step.Args = []any{nil}
			}
			s.both(step)
		}
		info := pgssResetInfo(s)
		if len(info) != 1 || info[0][0] == nil || info[0][1] == nil {
			s.t.Fatalf("info must have one nonNULL sample: %#v", info)
		}
		s.both(fixture.Select("SELECT * FROM pgss_part.pg_stat_statements_info()", []string{"dealloc", "stats_reset"}, []string{"INT8", "TIMESTAMPTZ"}, info))
		s.both(fixture.Select("SELECT * FROM pgss_part.pg_stat_statements_info", []string{"dealloc", "stats_reset"}, []string{"INT8", "TIMESTAMPTZ"}, info))
		s.both(fixture.Select("SELECT dealloc>=0 AS valid,stats_reset<=clock_timestamp() AS dated FROM pgss_part.pg_stat_statements_info()", []string{"valid", "dated"}, []string{"BOOL", "BOOL"}, [][]any{{true, true}}))
	})
}
