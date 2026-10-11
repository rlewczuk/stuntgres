package reference_test

import (
	"database/sql"
	"fmt"
	"strconv"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceDefaultQueryPlanningDecisive(t *testing.T) {
	// Source-derived expectations from REL_18_0's constant AND/OR planning,
	// independently exercised on PostgreSQL before the native assertion.
	for _, tc := range []struct {
		expression string
		want       bool
	}{
		{"TRUE OR CAST('oops'::text AS INT)=1", true},
		{"FALSE AND CAST('oops'::text AS INT)=1", false},
		{"CAST(1 AS BOOL) OR CAST('oops'::text AS INT)=1", true},
		{"CAST(0 AS BOOL) AND CAST('oops'::text AS INT)=1", false},
		{"(NOT FALSE) OR CAST('oops'::text AS INT)=1", true},
		{"(1=1) OR CAST('oops'::text AS INT)=1", true},
		{"(NULL IS NULL) OR CAST('oops'::text AS INT)=1", true},
		{"(TRUE IS TRUE) OR CAST('oops'::text AS INT)=1", true},
		{"(1 IS NOT DISTINCT FROM 1) OR CAST('oops'::text AS INT)=1", true},
		{"(TRUE OR CAST('oops'::text AS INT)=1) OR CAST('oops'::text AS INT)=1", true},
	} {
		for _, prepared := range []bool{false, true} {
			for _, populated := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/prepared=%v/populated=%v", tc.expression, prepared, populated), func(t *testing.T) {
					p := fixture.NewScenario(t)
					nativeComplete := false
					defaultConsumerReferenceEach(t, p, func(t *testing.T, db *sql.DB, r *fixture.Recorder) {
						run := func(s fixture.Step) { s.Prepared = prepared; fixture.DefaultReferenceRunStep(t, p, db, r, s) }
						run(fixture.Exec("CREATE TABLE src(id INT)", 0))
						run(fixture.Exec("CREATE TABLE d(id INT,b BOOL DEFAULT ("+tc.expression+"))", 0))
						run(fixture.Exec("INSERT INTO d VALUES(0,NULL)", 1))
						count := int64(0)
						if populated {
							run(fixture.Exec("INSERT INTO src VALUES(1),(2)", 2))
							count = 2
						}
						run(fixture.Exec("INSERT INTO d(id,b) SELECT 9,NULL FROM src", count))
						run(fixture.Exec("INSERT INTO d(id) SELECT id FROM src ORDER BY id", count))
						want := [][]any{{int64(0), nil}}
						if populated {
							want = append(want, []any{int64(1), tc.want}, []any{int64(2), tc.want}, []any{int64(9), nil}, []any{int64(9), nil})
						}
						run(fixture.Select("SELECT * FROM d ORDER BY id", []string{"id", "b"}, []string{"INT4", "BOOL"}, want))
						if db == p.Memory {
							nativeComplete = true
						}
					})
					if !nativeComplete {
						return
					}
					t.Run("native-controls-and-replay", func(t *testing.T) {
						defaultConsumerReferenceRoundtrip(t, p, "'oops'", "d", "src")
						before := [][]any{{int64(0), nil}}
						if populated {
							before = append(before, []any{int64(1), tc.want}, []any{int64(2), tc.want}, []any{int64(9), nil}, []any{int64(9), nil})
						}
						check := fixture.Select("SELECT * FROM d ORDER BY id", []string{"id", "b"}, []string{"INT4", "BOOL"}, before)
						check.Prepared = prepared
						fixture.DefaultReferenceStep(t, p, check)
						s := fixture.Exec("INSERT INTO d(id) SELECT id FROM src ORDER BY id", 0)
						want := [][]any{{int64(0), nil}}
						if populated {
							s.Affected = 2
							want = [][]any{{int64(0), nil}, {int64(1), tc.want}, {int64(1), tc.want}, {int64(2), tc.want}, {int64(2), tc.want}, {int64(9), nil}, {int64(9), nil}}
						}
						s.Prepared = prepared
						p.Step(t, s)
						s = fixture.Select("SELECT * FROM d ORDER BY id", []string{"id", "b"}, []string{"INT4", "BOOL"}, want)
						s.Prepared = prepared
						fixture.DefaultReferenceStep(t, p, s)
					})
				})
			}
		}
	}
}

