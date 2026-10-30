package reference_test

import (
	"database/sql"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

// Exact bodies and diagnostics independently observed on pinned PostgreSQL18.6.
// OPEN's parameter frame has returned before schema, mapper and PL FETCH demand.
func TestReferenceXMLMapRowsPLCursors(t *testing.T) {
	xmlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		defer c.ExecContext(p.Ctx, `ROLLBACK`)
		xmlMapExec(t, p, c,
			`CREATE FUNCTION xr_pl_open(p refcursor, n integer) RETURNS refcursor LANGUAGE plpgsql AS $$
BEGIN
  OPEN p SCROLL FOR SELECT g AS x FROM generate_series(n,n+2) g;
  RETURN p;
END
$$`,
			`CREATE FUNCTION xr_pl_next(p refcursor) RETURNS integer LANGUAGE plpgsql AS $$
DECLARE v integer;
BEGIN
  FETCH NEXT FROM p INTO v;
  RETURN v;
END
$$`,
			`CREATE FUNCTION xr_pl_map(p refcursor) RETURNS xml LANGUAGE plpgsql AS $$
BEGIN
  RETURN cursor_to_xml(p,1,false,true,'');
END
$$`,
			`CREATE FUNCTION xr_pl_self(p refcursor) RETURNS refcursor LANGUAGE plpgsql AS $$
BEGIN
  OPEN p SCROLL FOR SELECT cursor_to_xml(p,1,false,true,'') AS x;
  RETURN p;
END
$$`,
			`CREATE FUNCTION xr_pl_demand(p refcursor) RETURNS xml LANGUAGE plpgsql AS $$
DECLARE v xml;
BEGIN
  FETCH NEXT FROM p INTO v;
  RETURN v;
END
$$`,
			`CREATE FUNCTION xr_pl_schema(p refcursor) RETURNS xml LANGUAGE plpgsql AS $$
DECLARE v xml;
BEGIN
  OPEN p SCROLL FOR SELECT cursor_to_xmlschema(p,false,true,'') AS x;
  FETCH NEXT FROM p INTO v;
  RETURN v;
END
$$`)
		for _, prepared := range []bool{false, true} {
			for _, inside := range []bool{false, true} {
				name, first, finish := "xr_pl", 10, "COMMIT"
				if inside { name, first, finish = "xr_pl_inside", 20, "ROLLBACK" }
				xmlMapExec(t, p, c, `BEGIN`)
				xmlMapCursorStep(t, p, b, c, prepared, fmt.Sprintf(`SELECT xr_pl_open('%s',%d) AS v`, name, first), "REFCURSOR", name)
				xmlMapCursorStep(t, p, b, c, prepared, `SELECT cursor_to_xmlschema('`+name+`',false,true,'') AS v`, "XML", xmlMapIntSchema("x", "row", "", "", false, true))
				query := `SELECT cursor_to_xml('`+name+`',1,false,true,'') AS v`
				if inside { query = `SELECT xr_pl_map('`+name+`') AS v` }
				for _, n := range []int{first, first+2} {
					xmlMapCursorStep(t, p, b, c, prepared, query, "XML", xmlMapForest("row", "", fmt.Sprintf("  <x>%d</x>\n", n)))
					if n == first { xmlMapCursorStep(t, p, b, c, prepared, `SELECT xr_pl_next('`+name+`') AS v`, "INT4", int64(first+1)) }
				}
				if !inside { xmlMapCursorStep(t, p, b, c, prepared, query, "XML", "") }
				xmlMapExec(t, p, c, finish)
				for _, q := range []string{`SELECT cursor_to_xml('`+name+`',1,false,true,'') AS v`, `SELECT cursor_to_xmlschema('`+name+`',false,true,'') AS v`} {
					step := fixture.ErrorDiagnostic(q, xmlError("34000", fmt.Sprintf("cursor %q does not exist", name), "", 0))
					step.SelectRows, step.ErrorStage = true, "execute"
					if prepared { step = fixture.Prepared(step) }
					fixture.RunStep(t, p.Ctx, c, b.Recorder, step)
				}
			}
			for _, plFetch := range []bool{false, true} {
				xmlMapExec(t, p, c, `BEGIN`)
				xmlMapCursorStep(t, p, b, c, prepared, `SELECT xr_pl_self('xr_pl_self') AS v`, "REFCURSOR", "xr_pl_self")
				query := `SELECT cursor_to_xml('xr_pl_self',1,false,true,'') AS v`
				diagnostic := xmlError("55000", `portal "xr_pl_self" cannot be run`, "", 0)
				if plFetch {
					query = `SELECT xr_pl_demand('xr_pl_self') AS v`
					diagnostic.Context = "PL/pgSQL function xr_pl_demand(refcursor) line 4 at FETCH"
				}
				step := fixture.ErrorDiagnostic(query, diagnostic)
				step.SelectRows, step.ErrorStage = true, "execute"
				if prepared { step = fixture.Prepared(step) }
				fixture.RunStep(t, p.Ctx, c, b.Recorder, step)
				xmlMapExec(t, p, c, `ROLLBACK`)
			}
			xmlMapExec(t, p, c, `BEGIN`)
			xmlMapCursorStep(t, p, b, c, prepared, `SELECT xr_pl_schema('xr_pl_schema') AS v`, "XML", xmlMapActiveSchema())
			xmlMapExec(t, p, c, `ROLLBACK`)
		}
	})
}
