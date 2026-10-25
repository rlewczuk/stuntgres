package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceQueryTableOIDFunctionFrames(t *testing.T) {
	routineScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		routineExec(t, p, b, c, "CREATE TABLE toi_function_parent(a INT)", "CREATE TABLE toi_function_child() INHERITS(toi_function_parent)", "CREATE TABLE toi_function_zero()")
		for _, s := range []fixture.Step{fixture.Exec("INSERT INTO toi_function_parent VALUES(1)", 1), fixture.Exec("INSERT INTO toi_function_child VALUES(2)", 1), fixture.Exec("INSERT INTO toi_function_zero DEFAULT VALUES", 1)} {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		}
		parent := tableoidCell(b, tableoidIdentity(t, p, c, "toi_function_parent"))
		child := tableoidCell(b, tableoidIdentity(t, p, c, "toi_function_child"))
		zero := tableoidCell(b, tableoidIdentity(t, p, c, "toi_function_zero"))
		cases := []fixture.Step{
			fixture.Select("SELECT q.a,q.tableoid,f.physical FROM toi_function_parent q CROSS JOIN LATERAL unnest(ARRAY[q.tableoid]) f(physical) ORDER BY q.a", []string{"a", "tableoid", "physical"}, []string{"INT4", "OID", "OID"}, [][]any{{int64(1), parent, parent}, {int64(2), child, child}}),
			fixture.Select("SELECT f.physical FROM toi_function_zero z CROSS JOIN LATERAL unnest(ARRAY[z.tableoid]) f(physical)", []string{"physical"}, []string{"OID"}, [][]any{{zero}}),
			fixture.Select("SELECT f.physical FROM toi_function_zero z, (toi_function_parent q CROSS JOIN LATERAL unnest(ARRAY[z.tableoid,q.tableoid]) f(physical))", []string{"physical"}, []string{"OID"}, [][]any{{zero}, {parent}, {zero}, {child}}),
		}
		for i, s := range cases {
			t.Run(s.Query, func(t *testing.T) {
				if i == 2 {
					s.Bag = true
				}
				tableoidRoutes(t, p, b, c, s)
			})
		}
		for _, join := range []string{"RIGHT", "FULL"} {
			q := "SELECT f.physical FROM toi_function_parent q " + join + " JOIN LATERAL unnest(ARRAY[q.tableoid]) f(physical) ON TRUE"
			t.Run(join+"_forbidden_local_capture", func(t *testing.T) {
				tableoidDiagnosticRoutes(t, p, b, c, q, fixture.Diagnostic{Code: "42P10", Message: `invalid reference to FROM-clause entry for table "q"`, Detail: "The combining JOIN type must be INNER or LEFT for a LATERAL reference.", Position: fixture.Position(strings.Index(q, "q.tableoid") + 1)})
			})
		}
	})
}
