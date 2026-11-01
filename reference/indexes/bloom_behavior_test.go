package reference_test

import (
	"database/sql"
	"fmt"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func bloomEqualityChecks(t *testing.T, p *fixture.Pair, b fixture.Backend, c fixture.Executor, table string, held fixture.HeldStatement) {
	t.Helper()
	run := bloomRun(t, p, b, c)
	for _, tc := range []struct {
		predicate string
		want      [][]any
	}{
		{"i=7 AND s='a'", [][]any{{int64(7), "a"}, {int64(7), "a"}}},
		{"'a'=s AND 7=i", [][]any{{int64(7), "a"}, {int64(7), "a"}}},
		{"i=7", [][]any{{int64(7), "a"}, {int64(7), "a"}, {int64(7), nil}}},
		{"s='a'", [][]any{{int64(7), "a"}, {int64(7), "a"}, {nil, "a"}, {int64(8), "a"}}},
		{"i=NULL", nil},
		{"i=99 AND s='missing'", nil},
		{"i=7 AND s IS NULL", [][]any{{int64(7), nil}}},
		{"i=7 AND s='a' AND i>6", [][]any{{int64(7), "a"}, {int64(7), "a"}}},
	} {
		bloomBoth(run, bloomSelect(b, "SELECT i,s FROM "+table+" WHERE "+tc.predicate, tc.want))
	}
	for _, tc := range []struct {
		arg  any
		want [][]any
	}{
		{7, [][]any{{int64(7), "a"}, {int64(7), "a"}, {int64(7), nil}}},
		{8, [][]any{{int64(8), "b"}, {int64(8), "a"}}},
		{nil, nil}, {99, nil},
		{7, [][]any{{int64(7), "a"}, {int64(7), "a"}, {int64(7), nil}}},
	} {
		fixture.RunStep(t, p.Ctx, held, b.Recorder, bloomSelect(b, held.SQL, tc.want, tc.arg))
	}
	q := "SELECT i,s FROM " + table + " WHERE i=$1 AND s=$2"
	multi := bloomHeld(t, p, c, q)
	for _, tc := range []struct {
		i, s any
		want [][]any
	}{{7, "a", [][]any{{int64(7), "a"}, {int64(7), "a"}}}, {8, "b", [][]any{{int64(8), "b"}}}, {7, nil, nil}, {nil, "a", nil}, {99, "a", nil}} {
		s := bloomSelect(b, q, tc.want, tc.i, tc.s)
		run(s)
		fixture.RunStep(t, p.Ctx, multi, b.Recorder, s)
	}
}

func TestReferenceBloomEquality(t *testing.T) {
	bloomScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := bloomRun(t, p, b, c)
		run(fixture.Exec("CREATE EXTENSION bloom", 0))
		bloomSeed(run, "bloom_t")
		held := bloomHeld(t, p, c, "SELECT i,s FROM bloom_t WHERE i=$1")
		// Each phase asserts the same independently specified bag, not the
		// results of the preceding scan. Held parameters cross both DDLs.
		bloomEqualityChecks(t, p, b, c, "bloom_t", held)
		for _, options := range []string{"", " WITH(length=1,col1=1,col2=1,col32=4095)"} {
			run(fixture.Exec("CREATE INDEX bloom_eq ON bloom_t USING bloom(i,s)"+options, 0))
			bloomEqualityChecks(t, p, b, c, "bloom_t", held)
			run(fixture.Exec("SET enable_seqscan=off", 0))
			bloomEqualityChecks(t, p, b, c, "bloom_t", held)
			run(fixture.Exec("RESET enable_seqscan", 0))
			run(fixture.Exec("SET enable_indexscan=off", 0))
			bloomEqualityChecks(t, p, b, c, "bloom_t", held)
			run(fixture.Exec("RESET enable_indexscan", 0))
			run(fixture.Exec("DROP INDEX bloom_eq", 0))
		}
		// Binding succeeds with no tuples and with only NULL signatures.
		run(fixture.Exec("CREATE TABLE bloom_empty(i integer,s text)", 0))
		run(fixture.Exec("CREATE INDEX bloom_empty_idx ON bloom_empty USING bloom(i,s)", 0))
		bloomBoth(run, bloomSelect(b, "SELECT i,s FROM bloom_empty WHERE i=7", nil))
		run(fixture.Exec("INSERT INTO bloom_empty VALUES(NULL,NULL)", 1))
		bloomBoth(run, bloomSelect(b, "SELECT i,s FROM bloom_empty WHERE s='a'", nil))
		bloomBoth(run, bloomSelect(b, "SELECT i,s FROM bloom_empty WHERE i IS NULL", [][]any{{nil, nil}}))
	})
}

