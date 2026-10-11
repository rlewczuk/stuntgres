package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceQuerySubqueryJoinLegality(t *testing.T) {
	full := "FULL JOIN is only supported with merge-joinable or hash-joinable join conditions"
	nullable := "FOR UPDATE cannot be applied to the nullable side of an outer join"
	for _, tc := range []struct {
		name, query, message string
		names, types         []string
		cells                [][]any
	}{
		{name: "scalar", query: "SELECT(SELECT a.id FROM a FULL JOIN b ON a.id>b.id) AS value", message: full},
		{name: "derived", query: "SELECT d.id FROM(SELECT a.id FROM a FULL JOIN b ON a.id>b.id)d", message: full},
		{name: "empty-outer", query: "SELECT(SELECT a.id FROM a FULL JOIN b ON a.id>b.id) AS value WHERE FALSE", message: full},
		{name: "empty-inner", query: "SELECT(SELECT a.id FROM a FULL JOIN b ON a.id>b.id WHERE FALSE) AS value", names: []string{"value"}, types: []string{"INT4"}, cells: [][]any{{nil}}},
		{name: "ignored-target", query: "SELECT EXISTS(SELECT(SELECT a.id FROM a FULL JOIN b ON a.id>b.id) FROM t) AS present", names: []string{"present"}, types: []string{"BOOL"}, cells: [][]any{{true}}},
		{name: "derived-inherited-rowmark", query: "SELECT d.id FROM(SELECT a.id FROM a LEFT JOIN b ON a.id=b.id)d FOR UPDATE", message: nullable},
		{name: "child-local-rowmark", query: "SELECT(SELECT a.id FROM a LEFT JOIN b ON a.id=b.id FOR UPDATE) AS value", message: nullable},
		{name: "expression-no-inherited-rowmark", query: "SELECT t.id,(SELECT a.id FROM a LEFT JOIN b ON a.id=b.id) AS value FROM t FOR UPDATE", names: []string{"id", "value"}, types: []string{"INT4", "INT4"}, cells: [][]any{{int64(1), int64(1)}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			subqueryReference(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				run := subqueryRun(t, p, b, c)
				for _, q := range []string{"CREATE TABLE a(id INT)", "CREATE TABLE b(id INT)", "CREATE TABLE t(id INT)"} {
					run(fixture.Exec(q, 0))
				}
				for _, q := range []string{"INSERT INTO a VALUES(1)", "INSERT INTO b VALUES(1)", "INSERT INTO t VALUES(1)"} {
					run(fixture.Exec(q, 1))
				}
				held := subqueryReviewHold(t, p, c, tc.query)
				if tc.message != "" {
					want := fixture.ControlsReferenceDiagnostic("0A000", tc.message)
					subqueryReviewFailure(t, p, b, c, tc.query, want)
					subqueryReviewFailure(t, p, b, held, tc.query, want)
					return
				}
				step := fixture.Select(tc.query, tc.names, tc.types, tc.cells)
				run(step)
				fixture.RunStep(t, p.Ctx, held, b.Recorder, step)
			})
		})
	}
}

