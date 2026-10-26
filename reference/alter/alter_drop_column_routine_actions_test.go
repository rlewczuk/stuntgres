package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceAlterDropColumnRoutineActions(t *testing.T) {
	for _, held := range []bool{false, true} {
		name := "direct"
		if held {
			name = "held"
		}
		t.Run(name, func(t *testing.T) {
			dcrScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, notices *[]fixture.Diagnostic) {
				dcrSetup(t, p, b, c)
				dcrExec(t, p, b, c,
					"CREATE FUNCTION dcr_b() RETURNS integer LANGUAGE SQL BEGIN ATOMIC SELECT b FROM dcr_t; END",
					"CREATE FUNCTION dcr_ab() RETURNS integer LANGUAGE SQL BEGIN ATOMIC SELECT a+b FROM dcr_t; END",
					"CREATE FUNCTION dcr_child() RETURNS integer LANGUAGE SQL RETURN dcr_ab()",
					"CREATE TABLE dcr_owner(x integer DEFAULT dcr_body())")
				before := dcrRows(t, p, c, dcrSnapshotSQL)
				missing := fixture.ControlsReferenceDiagnostic("42703", `column "missing" of relation "dcr_t" does not exist`)
				dcrError(t, p, b, c, "ALTER TABLE dcr_t DROP a CASCADE, DROP missing", missing, held)
				dcrUnchanged(t, p, b, c, before)
				// PostgreSQL can emit dependency notices before a later action fails;
				// these are full proposed records, confirmed by phase-B observation.
				dcrNotices(t, notices, "function dcr_body()", "default value for column x of table dcr_owner", "function dcr_ab()", "function dcr_child()")
				want := dcrRestrict("dcr_t", "b", "function dcr_b() depends on column b of table dcr_t")
				dcrError(t, p, b, c, "ALTER TABLE dcr_t DROP a CASCADE, DROP b RESTRICT", want, held)
				dcrUnchanged(t, p, b, c, before)
				dcrNotices(t, notices, "function dcr_body()", "default value for column x of table dcr_owner", "function dcr_ab()", "function dcr_child()")
				drop := fixture.Exec("ALTER TABLE dcr_t DROP a CASCADE, DROP b CASCADE", 0)
				if held {
					stmt, err := c.PrepareContext(p.Ctx, drop.Query)
					if err != nil {
						t.Fatal(err)
					}
					defer stmt.Close()
					dcrRun(t, p, b, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: drop.Query}, drop)
				} else {
					dcrRun(t, p, b, c, drop)
				}
				// Separate column actions each report their own closure. The shared
				// a+b dependent is absent during discovery of the later action.
				first := fixture.Diagnostic{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: "drop cascades to 4 other objects", Detail: "drop cascades to function dcr_body()\ndrop cascades to default value for column x of table dcr_owner\ndrop cascades to function dcr_ab()\ndrop cascades to function dcr_child()"}
				second := fixture.Diagnostic{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: "drop cascades to function dcr_b()"}
				if len(*notices) != 2 || (*notices)[0] != first || (*notices)[1] != second {
					t.Errorf("multi-action notices = %#v; want %#v", *notices, []fixture.Diagnostic{first, second})
				}
				*notices = nil
				dcrInventory(t, p, b, c)
				dcrRun(t, p, b, c, fixture.Select("SELECT * FROM dcr_t", []string{"c"}, []string{"INT4"}, [][]any{{int64(9)}}))
				dcrRun(t, p, b, c, fixture.Exec("INSERT INTO dcr_owner DEFAULT VALUES", 1))
				dcrRun(t, p, b, c, fixture.Select("SELECT x FROM dcr_owner", []string{"x"}, []string{"INT4"}, [][]any{{nil}}))
			})
		})
	}
}
