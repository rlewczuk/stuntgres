package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePLpgSQLCursorOperandIdentity(t *testing.T) {
	for _, tc := range []struct{ name, statement, position string }{
		{"open_static", "OPEN c FOR SELECT 7;", "127"},
		{"open_dynamic", "OPEN c FOR EXECUTE 'SELECT $1::integer' USING 7;", "127"},
		{"fetch", "FETCH c INTO x;", "128"},
		{"move", "MOVE c;", "127"},
		{"close", "CLOSE c;", "128"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					c, err := b.DB.Conn(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer c.Close()
					sqlCursorStep(t, p, b, c, fixture.Exec("CREATE DOMAIN pc_cursor_domain AS refcursor", 0))
					query := "CREATE FUNCTION pc_operand_identity() RETURNS integer LANGUAGE plpgsql AS $$DECLARE c pc_cursor_domain; x integer; BEGIN " + tc.statement + " RETURN 0; END$$"
					want := sqlCursorDiagnostic("42804", `variable "c" must be of type cursor or refcursor`)
					want.Position = tc.position
					plCursorError(t, p, b, c, query, want, false, false)
					plCursorValue(t, p, b, c, "SELECT count(*) AS v FROM pg_proc WHERE proname='pc_operand_identity'", "INT8", int64(0))
					plCursorFunction(t, p, b, c, "pc_operand_identity", "", "integer", "DECLARE c refcursor; x integer; BEGIN OPEN c FOR SELECT 7; FETCH c INTO x; CLOSE c; RETURN x; END")
					plCursorValue(t, p, b, c, "SELECT pc_operand_identity() AS v", "INT4", int64(7))
				})
			}
		})
	}
}
