package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func pgssCounterMutationRoutes(t *testing.T) {
	pgssCase(t, true, func(s *pgssScenario) {
		pgssCounterSetup(s)
		s.exec("CREATE TABLE pgss_part.pgss_counter_parts(k integer,v text) PARTITION BY RANGE(k)")
		s.exec("CREATE TABLE pgss_part.pgss_counter_low PARTITION OF pgss_part.pgss_counter_parts FOR VALUES FROM(0) TO(10)")
		s.exec("CREATE TABLE pgss_part.pgss_counter_high PARTITION OF pgss_part.pgss_counter_parts FOR VALUES FROM(10) TO(20)")
		q := "INSERT INTO pgss_part.pgss_counter_parts VALUES($1::integer,$2::text),($3::integer,$4::text) RETURNING k,v"
		step := fixture.Select(q, []string{"k", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "low"}, {int64(11), "high"}}, int64(1), "low", int64(11), "high")
		step.Bag = true
		s.work(fixture.Prepared(step))
		pgssCounterImages(s, q, 1, 2, 2, 2, 144)
		q = "UPDATE pgss_part.pgss_counter_parts SET k=$1::integer WHERE k=$2::integer RETURNING k,v"
		s.work(fixture.Select(q, []string{"k", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(12), "low"}}, int64(12), int64(1)))
		// Partition routing changes location, not one UPDATE into two events.
		pgssCounterImages(s, q, 1, 1, 1, 1, 144)
		s.both(fixture.Select("SELECT k,v FROM pgss_part.pgss_counter_parts ORDER BY k", []string{"k", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(11), "high"}, {int64(12), "low"}}))
		// A destination veto keeps the accepted source deletion. Nested
		// trigger work has its own image and must not consume the move's OLD.
		s.exec("CREATE TABLE pgss_part.pgss_counter_move_audit(k integer,v text)")
		s.exec("CREATE FUNCTION pgss_part.pgss_counter_move_veto() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN INSERT INTO pgss_part.pgss_counter_move_audit VALUES(NEW.k,NEW.v); RETURN NULL; END$$")
		s.exec("CREATE TRIGGER pgss_counter_move_veto BEFORE INSERT ON pgss_part.pgss_counter_low FOR EACH ROW EXECUTE FUNCTION pgss_part.pgss_counter_move_veto()")
		q = "UPDATE pgss_part.pgss_counter_parts AS vetoed_move SET k=$1::integer WHERE k=$2::integer RETURNING k,v"
		s.work(fixture.Select(q, []string{"k", "v"}, []string{"INT4", "TEXT"}, nil, int64(2), int64(11)))
		pgssCounterImages(s, q, 1, 0, 2, 2, 144) // OLD72 plus the nested audit INSERT72.
		s.both(fixture.Select("SELECT k,v FROM pgss_part.pgss_counter_parts", []string{"k", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(12), "low"}}))
		s.both(fixture.Select("SELECT k,v FROM pgss_part.pgss_counter_move_audit", []string{"k", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(2), "high"}}))
		s.exec("CREATE TABLE pgss_part.pgss_counter_merge(k integer,v text)")
		s.run(fixture.Exec("INSERT INTO pgss_part.pgss_counter_merge VALUES(1,'old')", 1))
		q = "MERGE INTO pgss_part.pgss_counter_merge t USING (VALUES($1::integer,$2::text),($3::integer,$4::text)) s(k,v) ON t.k=s.k WHEN MATCHED THEN UPDATE SET v=s.v WHEN NOT MATCHED THEN INSERT VALUES(s.k,s.v) RETURNING t.k,t.v"
		step = fixture.Select(q, []string{"k", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), nil}, {int64(2), "new"}}, int64(1), nil, int64(2), "new")
		step.Bag = true
		s.work(fixture.Prepared(step))
		pgssCounterImages(s, q, 1, 2, 2, 2, 184) //72+40 update;72 insert.
		s.both(fixture.Select("SELECT k,v FROM pgss_part.pgss_counter_merge ORDER BY k", []string{"k", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), nil}, {int64(2), "new"}}))
		s.exec("CREATE TABLE pgss_part.pgss_counter_cte_source(k integer,v text)")
		s.exec("CREATE TABLE pgss_part.pgss_counter_cte_target(k integer,v text)")
		s.run(fixture.Exec("INSERT INTO pgss_part.pgss_counter_cte_source VALUES(3,'move')", 1))
		q = "WITH moved AS (DELETE FROM pgss_part.pgss_counter_cte_source RETURNING k,v), copied AS (INSERT INTO pgss_part.pgss_counter_cte_target SELECT k,v FROM moved RETURNING k,v) SELECT a.k,a.v,b.v FROM copied a JOIN copied b ON a.k=b.k"
		s.work(fixture.Prepared(fixture.Select(q, []string{"k", "v", "v"}, []string{"INT4", "TEXT", "TEXT"}, [][]any{{int64(3), "move", "move"}})))
		pgssCounterImages(s, q, 1, 1, 2, 2, 144) // Materialized replay is not a second INSERT.
		s.both(fixture.Select("SELECT k,v FROM pgss_part.pgss_counter_cte_source", []string{"k", "v"}, []string{"INT4", "TEXT"}, nil))
		s.both(fixture.Select("SELECT k,v FROM pgss_part.pgss_counter_cte_target", []string{"k", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(3), "move"}}))
		s.exec("CREATE TABLE pgss_part.pgss_counter_upsert(k integer PRIMARY KEY,v text)")
		s.run(fixture.Exec("INSERT INTO pgss_part.pgss_counter_upsert VALUES(1,'old')", 1))
		q = "INSERT INTO pgss_part.pgss_counter_upsert VALUES($1::integer,$2::text) ON CONFLICT(k) DO UPDATE SET v=excluded.v RETURNING k,v"
		s.work(fixture.Prepared(fixture.Select(q, []string{"k", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), nil}}, int64(1), nil)))
		pgssCounterImages(s, q, 1, 1, 1, 1, 112) // Rejected INSERT candidate contributes no image.
		s.both(fixture.Select("SELECT k,v FROM pgss_part.pgss_counter_upsert", []string{"k", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), nil}}))
	})
}

// This family isolates accepted images from access-path choice. Read request
// magnitudes have their own source-owner witnesses; no guessed planner count is
// mixed into the independent per-event mutation arithmetic here.
func pgssCounterImages(s *pgssScenario, pattern string, calls, rows, dirty, records, bytes int64) {
	s.t.Helper()
	condition := "shared_blks_dirtied=$2::bigint AND wal_records=$3::bigint AND wal_bytes=$4::numeric"
	if s.b.PG {
		condition = "wal_records>0 AND wal_bytes>0 AND $2::bigint>=0 AND $3::bigint>0 AND $4::numeric>0"
	}
	q := "SELECT calls,rows," + condition + " AS images FROM pgss_part.pg_stat_statements WHERE query LIKE $1 AND toplevel AND userid=(SELECT oid FROM pg_roles WHERE rolname=current_user) AND dbid=(SELECT oid FROM pg_database WHERE datname=current_database())"
	s.both(fixture.Select(q, []string{"calls", "rows", "images"}, []string{"INT8", "INT8", "BOOL"}, [][]any{{calls, rows, true}}, pattern, dirty, records, bytes))
}
