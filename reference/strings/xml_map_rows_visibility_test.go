package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

// Independently observed on pinned PostgreSQL18.6. The stable caller keeps its
// original snapshot, while a public same-transaction read proves the nested
// INSERT/DELETE happened. The private owner test inspects preparation itself.
func TestReferenceXMLMapRowsPreparationVisibility(t *testing.T) {
	xmlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		defer c.ExecContext(p.Ctx, `ROLLBACK`)
		xmlMapExec(t, p, c,
			`CREATE TABLE xr_visibility(x integer)`,
			`INSERT INTO xr_visibility VALUES (1)`,
			`CREATE FUNCTION xr_mutate() RETURNS integer LANGUAGE plpgsql VOLATILE AS $$
BEGIN
  INSERT INTO xr_visibility VALUES (2);
  DELETE FROM xr_visibility WHERE x=1;
  RETURN 0;
END
$$`,
			`CREATE FUNCTION xr_prepare_visibility() RETURNS xml LANGUAGE plpgsql STABLE AS $$
BEGIN
  PERFORM xr_mutate();
  BEGIN
    PERFORM query_to_xmlschema('SELECT x FROM xr_visibility; SELECT 1',false,true,'');
  EXCEPTION WHEN invalid_cursor_definition THEN
    PERFORM 1;
  END;
  RETURN query_to_xml('SELECT x FROM xr_visibility ORDER BY x',false,true,'');
END
$$`,
			`CREATE FUNCTION xr_prepare_readonly() RETURNS integer LANGUAGE plpgsql STABLE AS $$
BEGIN
  BEGIN
    PERFORM query_to_xmlschema('SELECT x FROM xr_visibility; SELECT 1',false,true,'');
  EXCEPTION WHEN invalid_cursor_definition THEN
    PERFORM 1;
  END;
  INSERT INTO xr_visibility VALUES (3);
  RETURN 1;
END
$$`)
		for _, prepared := range []bool{false, true} {
			xmlMapExec(t, p, c, `BEGIN`)
			xmlMapCursorStep(t, p, b, c, prepared, `SELECT xr_prepare_visibility() AS v`, "XML", xmlMapForest("row", "", "  <x>1</x>\n"))
			step := fixture.Select(`SELECT x FROM xr_visibility ORDER BY x`, []string{"x"}, []string{"INT4"}, [][]any{{int64(2)}})
			if prepared { step = fixture.Prepared(step) }
			fixture.RunStep(t, p.Ctx, c, b.Recorder, step)
			xmlMapExec(t, p, c, `ROLLBACK`)
			diagnostic := xmlError("0A000", "INSERT is not allowed in a non-volatile function", "", 0)
			diagnostic.Context = "SQL statement \"INSERT INTO xr_visibility VALUES (3)\"\nPL/pgSQL function xr_prepare_readonly() line 8 at SQL statement"
			step = fixture.ErrorDiagnostic(`SELECT xr_prepare_readonly() AS v`, diagnostic)
			step.SelectRows, step.ErrorStage = true, "execute"
			if prepared { step = fixture.Prepared(step) }
			fixture.RunStep(t, p.Ctx, c, b.Recorder, step)
			step = fixture.Select(`SELECT x FROM xr_visibility ORDER BY x`, []string{"x"}, []string{"INT4"}, [][]any{{int64(1)}})
			if prepared { step = fixture.Prepared(step) }
			fixture.RunStep(t, p.Ctx, c, b.Recorder, step)
		}
	})
}
