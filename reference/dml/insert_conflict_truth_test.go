package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceInsertConflictNestedTruthTests(t *testing.T) {
	for _, predicate := range []string{
		"NOT (active IS TRUE)",
		"NOT (active IS FALSE)",
		"(active IS TRUE) IS NOT NULL",
		"(active IS FALSE) IS NOT NULL",
		"(active IS TRUE)=FALSE",
		"NOT ((active IS TRUE) IS NULL)",
		"NOT ((active IS FALSE) IS NULL)",
		"((active IS TRUE) IS FALSE)",
	} {
		t.Run("negative/"+predicate, func(t *testing.T) {
			query := "INSERT INTO t VALUES(4,FALSE),(2,FALSE) ON CONFLICT(k) WHERE " + predicate + " DO NOTHING"
			insertConflictSteps(t,
				fixture.Exec("CREATE TABLE t(k INT,active BOOL)", 0),
				fixture.Exec("CREATE UNIQUE INDEX ui ON t(k) WHERE active IS NOT NULL", 0),
				fixture.Exec("INSERT INTO t VALUES(1,TRUE),(2,FALSE),(3,NULL)", 3),
				insertConflictNoMatch(query),
				fixture.Select("SELECT k,active FROM t ORDER BY k", []string{"k", "active"}, []string{"INT4", "BOOL"}, [][]any{{int64(1), true}, {int64(2), false}, {int64(3), nil}}))
		})
	}
	// These implications are mathematically valid, but PostgreSQL 18's released
	// proof rules reject them. Value-preserving Boolean normalization must not
	// widen inference beyond the observed contract.
	for _, c := range []struct{ index, target string }{
		{"active IS NOT NULL", "NOT (active IS NOT TRUE)"},
		{"active IS NOT NULL", "NOT (active IS NOT FALSE)"},
		{"active", "active IS TRUE"},
		{"NOT active", "active IS FALSE"},
	} {
		t.Run("release-negative/"+c.index+"/"+c.target, func(t *testing.T) {
			insertConflictSteps(t,
				fixture.Exec("CREATE TABLE t(k INT,active BOOL)", 0),
				fixture.Exec("CREATE UNIQUE INDEX ui ON t(k) WHERE "+c.index, 0),
				fixture.Exec("INSERT INTO t VALUES(1,TRUE),(2,FALSE),(3,NULL)", 3),
				insertConflictNoMatch("INSERT INTO t VALUES(1,TRUE),(2,FALSE),(3,NULL),(4,NULL),(4,NULL) ON CONFLICT(k) WHERE "+c.target+" DO NOTHING"),
				fixture.Select("SELECT k,active FROM t ORDER BY k", []string{"k", "active"}, []string{"INT4", "BOOL"}, [][]any{{int64(1), true}, {int64(2), false}, {int64(3), nil}}))
		})
	}
	for _, c := range []struct {
		index, target string
		count         int64
		rows          [][]any
	}{
		{"NOT (active IS TRUE)", "NOT (active IS TRUE)", 2, [][]any{{int64(1), true}, {int64(1), true}, {int64(2), false}, {int64(3), nil}, {int64(4), nil}}},
		{"(active IS TRUE) IS NOT NULL", "(active IS TRUE) IS NOT NULL", 1, [][]any{{int64(1), true}, {int64(2), false}, {int64(3), nil}, {int64(4), nil}}},
		{"((active IS TRUE) IS FALSE)", "((active IS TRUE) IS FALSE)", 2, [][]any{{int64(1), true}, {int64(1), true}, {int64(2), false}, {int64(3), nil}, {int64(4), nil}}},
	} {
		t.Run("positive/"+c.index+"/"+c.target, func(t *testing.T) {
			insertConflictSteps(t,
				fixture.Exec("CREATE TABLE t(k INT,active BOOL)", 0),
				fixture.Exec("CREATE UNIQUE INDEX ui ON t(k) WHERE "+c.index, 0),
				fixture.Exec("INSERT INTO t VALUES(1,TRUE),(2,FALSE),(3,NULL)", 3),
				fixture.Exec("INSERT INTO t VALUES(1,TRUE),(2,FALSE),(3,NULL),(4,NULL),(4,NULL) ON CONFLICT(k) WHERE "+c.target+" DO NOTHING", c.count),
				fixture.Select("SELECT k,active FROM t ORDER BY k", []string{"k", "active"}, []string{"INT4", "BOOL"}, c.rows))
		})
	}
	// Truth tests are nonnullable values under their parents, not interchangeable
	// with a nullable Boolean qualification. Literal rows expose the counterexample.
	insertConflictSteps(t,
		fixture.Exec("CREATE TABLE truth_values(k INT,active BOOL)", 0),
		fixture.Exec("INSERT INTO truth_values VALUES(1,TRUE),(2,FALSE),(3,NULL)", 3),
		fixture.Select("SELECT active,NOT (active IS TRUE) AS nt,NOT (active IS FALSE) AS nf,(active IS TRUE) IS NOT NULL AS nn FROM truth_values ORDER BY k", []string{"active", "nt", "nf", "nn"}, []string{"BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{true, false, true, true}, {false, true, false, true}, {nil, true, true, true}}))
}