func TestReferenceBloomFallback(t *testing.T) {
	bloomScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := bloomRun(t, p, b, c)
		run(fixture.Exec("CREATE EXTENSION bloom", 0))
		bloomSeed(run, "bloom_fallback")
		run(fixture.Exec("CREATE INDEX bloom_fallback_idx ON bloom_fallback USING bloom(i,s) WITH(length=1,col1=1,col2=1)", 0))
		for _, preference := range []string{"RESET enable_seqscan", "SET enable_seqscan=off"} {
			run(fixture.Exec(preference, 0))
			for _, tc := range []struct {
				predicate string
				want      [][]any
			}{
				{"i=7 OR s='a'", [][]any{{int64(7), "a"}, {int64(7), "a"}, {int64(7), nil}, {nil, "a"}, {int64(8), "a"}}},
				{"i>=8", [][]any{{int64(8), "b"}, {int64(8), "a"}}},
				{"i IS NULL", [][]any{{nil, "a"}, {nil, nil}}},
				{"s IS NOT NULL", [][]any{{int64(7), "a"}, {int64(7), "a"}, {nil, "a"}, {int64(8), "b"}, {int64(8), "a"}}},
				{"NOT(i=7)", [][]any{{int64(8), "b"}, {int64(8), "a"}}},
				{"i=7 AND (s='a' OR s IS NULL)", [][]any{{int64(7), "a"}, {int64(7), "a"}, {int64(7), nil}}},
				{"s='a' AND i>=8", [][]any{{int64(8), "a"}}},
			} {
				bloomBoth(run, bloomSelect(b, "SELECT i,s FROM bloom_fallback WHERE "+tc.predicate, tc.want))
			}
			s := bloomSelect(b, "SELECT i,s FROM bloom_fallback ORDER BY i NULLS LAST,s NULLS LAST", [][]any{{int64(7), "a"}, {int64(7), "a"}, {int64(7), nil}, {int64(8), "a"}, {int64(8), "b"}, {nil, "a"}, {nil, nil}})
			s.Bag = false
			bloomBoth(run, s)
			bloomBoth(run, bloomSelect(b, "SELECT i,s FROM bloom_fallback WHERE i=$1 OR s=$2", [][]any{{int64(8), "b"}, {int64(8), "a"}, {int64(7), "a"}, {int64(7), "a"}, {nil, "a"}}, 8, "a"))
		}
		run(fixture.Exec("RESET enable_seqscan", 0))
	})
}

// With 16 effective bits and one bit per int4, 17 distinct non-NULL values
// guarantee a genuine collision. Public results must never expose its extra
// candidate; the owning internal proof separately observes candidate consumption.
func bloomCollisionChecks(t *testing.T, p *fixture.Pair, b fixture.Backend, c fixture.Executor, table string) {
	t.Helper()
	run := bloomRun(t, p, b, c)
	q := "SELECT i FROM " + table + " WHERE i=$1"
	held := bloomHeld(t, p, c, q)
	for _, preference := range []string{"RESET enable_seqscan", "SET enable_seqscan=off"} {
		run(fixture.Exec(preference, 0))
		for i := 0; i <= 16; i++ {
			want := [][]any{{int64(i)}}
			if i == 0 {
				want = append(want, []any{int64(0)})
			}
			s := fixture.Select(q, []string{"i"}, []string{"INT4"}, want, i)
			s.Bag = true
			s.CheckMetadata = func(t *testing.T, rows *sql.Rows) { fixture.DriverBasicMetadata(t, rows, b.PG, []string{"INT4"}, []bool{true}) }
			fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
			s.Query, s.Args = fmt.Sprintf("SELECT i FROM %s WHERE i=%d", table, i), nil
			run(s)
		}
		for _, arg := range []any{nil, 99} {
			s := fixture.Select(q, []string{"i"}, []string{"INT4"}, nil, arg)
			s.CheckMetadata = func(t *testing.T, rows *sql.Rows) { fixture.DriverBasicMetadata(t, rows, b.PG, []string{"INT4"}, []bool{true}) }
			fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
		}
	}
	run(fixture.Exec("RESET enable_seqscan", 0))
}

func bloomCollisionSeed(run func(fixture.Step), table string) {
	run(fixture.Exec("CREATE TABLE "+table+"(i integer)", 0))
	values := make([]string, 0, 19)
	for i := 0; i <= 16; i++ {
		values = append(values, fmt.Sprintf("(%d)", i))
	}
	values = append(values, "(0)", "(NULL)")
	run(fixture.Exec("INSERT INTO "+table+" VALUES"+strings.Join(values, ","), 19))
	run(fixture.Exec("CREATE INDEX bloom_collision_idx ON "+table+" USING bloom(i) WITH(length=1,col1=1)", 0))
}

func TestReferenceBloomCollision(t *testing.T) {
	bloomScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := bloomRun(t, p, b, c)
		run(fixture.Exec("CREATE EXTENSION bloom", 0))
		bloomCollisionSeed(run, "bloom_collision")
		bloomCollisionChecks(t, p, b, c, "bloom_collision")
	})
}
