package reference_test

import (
	"database/sql"
	"reflect"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

const pgssResetSignature = "pgss_part.pg_stat_statements_reset(oid,oid,bigint,boolean)"
const pgssResetCall = "SELECT pgss_part.pg_stat_statements_reset()"

// Read actual public values, including every physical counter, rather than
// reconstructing an expected snapshot from the implementation under test.
func pgssResetSnapshot(s *pgssScenario) [][]any {
	s.t.Helper()
	return pgssResetRead(s, "SELECT * FROM pgss_part.pg_stat_statements ORDER BY userid,dbid,queryid,toplevel")
}

func pgssResetRead(s *pgssScenario, q string, args ...any) [][]any {
	s.t.Helper()
	rows, err := s.observer.QueryContext(s.p.Ctx, q, args...)
	if err != nil {
		s.t.Fatal(q, err)
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil {
		s.t.Fatal(err)
	}
	var result [][]any
	for rows.Next() {
		values := make([]any, len(columns))
		pointers := make([]any, len(columns))
		for i := range values {
			pointers[i] = &values[i]
		}
		if err := rows.Scan(pointers...); err != nil {
			s.t.Fatal(err)
		}
		for i := range values {
			values[i] = fixture.Normalize(values[i])
		}
		result = append(result, values)
	}
	if err := rows.Err(); err != nil {
		s.t.Fatal(err)
	}
	return result
}

func pgssResetEqual(t *testing.T, got, want any) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("reset state changed unexpectedly:\ngot  %#v\nwant %#v", got, want)
	}
}

func pgssResetInfo(s *pgssScenario) [][]any {
	s.t.Helper()
	return pgssResetRead(s, "SELECT dealloc,stats_reset FROM pgss_part.pg_stat_statements_info")
}

// The result is scalar, not SETOF. Assert the public descriptor and exactly one
// row, including the STRICT NULL case. Never compare server clock instants.
func pgssResetScalar(s *pgssScenario, x fixture.Executor, q string, null bool, args ...any) time.Time {
	s.t.Helper()
	var before, after time.Time
	if err := s.observer.QueryRowContext(s.p.Ctx, "SELECT clock_timestamp()").Scan(&before); err != nil {
		s.t.Fatal(err)
	}
	rows, err := x.QueryContext(s.p.Ctx, q, args...)
	if err != nil {
		s.t.Fatal(q, err)
	}
	defer rows.Close()
	columns, err := rows.ColumnTypes()
	if err != nil {
		s.t.Fatal(err)
	}
	if len(columns) != 1 || columns[0].Name() != "pg_stat_statements_reset" || columns[0].DatabaseTypeName() != "TIMESTAMPTZ" {
		s.t.Fatalf("reset result metadata: %#v", columns)
	}
	if !rows.Next() {
		s.t.Fatalf("reset missing scalar row: %v", rows.Err())
	}
	var value sql.NullTime
	if err := rows.Scan(&value); err != nil {
		s.t.Fatal(err)
	}
	if rows.Next() || rows.Err() != nil {
		s.t.Fatalf("reset must produce exactly one row: %v", rows.Err())
	}
	if err := rows.Close(); err != nil {
		s.t.Fatal(err)
	}
	if value.Valid == null {
		s.t.Fatalf("reset timestamp valid=%v, want NULL=%v", value.Valid, null)
	}
	if err := s.observer.QueryRowContext(s.p.Ctx, "SELECT clock_timestamp()").Scan(&after); err != nil {
		s.t.Fatal(err)
	}
	if value.Valid && (value.Time.Before(before) || value.Time.After(after) || value.Time.Nanosecond()%1000 != 0) {
		s.t.Fatalf("reset instant %v outside [%v,%v] or not microsecond precision", value.Time, before, after)
	}
	return value.Time
}

func pgssResetHeld(s *pgssScenario, q string) fixture.HeldStatement {
	s.t.Helper()
	stmt, err := s.observer.PrepareContext(s.p.Ctx, q)
	if err != nil {
		s.t.Fatal(q, err)
	}
	s.t.Cleanup(func() { stmt.Close() })
	return fixture.HeldStatement{Executor: s.observer, Statement: stmt, SQL: q}
}

func pgssResetSeed(s *pgssScenario) (string, string) {
	s.t.Helper()
	s.exec("CREATE TABLE pgss_part.pgss_reset_data(k integer)")
	s.run(fixture.Exec("INSERT INTO pgss_part.pgss_reset_data VALUES(1),(2)", 2))
	a := "SELECT k AS pgss_reset_a FROM pgss_part.pgss_reset_data pgss_reset_a WHERE k<=$1"
	b := "SELECT k AS pgss_reset_b FROM pgss_part.pgss_reset_data pgss_reset_b WHERE k=$1"
	s.work(fixture.Select(a, []string{"pgss_reset_a"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}, int64(2)))
	s.work(fixture.Prepared(fixture.Select(a, []string{"pgss_reset_a"}, []string{"INT4"}, [][]any{{int64(1)}}, int64(1))))
	s.work(fixture.Select(b, []string{"pgss_reset_b"}, []string{"INT4"}, [][]any{{int64(1)}}, int64(1)))
	return a, b
}

func pgssResetIdentity(s *pgssScenario, q string) (int64, int64, int64) {
	s.t.Helper()
	var user, db, query int64
	if err := s.observer.QueryRowContext(s.p.Ctx, "SELECT userid::bigint,dbid::bigint,queryid FROM pgss_part.pg_stat_statements WHERE query=$1 AND toplevel AND userid=(SELECT oid FROM pg_roles WHERE rolname=current_user) AND dbid=(SELECT oid FROM pg_database WHERE datname=current_database())", q).Scan(&user, &db, &query); err != nil {
		s.t.Fatal(q, err)
	}
	if user == 0 || db == 0 || query == 0 {
		s.t.Fatal("zero measured identity")
	}
	return user, db, query
}

func pgssResetAssertMinmax(s *pgssScenario, before, after [][]any, selected func([]any) bool, at time.Time) {
	s.t.Helper()
	if len(before) != len(after) {
		s.t.Fatalf("minmax removed entries: %d -> %d", len(before), len(after))
	}
	for i, old := range before {
		want := append([]any(nil), old...)
		if selected(old) {
			for _, column := range []int{7, 8, 13, 14} {
				want[column] = float64(0)
			}
			want[51] = fixture.Normalize(at)
		}
		pgssResetEqual(s.t, after[i], want)
	}
}
