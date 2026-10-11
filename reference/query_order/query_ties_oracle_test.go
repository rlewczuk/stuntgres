package reference_test

import (
	"strings"
	"stuntgres/reference/fixture"
	"testing"
	"unicode/utf8"
)

func TestReferenceQueryTiesOracleQuestions(t *testing.T) {
	for _, shape := range []string{"NULL", "(NULL)", "(NULL+1)", "NULL::bigint", "(NULL::bigint)"} {
		for _, view := range []bool{false, true} {
			t.Run(shape+map[bool]string{false: "/select", true: "/view"}[view], func(t *testing.T) {
				referenceTiesScenario(t, func(p *fixture.Pair, b fixture.Backend) {
					referenceTiesSetup(t, p, b)
					q := "SELECT k FROM ties_t ORDER BY k FETCH FIRST " + shape + " ROWS WITH TIES"
					if view {
						q = "CREATE VIEW ties_null_v AS " + q
					}
					if shape == "NULL::bigint" {
						s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "42601", Message: "syntax error at or near \"::\"", Position: fixture.Position(strings.Index(q, "::") + 1)})
						s.SelectRows = !view
						s.ErrorStage = "execute"
						fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
						s.ErrorStage = "prepare"
						fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Prepared(s))
					} else if shape == "NULL" || shape == "(NULL)" {
						s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "2201W", Message: "row count cannot be null in FETCH FIRST ... WITH TIES clause"})
						s.SelectRows = !view
						s.ErrorStage = "execute"
						fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
						// SELECT analysis occurs at Prepare; view utility body
						// semantics are checked only when the command executes.
						if !view {
							s.ErrorStage = "prepare"
						}
						fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Prepared(s))
					} else if view {
						fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec(q, 0))
						referenceTiesBoth(t, p, b, fixture.Select("SELECT k FROM ties_null_v ORDER BY k", []string{"k"}, []string{"INT4"}, referenceLimitIDs(1, 2, 2, 3)))
						fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("DROP VIEW ties_null_v", 0))
						fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Prepared(fixture.Exec(q, 0)))
						referenceTiesBoth(t, p, b, fixture.Select("SELECT k FROM ties_null_v ORDER BY k", []string{"k"}, []string{"INT4"}, referenceLimitIDs(1, 2, 2, 3)))
					} else {
						referenceTiesBoth(t, p, b, fixture.Select(q, []string{"k"}, []string{"INT4"}, referenceLimitIDs(1, 2, 2, 3)))
					}
				})
			})
		}
	}
	t.Run("nil input normal and empty metadata", func(t *testing.T) {
		referenceTiesScenario(t, func(p *fixture.Pair, b fixture.Backend) {
			referenceTiesSetup(t, p, b)
			for _, pred := range []string{"", " WHERE FALSE"} {
				keys := []int64{1, 2, 2, 3}
				if pred != "" {
					keys = nil
				}
				referenceTiesBoth(t, p, b, fixture.Select("SELECT k FROM ties_t"+pred+" ORDER BY k FETCH FIRST $1 ROWS WITH TIES", []string{"k"}, []string{"INT4"}, referenceLimitIDs(keys...), nil))
			}
		})
	})
	for _, q := range []string{
		"SELECT k FROM ties_t FETCH FIRST 2 ROWS WITH TIES",
		"SELECT /*é*/ k FROM ties_t FETCH FIRST 2 ROWS WITH TIES",
		"SELECT k FROM (SELECT k FROM ties_t ORDER BY k) AS ordered FETCH FIRST 2 ROWS WITH TIES",
		"SELECT k FROM missing_ties_source FETCH FIRST TRUE ROWS WITH TIES",
		"SELECT k FROM missing_ties_source FETCH FIRST NULL ROWS WITH TIES",
		"SELECT k FROM ties_t FETCH FIRST 2 ROWS WITH TIES FOR UPDATE SKIP LOCKED",
		"SELECT k FROM ties_t ORDER BY k FETCH FIRST 2 ROWS WITH TIES FOR UPDATE SKIP LOCKED",
		"SELECT k FROM ties_t ORDER BY k FOR UPDATE SKIP LOCKED FETCH FIRST 2 ROWS WITH TIES",
		"SELECT k FROM ties_t ORDER BY k FOR UPDATE SKIP LOCKED FOR UPDATE NOWAIT FETCH FIRST 2 ROWS WITH TIES",
		"SELECT k FROM ties_t ORDER BY k FOR UPDATE NOWAIT FOR UPDATE SKIP LOCKED FETCH FIRST 2 ROWS WITH TIES",
		"SELECT k FROM ties_t ORDER BY k FOR UPDATE NOWAIT FETCH FIRST 2 ROWS WITH TIES FOR UPDATE SKIP LOCKED",
	} {
		t.Run(q, func(t *testing.T) {
			referenceTiesScenario(t, func(p *fixture.Pair, b fixture.Backend) {
				referenceTiesSetup(t, p, b)
				message := "WITH TIES cannot be specified without ORDER BY clause"
				if strings.Contains(q, "ORDER BY k FOR UPDATE") || strings.Contains(q, "ties_t ORDER BY k FETCH") {
					message = "SKIP LOCKED and WITH TIES options cannot be used together"
				}
				position := utf8.RuneCountInString(q[:strings.Index(q, "WITH TIES")]) + 1
				if q == "SELECT k FROM ties_t ORDER BY k FOR UPDATE NOWAIT FETCH FIRST 2 ROWS WITH TIES FOR UPDATE SKIP LOCKED" {
					message = "syntax error at or near \"FOR\""
					position = utf8.RuneCountInString(q[:strings.LastIndex(q, "FOR UPDATE")]) + 1
				}
				s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "42601", Message: message, Position: fixture.Position(position)})
				s.SelectRows = true
				s.ErrorStage = "execute"
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
				s.ErrorStage = "prepare"
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Prepared(s))
			})
		})
	}
	for _, q := range []string{
		"(SELECT k FROM ties_t ORDER BY k) FETCH FIRST 2 ROWS WITH TIES",
		"(SELECT k FROM ties_t) ORDER BY k FETCH FIRST 2 ROWS WITH TIES",
		"SELECT k FROM ties_t ORDER BY k FETCH FIRST 2 ROWS WITH TIES FOR UPDATE",
		"SELECT k FROM ties_t ORDER BY k FOR UPDATE NOWAIT FETCH FIRST 2 ROWS WITH TIES",
	} {
		t.Run(q, func(t *testing.T) {
			referenceTiesScenario(t, func(p *fixture.Pair, b fixture.Backend) {
				referenceTiesSetup(t, p, b)
				referenceTiesBoth(t, p, b, fixture.Select(q, []string{"k"}, []string{"INT4"}, referenceLimitIDs(1, 2, 2)))
			})
		})
	}
	for _, tc := range []struct {
		bound, code, message string
		runtime              bool
	}{
		{"TRUE", "42804", "argument of LIMIT must be type bigint, not type boolean", false},
		{"k", "42P10", "argument of LIMIT must not contain variables", false},
		{"count(*)", "42803", "aggregate functions are not allowed in LIMIT", false},
		{"-1", "2201W", "LIMIT must not be negative", true},
	} {
		t.Run("bound/"+tc.bound, func(t *testing.T) {
			referenceTiesScenario(t, func(p *fixture.Pair, b fixture.Backend) {
				referenceTiesSetup(t, p, b)
				prefix := "SELECT k FROM ties_t ORDER BY k FETCH FIRST "
				q := prefix + tc.bound + " ROWS WITH TIES"
				position := ""
				if !tc.runtime {
					position = fixture.Position(len(prefix) + 1)
				}
				s := fixture.ImplicitError(q, fixture.Diagnostic{Code: tc.code, Message: tc.message, Position: position})
				s.SelectRows = true
				s.ErrorStage = "execute"
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
				if !tc.runtime {
					s.ErrorStage = "prepare"
				}
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Prepared(s))
			})
		})
	}
	t.Run("ordered view is not local ORDER", func(t *testing.T) {
		referenceTiesScenario(t, func(p *fixture.Pair, b fixture.Backend) {
			referenceTiesSetup(t, p, b)
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE VIEW ties_ordered AS SELECT k FROM ties_t ORDER BY k", 0))
			q := "SELECT k FROM ties_ordered FETCH FIRST 2 ROWS WITH TIES"
			s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "42601", Message: "WITH TIES cannot be specified without ORDER BY clause", Position: fixture.Position(strings.Index(q, "WITH TIES") + 1)})
			s.SelectRows = true
			s.ErrorStage = "execute"
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
			s.ErrorStage = "prepare"
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Prepared(s))
		})
	})
}

