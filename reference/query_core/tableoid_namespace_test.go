package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceQueryTableOIDNamespaces(t *testing.T) {
	routineScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		routineExec(t, p, b, c, "CREATE TABLE toi_names(a INT)", "CREATE VIEW toi_plain AS SELECT a FROM toi_names")
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO toi_names VALUES(2),(1)", 2))
		oid := tableoidCell(b, tableoidIdentity(t, p, c, "toi_names"))
		for _, q := range []string{
			"SELECT q.tableoid FROM toi_names q(tableoid) ORDER BY q.tableoid",
			"SELECT tableoid FROM toi_names q(tableoid) ORDER BY tableoid",
			"SELECT a AS tableoid FROM toi_names ORDER BY tableoid",
			"SELECT d.tableoid FROM (SELECT a AS tableoid FROM toi_names) d ORDER BY d.tableoid",
			"WITH d(tableoid) AS (SELECT a FROM toi_names) SELECT d.tableoid FROM d ORDER BY d.tableoid",
		} {
			tableoidRoutes(t, p, b, c, fixture.Select(q, []string{"tableoid"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}))
		}
		q := "SELECT a AS tableoid FROM toi_names WHERE tableoid='toi_names'::regclass::oid ORDER BY a"
		tableoidRoutes(t, p, b, c, fixture.Select(q, []string{"tableoid"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}))
		routineExec(t, p, b, c, "CREATE VIEW toi_forward AS SELECT q.tableoid FROM toi_names q")
		routineExec(t, p, b, c, "CREATE VIEW toi_named_output AS SELECT a AS tableoid FROM toi_names")
		tableoidRoutes(t, p, b, c, fixture.Select("SELECT tableoid FROM toi_named_output ORDER BY tableoid", []string{"tableoid"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}))
		for _, q := range []string{
			"SELECT d.* FROM (SELECT tableoid FROM toi_names) d",
			"WITH d AS (SELECT tableoid FROM toi_names) SELECT d.* FROM d",
			"SELECT * FROM toi_forward",
		} {
			tableoidRoutes(t, p, b, c, fixture.Select(q, []string{"tableoid"}, []string{"OID"}, [][]any{{oid}, {oid}}))
		}
	})
}

func TestReferenceQueryTableOIDDiagnostics(t *testing.T) {
	for _, populated := range []bool{false, true} {
		t.Run(map[bool]string{false: "empty", true: "populated"}[populated], func(t *testing.T) {
			routineScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				routineExec(t, p, b, c, "CREATE TABLE toi_diag(a INT)", "CREATE VIEW toi_diag_view AS SELECT a FROM toi_diag", "CREATE TABLE toi_diag_zero()")
				if populated {
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO toi_diag VALUES(1)", 1))
				}
				t.Run("mixed_cross_join_output", func(t *testing.T) {
					var want [][]any
					if populated {
						want = [][]any{{int64(1)}}
					}
					tableoidRoutes(t, p, b, c, fixture.Select("SELECT tableoid FROM toi_diag a(tableoid) CROSS JOIN toi_diag b", []string{"tableoid"}, []string{"INT4"}, want))
				})
				cases := []struct {
					q string
					d fixture.Diagnostic
				}{
					{"SELECT tableoid", fixture.Diagnostic{Code: "42703", Message: `column "tableoid" does not exist`, Position: "8"}},
					{"SELECT /*é*/ tableoid", fixture.Diagnostic{Code: "42703", Message: `column "tableoid" does not exist`, Position: "14"}},
					{"SELECT tableoid FROM toi_diag a CROSS JOIN toi_diag b", fixture.Diagnostic{Code: "42703", Message: `column "tableoid" does not exist`, Detail: `There are columns named "tableoid", but they are in tables that cannot be referenced from this part of the query.`, Hint: "Try using a table-qualified name.", Position: "8"}},
					{"SELECT tableoid FROM toi_diag a,toi_diag b", fixture.Diagnostic{Code: "42702", Message: `column reference "tableoid" is ambiguous`, Position: "8"}},
					{"SELECT tableoid FROM toi_diag a(tableoid),toi_diag b", fixture.Diagnostic{Code: "42702", Message: `column reference "tableoid" is ambiguous`, Position: "8"}},
					{"SELECT toi_diag.tableoid FROM toi_diag AS a", fixture.Diagnostic{Code: "42P01", Message: `invalid reference to FROM-clause entry for table "toi_diag"`, Hint: `Perhaps you meant to reference the table alias "a".`, Position: "8"}},
					{"SELECT d.tableoid FROM (SELECT a FROM toi_diag) AS d", fixture.Diagnostic{Code: "42703", Message: `column d.tableoid does not exist`, Position: "8"}},
					{"SELECT d.tableoid FROM toi_diag_view d", fixture.Diagnostic{Code: "42703", Message: `column d.tableoid does not exist`, Position: "8"}},
					{"WITH d AS (SELECT a FROM toi_diag) SELECT d.tableoid FROM d", fixture.Diagnostic{Code: "42703", Message: `column d.tableoid does not exist`, Position: "43"}},
					{"SELECT j.tableoid FROM (toi_diag a CROSS JOIN toi_diag b) j", fixture.Diagnostic{Code: "42703", Message: `column j.tableoid does not exist`, Position: "8"}},
					{"SELECT (SELECT d.tableoid FROM (SELECT a FROM toi_diag) d) FROM toi_diag d", fixture.Diagnostic{Code: "42703", Message: `column d.tableoid does not exist`, Detail: `There is a column named "tableoid" in table "d", but it cannot be referenced from this part of the query.`, Position: "16"}},
					{"SELECT * FROM toi_diag AS q(x,y)", fixture.Diagnostic{Code: "42P10", Message: `table "q" has 1 columns available but 2 columns specified`}},
					{"SELECT * FROM toi_diag_zero AS q(x)", fixture.Diagnostic{Code: "42P10", Message: `table "q" has 0 columns available but 1 columns specified`}},
					{"SELECT q.tableoid FROM toi_diag q LIMIT q.tableoid", fixture.Diagnostic{Code: "42P10", Message: "argument of LIMIT must not contain variables"}},
					{"SELECT q.tableoid FROM toi_diag q OFFSET q.tableoid", fixture.Diagnostic{Code: "42P10", Message: "argument of OFFSET must not contain variables"}},
					{"SELECT b.tableoid FROM toi_diag a CROSS JOIN toi_diag b GROUP BY a.tableoid", fixture.Diagnostic{Code: "42803", Message: `column "b.tableoid" must appear in the GROUP BY clause or be used in an aggregate function`, Position: "8"}},
				}
				for _, tc := range cases {
					t.Run(tc.q, func(t *testing.T) {
						if tc.d.Code == "42P10" && strings.Contains(tc.q, "LIMIT") {
							tc.d.Position = fixture.Position(strings.LastIndex(tc.q, "q.tableoid") + 1)
						}
						if tc.d.Code == "42P10" && strings.Contains(tc.q, "OFFSET") {
							tc.d.Position = fixture.Position(strings.LastIndex(tc.q, "q.tableoid") + 1)
						}
						tableoidDiagnosticRoutes(t, p, b, c, tc.q, tc.d)
					})
				}
			})
		})
	}
}
