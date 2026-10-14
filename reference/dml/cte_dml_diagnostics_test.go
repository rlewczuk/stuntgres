package reference_test

import (
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"stuntgres/reference/fixture"
)

func cteDMLPosition(q, token string, last bool) string {
	at := strings.Index(q, token)
	if last {
		at = strings.LastIndex(q, token)
	}
	if at < 0 {
		panic("diagnostic token absent")
	}
	return strconv.Itoa(utf8.RuneCountInString(q[:at]) + 1)
}

func cteDMLError(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool, q string, want fixture.Diagnostic, stage ...string) {
	t.Helper()
	s := fixture.ImplicitError(q, want)
	s.SelectRows = true
	s.ErrorStage = "execute"
	if prepared {
		s.ErrorStage = "prepare"
	}
	if len(stage) != 0 {
		s.ErrorStage = stage[0]
	}
	cteDMLRun(t, p, b, prepared, s)
	cteDMLRun(t, p, b, false, cteDMLBag("SELECT * FROM t", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(10)}, {int64(2), int64(20)}}))
}

func TestReferenceCTEDMLDiagnostics(t *testing.T) {
	cases := []struct {
		name, q, code, message, token, detail, hint string
		last                                        bool
	}{
		{"no_returning", "WITH d AS (DELETE FROM t) SELECT * FROM d", "0A000", `WITH query "d" does not have a RETURNING clause`, "d", "", "", true},
		{"no_returning_utf8", `/* é */ WITH "dé" AS (DELETE FROM t) SELECT * FROM "dé"`, "0A000", `WITH query "dé" does not have a RETURNING clause`, `"dé"`, "", "", true},
		{"derived", "SELECT * FROM (WITH d AS (DELETE FROM t RETURNING id) SELECT * FROM d) s", "0A000", "WITH clause containing a data-modifying statement must be at the top level", "d AS", "", "", false},
		{"scalar", "SELECT (WITH d AS (DELETE FROM t RETURNING id) SELECT id FROM d LIMIT 1)", "0A000", "WITH clause containing a data-modifying statement must be at the top level", "d AS", "", "", false},
		{"nested_body", "WITH s AS (WITH d AS (DELETE FROM t RETURNING id) SELECT * FROM d) SELECT * FROM s", "0A000", "WITH clause containing a data-modifying statement must be at the top level", "d AS", "", "", false},
		{"source_local", "INSERT INTO t WITH d AS (DELETE FROM t RETURNING id,v) SELECT * FROM d", "0A000", "WITH clause containing a data-modifying statement must be at the top level", "d AS", "", "", false},
		{"duplicate", "WITH d AS (DELETE FROM t RETURNING id), d AS (SELECT 1) SELECT * FROM d", "42712", `WITH query name "d" specified more than once`, "d AS", "", "", true},
		{"duplicate_precedes_body", "WITH d AS (DELETE FROM missing RETURNING id), d AS (SELECT 1) SELECT 7", "42712", `WITH query name "d" specified more than once`, "d AS", "", "", true},
		{"excess_aliases", "WITH d(a,b,c) AS (DELETE FROM t RETURNING id,v) SELECT * FROM d", "42P10", `WITH query "d" has 2 columns available but 3 columns specified`, "d(a", "", "", false},
		{"forward", "WITH a AS (INSERT INTO t SELECT * FROM b RETURNING id), b AS (SELECT 3,30) SELECT * FROM a", "42P01", `relation "b" does not exist`, "b RETURNING", `There is a WITH item named "b", but it cannot be referenced from this part of the query.`, "Use WITH RECURSIVE, or re-order the WITH items to remove forward references.", false},
		{"rowmark", "WITH d AS (DELETE FROM t RETURNING id) SELECT * FROM d FOR UPDATE OF d", "0A000", "FOR UPDATE cannot be applied to a WITH query", "d", "", "", true},
		{"suffix", "WITH d AS (DELETE FROM t RETURNING id) SELECT * FROM d; SELECT FROM", "42601", "syntax error at end of input", "", "", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cteDMLRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
				cteDMLSeed(t, p, b)
				position := strconv.Itoa(utf8.RuneCountInString(tc.q) + 1)
				if tc.token != "" {
					position = cteDMLPosition(tc.q, tc.token, tc.last)
				}
				cteDMLError(t, p, b, prepared, tc.q, fixture.Diagnostic{Code: tc.code, Message: tc.message, Position: position, Detail: tc.detail, Hint: tc.hint})
			})
		})
	}
}

