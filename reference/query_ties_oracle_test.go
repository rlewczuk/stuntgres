package reference_test

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestReferenceQueryTiesOracleQuestions(t *testing.T) {
	for _, shape := range []string{"NULL", "(NULL)", "(NULL+1)", "NULL::bigint", "(NULL::bigint)"} {
		for _, view := range []bool{false, true} {
			t.Run(shape+map[bool]string{false: "/select", true: "/view"}[view], func(t *testing.T) {
				referenceTiesScenario(t, func(p *referencePair, b referenceBackend) {
					referenceTiesSetup(t, p, b)
					q := "SELECT k FROM ties_t ORDER BY k FETCH FIRST " + shape + " ROWS WITH TIES"
					if view {
						q = "CREATE VIEW ties_null_v AS " + q
					}
					if shape == "NULL::bigint" {
						s := referenceImplicitError(q, referenceDiagnostic{Code: "42601", Message: "syntax error at or near \"::\"", Position: referencePosition(strings.Index(q, "::") + 1)})
						s.selectRows = !view
						s.errorStage = "execute"
						referenceRunStep(t, p.ctx, b.db, b.recorder, s)
						s.errorStage = "prepare"
						referenceRunStep(t, p.ctx, b.db, b.recorder, referencePrepared(s))
					} else if shape == "NULL" || shape == "(NULL)" {
						s := referenceImplicitError(q, referenceDiagnostic{Code: "2201W", Message: "row count cannot be null in FETCH FIRST ... WITH TIES clause"})
						s.selectRows = !view
						s.errorStage = "execute"
						referenceRunStep(t, p.ctx, b.db, b.recorder, s)
						// Native utility Prepare eagerly analyzes CREATE VIEW;
						// PostgreSQL defers this utility analysis to Execute.
						if !view || !b.pg {
							s.errorStage = "prepare"
						}
						referenceRunStep(t, p.ctx, b.db, b.recorder, referencePrepared(s))
					} else if view {
						referenceRunStep(t, p.ctx, b.db, b.recorder, referenceExec(q, 0))
						referenceTiesBoth(t, p, b, referenceSelect("SELECT k FROM ties_null_v ORDER BY k", []string{"k"}, []string{"INT4"}, referenceLimitIDs(1, 2, 2, 3)))
						referenceRunStep(t, p.ctx, b.db, b.recorder, referenceExec("DROP VIEW ties_null_v", 0))
						referenceRunStep(t, p.ctx, b.db, b.recorder, referencePrepared(referenceExec(q, 0)))
						referenceTiesBoth(t, p, b, referenceSelect("SELECT k FROM ties_null_v ORDER BY k", []string{"k"}, []string{"INT4"}, referenceLimitIDs(1, 2, 2, 3)))
					} else {
						referenceTiesBoth(t, p, b, referenceSelect(q, []string{"k"}, []string{"INT4"}, referenceLimitIDs(1, 2, 2, 3)))
					}
				})
			})
		}
	}
	t.Run("nil input normal and empty metadata", func(t *testing.T) {
		referenceTiesScenario(t, func(p *referencePair, b referenceBackend) {
			referenceTiesSetup(t, p, b)
			for _, pred := range []string{"", " WHERE FALSE"} {
				keys := []int64{1, 2, 2, 3}
				if pred != "" {
					keys = nil
				}
				referenceTiesBoth(t, p, b, referenceSelect("SELECT k FROM ties_t"+pred+" ORDER BY k FETCH FIRST $1 ROWS WITH TIES", []string{"k"}, []string{"INT4"}, referenceLimitIDs(keys...), nil))
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
			referenceTiesScenario(t, func(p *referencePair, b referenceBackend) {
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
				s := referenceImplicitError(q, referenceDiagnostic{Code: "42601", Message: message, Position: referencePosition(position)})
				s.selectRows = true
				s.errorStage = "execute"
				referenceRunStep(t, p.ctx, b.db, b.recorder, s)
				s.errorStage = "prepare"
				referenceRunStep(t, p.ctx, b.db, b.recorder, referencePrepared(s))
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
			referenceTiesScenario(t, func(p *referencePair, b referenceBackend) {
				referenceTiesSetup(t, p, b)
				referenceTiesBoth(t, p, b, referenceSelect(q, []string{"k"}, []string{"INT4"}, referenceLimitIDs(1, 2, 2)))
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
			referenceTiesScenario(t, func(p *referencePair, b referenceBackend) {
				referenceTiesSetup(t, p, b)
				prefix := "SELECT k FROM ties_t ORDER BY k FETCH FIRST "
				q := prefix + tc.bound + " ROWS WITH TIES"
				position := ""
				if !tc.runtime {
					position = referencePosition(len(prefix) + 1)
				}
				s := referenceImplicitError(q, referenceDiagnostic{Code: tc.code, Message: tc.message, Position: position})
				s.selectRows = true
				s.errorStage = "execute"
				referenceRunStep(t, p.ctx, b.db, b.recorder, s)
				if !tc.runtime {
					s.errorStage = "prepare"
				}
				referenceRunStep(t, p.ctx, b.db, b.recorder, referencePrepared(s))
			})
		})
	}
	t.Run("ordered view is not local ORDER", func(t *testing.T) {
		referenceTiesScenario(t, func(p *referencePair, b referenceBackend) {
			referenceTiesSetup(t, p, b)
			referenceRunStep(t, p.ctx, b.db, b.recorder, referenceExec("CREATE VIEW ties_ordered AS SELECT k FROM ties_t ORDER BY k", 0))
			q := "SELECT k FROM ties_ordered FETCH FIRST 2 ROWS WITH TIES"
			s := referenceImplicitError(q, referenceDiagnostic{Code: "42601", Message: "WITH TIES cannot be specified without ORDER BY clause", Position: referencePosition(strings.Index(q, "WITH TIES") + 1)})
			s.selectRows = true
			s.errorStage = "execute"
			referenceRunStep(t, p.ctx, b.db, b.recorder, s)
			s.errorStage = "prepare"
			referenceRunStep(t, p.ctx, b.db, b.recorder, referencePrepared(s))
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
				referenceTiesScenario(t, func(p *referencePair, b referenceBackend) {
					referenceTiesSetup(t, p, b)
					referenceRunStep(t, p.ctx, b.db, b.recorder, referenceExec("CREATE SEQUENCE ties_s START WITH 1", 0))
					s := referenceSelect("SELECT nextval('ties_s') AS n FROM ties_t ORDER BY k "+tc.suffix, []string{"n"}, []string{"INT8"}, referenceLimitIDs(tc.values...))
					if prepared {
						s = referencePrepared(s)
					}
					referenceRunStep(t, p.ctx, b.db, b.recorder, s)
					referenceRunStep(t, p.ctx, b.db, b.recorder, referenceSelect("SELECT nextval('ties_s') AS n", []string{"n"}, []string{"INT8"}, referenceLimitIDs(tc.next)))
				})
			})
		}
	}
	t.Run("shared volatile order cache", func(t *testing.T) {
		referenceTiesScenario(t, func(p *referencePair, b referenceBackend) {
			referenceTiesSetup(t, p, b)
			referenceRunStep(t, p.ctx, b.db, b.recorder, referenceExec("CREATE SEQUENCE ties_s START WITH 1", 0))
			referenceRunStep(t, p.ctx, b.db, b.recorder, referenceSelect("SELECT nextval('ties_s')%2 AS n FROM ties_t ORDER BY n,n FETCH FIRST ROW WITH TIES", []string{"n"}, []string{"INT8"}, referenceLimitIDs(0, 0)))
			referenceRunStep(t, p.ctx, b.db, b.recorder, referenceSelect("SELECT nextval('ties_s') AS n", []string{"n"}, []string{"INT8"}, referenceLimitIDs(5)))
		})
	})
	t.Run("all peers EOF", func(t *testing.T) {
		referenceTiesScenario(t, func(p *referencePair, b referenceBackend) {
			referenceTiesSetup(t, p, b)
			referenceRunStep(t, p.ctx, b.db, b.recorder, referenceExec("UPDATE ties_t SET k=2 WHERE id<4", 3))
			referenceRunStep(t, p.ctx, b.db, b.recorder, referenceExec("CREATE SEQUENCE ties_s START WITH 1", 0))
			referenceRunStep(t, p.ctx, b.db, b.recorder, referenceSelect("SELECT nextval('ties_s') AS n FROM ties_t WHERE id<4 ORDER BY k FETCH FIRST ROW WITH TIES", []string{"n"}, []string{"INT8"}, referenceLimitIDs(1, 2, 3)))
			referenceRunStep(t, p.ctx, b.db, b.recorder, referenceSelect("SELECT nextval('ties_s') AS n", []string{"n"}, []string{"INT8"}, referenceLimitIDs(4)))
		})
	})
}
