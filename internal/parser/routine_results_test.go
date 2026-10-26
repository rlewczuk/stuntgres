package parser

import (
	"errors"
	"testing"

	"stuntgres/internal/sqlstate"
)

func TestRoutineResultDeclarations(t *testing.T) {
	for _, tc := range []struct {
		declaration           string
		specified, set, table bool
		outputs               int
	}{
		{"(IN x integer,OUT a integer,OUT b text)", false, false, false, 0},
		{"(IN x integer,OUT a integer,OUT b text) RETURNS record", true, false, false, 0},
		{"(integer) RETURNS SETOF integer", true, true, false, 0},
		{"(integer) RETURNS SETOF pg_catalog.int4[]", true, true, false, 0},
		{"() RETURNS \"setof\"", true, false, false, 0},
		{"(integer) RETURNS TABLE(a integer,b text)", true, true, true, 2},
		{"(integer) RETURNS TABLE(a integer)", true, true, true, 1},
	} {
		for _, body := range []string{" LANGUAGE SQL AS 'SELECT 1'", " LANGUAGE SQL BEGIN ATOMIC SELECT 1; END", " AS 'SELECT 1' LANGUAGE SQL"} {
			statements, err := Parse("CREATE FUNCTION result_form" + tc.declaration + body)
			if err != nil {
				t.Fatal(tc.declaration, body, err)
			}
			d := statements[0].(*CreateFunction)
			if d.ResultSpecified != tc.specified || d.Result.Setof != tc.set || (d.TableResult != nil) != tc.table || len(d.TableResult) != tc.outputs {
				t.Fatal("result declaration form lost", d)
			}
			for _, output := range d.TableResult {
				if output.Mode != "table" || output.Name.Text == "" || output.Pos.Character <= 0 || output.Type.Pos.Character <= 0 {
					t.Fatal("TABLE output syntax lost", output)
				}
			}
		}
	}
	for _, query := range []string{"SELECT returns setof", "SELECT returns AS setof", "SELECT \"setof\""} {
		if _, err := Parse(query); err != nil {
			t.Fatal("SETOF became a reserved ordinary identifier", query, err)
		}
	}
	_, err := Parse("CREATE FUNCTION rr_bad_table(OUT x integer) RETURNS TABLE(y integer) LANGUAGE SQL AS 'SELECT 1'")
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) || diagnostic.Code != "42601" || diagnostic.Message != "OUT and INOUT arguments aren't allowed in TABLE functions" || diagnostic.Position != "30" {
		t.Fatal("TABLE conflicting output diagnostic", err)
	}
	statements, err := Parse("CREATE FUNCTION result_form() RETURNS TABLE(a integer) LANGUAGE SQL AS 'SELECT 1'; invalid")
	if err == nil || len(statements) != 0 {
		t.Fatal("accepted valid declaration prefix")
	}
}