func TestReferenceQuerySubqueryMutationJoinLegality(t *testing.T) {
	full := "FULL JOIN is only supported with merge-joinable or hash-joinable join conditions"
	nullable := "FOR UPDATE cannot be applied to the nullable side of an outer join"
	// Pinned PostgreSQL observations establish these independently specified
	// mutation contracts, including successful held Prepare before execution.
	for _, tc := range []struct {
		name, query, message string
		affected             int64
		names, types         []string
		cells, stored        [][]any
	}{
		{
			name:    "update-assignment-selected",
			query:   "UPDATE t SET id=(SELECT a.id FROM a FULL JOIN b ON a.id>b.id) WHERE id=1",
			message: full,
		},
		{
			name:    "update-assignment-outer-false",
			query:   "UPDATE t SET id=(SELECT a.id FROM a FULL JOIN b ON a.id>b.id) WHERE FALSE",
			message: full,
		},
		{
			name:    "update-predicate",
			query:   "UPDATE t SET id=2 WHERE id=(SELECT a.id FROM a FULL JOIN b ON a.id>b.id)",
			message: full,
		},
		{
			name:    "delete-predicate",
			query:   "DELETE FROM t WHERE id=(SELECT a.id FROM a FULL JOIN b ON a.id>b.id)",
			message: full,
		},
		{
			name:    "delete-predicate-no-matching-row",
			query:   "DELETE FROM t WHERE id=99 AND id=(SELECT a.id FROM a FULL JOIN b ON a.id>b.id)",
			message: full,
		},
		{
			name:    "insert-values",
			query:   "INSERT INTO t VALUES((SELECT a.id FROM a FULL JOIN b ON a.id>b.id))",
			message: full,
		},
		{
			name:    "insert-select-empty-root",
			query:   "INSERT INTO t SELECT(SELECT a.id FROM a FULL JOIN b ON a.id>b.id) WHERE FALSE",
			message: full,
		},
		{
			name:    "update-returning-selected",
			query:   "UPDATE t SET id=2 WHERE id=1 RETURNING(SELECT a.id FROM a FULL JOIN b ON a.id>b.id) AS value",
			message: full,
		},
		{
			name:    "update-returning-zero-row",
			query:   "UPDATE t SET id=2 WHERE FALSE RETURNING(SELECT a.id FROM a FULL JOIN b ON a.id>b.id) AS value",
			message: full,
		},
		{
			name:    "delete-returning-selected",
			query:   "DELETE FROM t WHERE id=1 RETURNING(SELECT a.id FROM a FULL JOIN b ON a.id>b.id) AS value",
			message: full,
		},
		{
			name:    "delete-returning-zero-row",
			query:   "DELETE FROM t WHERE FALSE RETURNING(SELECT a.id FROM a FULL JOIN b ON a.id>b.id) AS value",
			message: full,
		},
		{
			name:    "update-assignment-child-local-rowmark",
			query:   "UPDATE t SET id=(SELECT a.id FROM a LEFT JOIN b ON a.id=b.id FOR UPDATE) WHERE id=1",
			message: nullable,
		},
		{
			name:    "update-assignment-child-local-rowmark-outer-false",
			query:   "UPDATE t SET id=(SELECT a.id FROM a LEFT JOIN b ON a.id=b.id FOR UPDATE) WHERE FALSE",
			message: nullable,
		},
		{
			name:     "update-eligible-exists-discarded-target",
			query:    "UPDATE t SET id=2 WHERE EXISTS(SELECT(SELECT a.id FROM a FULL JOIN b ON a.id>b.id) FROM a)",
			affected: 1, stored: [][]any{{int64(2)}},
		},
		{
			name:     "delete-eligible-exists-discarded-target",
			query:    "DELETE FROM t WHERE EXISTS(SELECT(SELECT a.id FROM a FULL JOIN b ON a.id>b.id) FROM a)",
			affected: 1,
		},
		{
			name:     "update-assignment-child-false",
			query:    "UPDATE t SET id=(SELECT a.id FROM a FULL JOIN b ON a.id>b.id WHERE FALSE) WHERE id=1",
			affected: 1, stored: [][]any{{nil}},
		},
		{
			name:     "insert-values-child-false",
			query:    "INSERT INTO t VALUES((SELECT a.id FROM a FULL JOIN b ON a.id>b.id WHERE FALSE))",
			affected: 1, stored: [][]any{{int64(1)}, {nil}},
		},
		{
			name:     "update-returning-child-false-selected",
			query:    "UPDATE t SET id=2 WHERE id=1 RETURNING(SELECT a.id FROM a FULL JOIN b ON a.id>b.id WHERE FALSE) AS value",
			affected: 1, names: []string{"value"}, types: []string{"INT4"},
			cells: [][]any{{nil}}, stored: [][]any{{int64(2)}},
		},
		{
			name:  "update-returning-child-false-zero-row",
			query: "UPDATE t SET id=2 WHERE FALSE RETURNING(SELECT a.id FROM a FULL JOIN b ON a.id>b.id WHERE FALSE) AS value",
			names: []string{"value"}, types: []string{"INT4"}, stored: [][]any{{int64(1)}},
		},
		{
			name:     "delete-returning-child-false-selected",
			query:    "DELETE FROM t WHERE id=1 RETURNING(SELECT a.id FROM a FULL JOIN b ON a.id>b.id WHERE FALSE) AS value",
			affected: 1, names: []string{"value"}, types: []string{"INT4"}, cells: [][]any{{nil}},
		},
		{
			name:  "delete-returning-child-false-zero-row",
			query: "DELETE FROM t WHERE FALSE RETURNING(SELECT a.id FROM a FULL JOIN b ON a.id>b.id WHERE FALSE) AS value",
			names: []string{"value"}, types: []string{"INT4"}, stored: [][]any{{int64(1)}},
		},
		{
			name:   "delete-folded-false-unused-subplan",
			query:  "DELETE FROM t WHERE FALSE AND id=(SELECT a.id FROM a FULL JOIN b ON a.id>b.id)",
			stored: [][]any{{int64(1)}},
		},
		{
			name:   "update-folded-false-unused-subplan",
			query:  "UPDATE t SET id=2 WHERE FALSE AND id=(SELECT a.id FROM a FULL JOIN b ON a.id>b.id)",
			stored: [][]any{{int64(1)}},
		},
		{
			name:     "update-left-true-unused-subplan",
			query:    "UPDATE t SET id=2 WHERE TRUE OR id=(SELECT a.id FROM a FULL JOIN b ON a.id>b.id)",
			affected: 1, stored: [][]any{{int64(2)}},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			subqueryReference(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				run := subqueryRun(t, p, b, c)
				for _, q := range []string{"CREATE TABLE a(id INT)", "CREATE TABLE b(id INT)", "CREATE TABLE t(id INT)"} {
					run(fixture.Exec(q, 0))
				}
				for _, q := range []string{"INSERT INTO a VALUES(1)", "INSERT INTO b VALUES(1)", "INSERT INTO t VALUES(1)"} {
					run(fixture.Exec(q, 1))
				}
				held := subqueryReviewHold(t, p, c, tc.query)
				checkStored := func(t *testing.T, cells [][]any) {
					t.Helper()
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select(
						"SELECT id FROM t ORDER BY id NULLS LAST",
						[]string{"id"}, []string{"INT4"}, cells,
					))
				}
				reset := func(t *testing.T) {
					t.Helper()
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("DELETE FROM t", int64(len(tc.stored))))
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO t VALUES(1)", 1))
					checkStored(t, [][]any{{int64(1)}})
				}
				for _, route := range []struct {
					name string
					x    fixture.Executor
				}{{"direct", c}, {"held", held}} {
					t.Run(route.name, func(t *testing.T) {
						if tc.message != "" {
							// Both severities are ERROR; every optional diagnostic
							// field, including Position and Context, must be absent.
							want := fixture.ControlsReferenceDiagnostic("0A000", tc.message)
							subqueryReviewFailure(t, p, b, route.x, tc.query, want)
							checkStored(t, [][]any{{int64(1)}})
							return
						}
						// Query verifies zero-column non-RETURNING cursors or the
						// independently specified RETURNING metadata, even at zero rows.
						fixture.RunStep(t, p.Ctx, route.x, b.Recorder, fixture.Select(
							tc.query, tc.names, tc.types, tc.cells,
						))
						checkStored(t, tc.stored)
						reset(t)
						// Exec has its own affected count, not the Query row count.
						fixture.RunStep(t, p.Ctx, route.x, b.Recorder, fixture.Exec(tc.query, tc.affected))
						checkStored(t, tc.stored)
						reset(t)
					})
				}
			})
		})
	}
}