func TestReferenceQueryTiesDemand(t *testing.T) {
	for _, tc := range []struct {
		suffix string
		values []int64
		next   int64
	}{
		{"FETCH FIRST 2 ROWS WITH TIES", []int64{1, 2, 3}, 5},
		{"FETCH FIRST ROW WITH TIES", []int64{1}, 3},
		{"FETCH FIRST 0 ROWS WITH TIES", nil, 1},
		{"OFFSET 1 FETCH FIRST ROW WITH TIES", []int64{2, 3}, 5},
		{"OFFSET 2 FETCH FIRST ROW WITH TIES", []int64{3}, 5},
		{"FETCH FIRST 4 ROWS WITH TIES", []int64{1, 2, 3, 4}, 5},
	} {
		for _, prepared := range []bool{false, true} {
			t.Run(tc.suffix+map[bool]string{false: "/direct", true: "/prepared"}[prepared], func(t *testing.T) {
				referenceTiesScenario(t, func(p *fixture.Pair, b fixture.Backend) {
					referenceTiesSetup(t, p, b)
					fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE SEQUENCE ties_s START WITH 1", 0))
					s := fixture.Select("SELECT nextval('ties_s') AS n FROM ties_t ORDER BY k "+tc.suffix, []string{"n"}, []string{"INT8"}, referenceLimitIDs(tc.values...))
					if prepared {
						s = fixture.Prepared(s)
					}
					fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
					fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Select("SELECT nextval('ties_s') AS n", []string{"n"}, []string{"INT8"}, referenceLimitIDs(tc.next)))
				})
			})
		}
	}
	t.Run("shared volatile order cache", func(t *testing.T) {
		referenceTiesScenario(t, func(p *fixture.Pair, b fixture.Backend) {
			referenceTiesSetup(t, p, b)
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE SEQUENCE ties_s START WITH 1", 0))
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Select("SELECT nextval('ties_s')%2 AS n FROM ties_t ORDER BY n,n FETCH FIRST ROW WITH TIES", []string{"n"}, []string{"INT8"}, referenceLimitIDs(0, 0)))
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Select("SELECT nextval('ties_s') AS n", []string{"n"}, []string{"INT8"}, referenceLimitIDs(5)))
		})
	})
	t.Run("all peers EOF", func(t *testing.T) {
		referenceTiesScenario(t, func(p *fixture.Pair, b fixture.Backend) {
			referenceTiesSetup(t, p, b)
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("UPDATE ties_t SET k=2 WHERE id<4", 3))
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE SEQUENCE ties_s START WITH 1", 0))
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Select("SELECT nextval('ties_s') AS n FROM ties_t WHERE id<4 ORDER BY k FETCH FIRST ROW WITH TIES", []string{"n"}, []string{"INT8"}, referenceLimitIDs(1, 2, 3)))
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Select("SELECT nextval('ties_s') AS n", []string{"n"}, []string{"INT8"}, referenceLimitIDs(4)))
		})
	})
}