func TestReferenceDefaultQueryPlanningRequiredBranches(t *testing.T) {
	for _, expression := range []string{
		"NULL OR CAST('oops'::text AS INT)=1",
		"NULL AND CAST('oops'::text AS INT)=1",
		"FALSE OR CAST('oops'::text AS INT)=1",
		"TRUE AND CAST('oops'::text AS INT)=1",
		"CAST('oops'::text AS INT)=1 OR TRUE",
		"CAST('oops'::text AS INT)=1 AND FALSE",
		"(TRUE OR CAST('discarded'::text AS INT)=1) AND CAST('oops'::text AS INT)=1",
	} {
		for _, prepared := range []bool{false, true} {
			for _, populated := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/prepared=%v/populated=%v", expression, prepared, populated), func(t *testing.T) {
					p := fixture.NewScenario(t)
					defaultConsumerReferenceEach(t, p, func(t *testing.T, db *sql.DB, r *fixture.Recorder) {
						run := func(s fixture.Step) { s.Prepared = prepared; fixture.DefaultReferenceRunStep(t, p, db, r, s) }
						run(fixture.Exec("CREATE TABLE src(id INT)", 0))
						run(fixture.Exec("CREATE TABLE d(id INT,b BOOL DEFAULT ("+expression+"))", 0))
						run(fixture.Exec("INSERT INTO d VALUES(0,TRUE)", 1))
						if populated {
							run(fixture.Exec("INSERT INTO src VALUES(1),(2)", 2))
						}
						s := fixture.ImplicitError("INSERT INTO d(id) SELECT id FROM src ORDER BY id", fixture.Diagnostic{Code: "22P02", Message: `invalid input syntax for type integer: "oops"`})
						s.ErrorStage = "execute"
						run(s)
						run(fixture.Select("SELECT * FROM d ORDER BY id", []string{"id", "b"}, []string{"INT4", "BOOL"}, [][]any{{int64(0), true}}))
					})
				})
			}
		}
	}
}

func TestReferenceDefaultQueryPlanningAnalysis(t *testing.T) {
	for _, tc := range []struct{ expression, token, code, message string }{
		{"TRUE OR CAST($1 AS INT)=1", "$1", "42P02", "there is no parameter $1"},
		{"FALSE AND CAST(b AS INT)=1", "b AS", "0A000", "cannot use column reference in DEFAULT expression"},
		{"TRUE OR CAST('oops' AS INT)=1", "'oops'", "22P02", `invalid input syntax for type integer: "oops"`},
	} {
		for _, prepared := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/prepared=%v", tc.expression, prepared), func(t *testing.T) {
				p := fixture.NewScenario(t)
				defaultConsumerReferenceEach(t, p, func(t *testing.T, db *sql.DB, r *fixture.Recorder) {
					q := "CREATE TABLE bad(b BOOL DEFAULT (" + tc.expression + "))"
					s := fixture.ImplicitError(q, fixture.Diagnostic{Code: tc.code, Message: tc.message, Position: strconv.Itoa(strings.Index(q, tc.token) + 1)})
					s.Prepared, s.ErrorStage = prepared, "execute"
					fixture.RunStep(t, p.Ctx, db, r, s)
					fixture.RunStep(t, p.Ctx, db, r, fixture.ImplicitError("SELECT * FROM bad", fixture.Diagnostic{Code: "42P01", Message: `relation "bad" does not exist`, Position: "15"}))
				})
			})
		}
	}
}