func TestReferenceQuerySubqueryMutationJoinParameterFreshness(t *testing.T) {
	subqueryReference(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := subqueryRun(t, p, b, c)
		for _, q := range []string{"CREATE TABLE t(id INT)", "CREATE TABLE a(id INT)", "CREATE TABLE b(id INT)"} {
			run(fixture.Exec(q, 0))
		}
		for _, q := range []string{"INSERT INTO a VALUES(1)", "INSERT INTO b VALUES(1)", "INSERT INTO t VALUES(1)"} {
			run(fixture.Exec(q, 1))
		}
		query := "UPDATE t SET id=(SELECT a.id FROM a FULL JOIN b ON a.id>b.id WHERE $1) RETURNING id"
		statement, err := c.PrepareContext(p.Ctx, query)
		fixture.State(t, err, "")
		defer statement.Close()
		held := fixture.HeldStatement{Executor: c, Statement: statement, SQL: query}
		for _, enabled := range []bool{false, true, false} {
			for _, prepared := range []bool{false, true} {
				run(fixture.Exec("DELETE FROM t", 1))
				run(fixture.Exec("INSERT INTO t VALUES(1)", 1))
				var executor fixture.Executor = c
				if prepared {
					executor = held
				}
				var queryStep, execStep fixture.Step
				stored := [][]any{{nil}}
				if enabled {
					want := fixture.ControlsReferenceDiagnostic("0A000", "FULL JOIN is only supported with merge-joinable or hash-joinable join conditions")
					execStep = fixture.ErrorDiagnostic(query, want, enabled)
					queryStep = execStep
					queryStep.SelectRows = true
					stored = [][]any{{int64(1)}}
				} else {
					queryStep = fixture.Select(query, []string{"id"}, []string{"INT4"}, [][]any{{nil}}, enabled)
					execStep = fixture.Exec(query, 1, enabled)
				}
				fixture.RunStep(t, p.Ctx, executor, b.Recorder, queryStep)
				run(fixture.Select("SELECT id FROM t", []string{"id"}, []string{"INT4"}, stored))
				fixture.RunStep(t, p.Ctx, executor, b.Recorder, execStep)
				run(fixture.Select("SELECT id FROM t", []string{"id"}, []string{"INT4"}, stored))
			}
		}
	})
}