func TestReferenceCTEDMLRestrictions(t *testing.T) {
	for _, tc := range []struct{ name, q, message string }{
		{"view", "CREATE VIEW forbidden AS WITH d AS (DELETE FROM t RETURNING id) SELECT * FROM d", "views must not contain data-modifying statements in WITH"},
		{"view_unused", "CREATE VIEW forbidden AS WITH d AS (DELETE FROM t) SELECT 7 AS n", "views must not contain data-modifying statements in WITH"},
		{"materialized", "CREATE MATERIALIZED VIEW forbidden AS WITH d AS (DELETE FROM t RETURNING id) SELECT * FROM d", "materialized views must not use data-modifying statements in WITH"},
		{"materialized_no_data", "CREATE MATERIALIZED VIEW forbidden AS WITH d AS (DELETE FROM t RETURNING id) SELECT * FROM d WITH NO DATA", "materialized views must not use data-modifying statements in WITH"},
		{"cursor", "DECLARE c CURSOR WITH HOLD FOR WITH d AS (DELETE FROM t RETURNING id) SELECT * FROM d", "DECLARE CURSOR must not contain data-modifying statements in WITH"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cteDMLRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
				cteDMLSeed(t, p, b)
				stage := "execute"
				if prepared && tc.name != "view" && tc.name != "view_unused" {
					stage = "prepare"
				}
				cteDMLError(t, p, b, prepared, tc.q, fixture.Diagnostic{Code: "0A000", Message: tc.message}, stage)
				cteDMLRun(t, p, b, false, fixture.Select("SELECT relname FROM pg_class WHERE relname='forbidden'", []string{"relname"}, []string{"NAME"}, [][]any{}))
			})
		})
	}
	t.Run("recursive_boundary", func(t *testing.T) {
		cteDMLRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
			cteDMLSeed(t, p, b)
			q := "WITH RECURSIVE d AS (DELETE FROM t USING d WHERE t.id=d.id RETURNING t.id) SELECT * FROM d"
			want := fixture.Diagnostic{Code: "42P19", Message: `recursive query "d" must not contain data-modifying statements`, Position: "16"}
			cteDMLError(t, p, b, prepared, q, want)
		})
	})
	for _, tc := range []struct{ name, rule, message string }{
		{"conditional", "CREATE RULE r AS ON DELETE TO t WHERE old.id>0 DO INSTEAD DELETE FROM log WHERE id=old.id", "conditional DO INSTEAD rules are not supported for data-modifying statements in WITH"},
		{"also", "CREATE RULE r AS ON DELETE TO t DO ALSO INSERT INTO log VALUES(old.id)", "DO ALSO rules are not supported for data-modifying statements in WITH"},
		{"multi", "CREATE RULE r AS ON DELETE TO t DO INSTEAD (DELETE FROM log WHERE id=old.id; INSERT INTO log VALUES(old.id))", "multi-statement DO INSTEAD rules are not supported for data-modifying statements in WITH"},
	} {
		t.Run("rule_"+tc.name, func(t *testing.T) {
			cteDMLRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
				cteDMLSeed(t, p, b)
				cteDMLRun(t, p, b, false, fixture.Exec("CREATE TABLE log(id INT)", 0))
				cteDMLRun(t, p, b, false, fixture.Exec(tc.rule, 0))
				cteDMLError(t, p, b, prepared, "WITH d AS (DELETE FROM t) SELECT 7 AS n", fixture.Diagnostic{Code: "0A000", Message: tc.message})
				cteDMLRun(t, p, b, false, fixture.Select("SELECT id FROM log", []string{"id"}, []string{"INT4"}, [][]any{}))
				cteDMLRun(t, p, b, false, cteDMLBag("SELECT id,v FROM t", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(10)}, {int64(2), int64(20)}}))
			})
		})
	}
	t.Run("physical_target_shadow", func(t *testing.T) {
		cteDMLRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
			cteDMLSeed(t, p, b)
			q := "WITH t AS (SELECT 99 AS id), d AS (DELETE FROM t RETURNING id) SELECT * FROM d"
			cteDMLRun(t, p, b, prepared, cteDMLBag(q, []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}))
			cteDMLRun(t, p, b, false, cteDMLBag("SELECT * FROM t", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{}))
		})
	})
}
