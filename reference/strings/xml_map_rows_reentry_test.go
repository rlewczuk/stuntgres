package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

// Pinned PostgreSQL 18.6 observations cover ordinary FETCH as well as mapper
// entry: both reject recursive movement with the same otherwise empty envelope.
func TestReferenceXMLMapRowsCursorReentry(t *testing.T) {
	xmlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		defer c.ExecContext(p.Ctx, `ROLLBACK`)
		for _, prepared := range []bool{false, true} {
			for _, query := range []string{`FETCH NEXT FROM xr_self`, `SELECT cursor_to_xml('xr_self',1,false,true,'') AS v`} {
				xmlMapExec(t, p, c, `BEGIN`, `DECLARE xr_self SCROLL CURSOR FOR SELECT cursor_to_xml('xr_self',1,false,true,'') AS x`)
				step := fixture.ErrorDiagnostic(query, xmlError("55000", `portal "xr_self" cannot be run`, "", 0))
				step.SelectRows, step.ErrorStage = true, "execute"
				if prepared { step = fixture.Prepared(step) }
				fixture.RunStep(t, p.Ctx, c, b.Recorder, step)
				xmlMapExec(t, p, c, `ROLLBACK`)
			}
			// Inspecting the same active producer's descriptor is not motion.
			// A second real FETCH proves successful motion releases admission.
			xmlMapExec(t, p, c, `BEGIN`, `DECLARE xr_self_schema SCROLL CURSOR FOR SELECT cursor_to_xmlschema('xr_self_schema',false,true,'') AS x FROM generate_series(1,2)`)
			for range 2 {
				step := fixture.Select(`FETCH NEXT FROM xr_self_schema`, []string{"x"}, []string{"XML"}, [][]any{{xmlMapActiveSchema()}})
				if prepared { step = fixture.Prepared(step) }
				fixture.RunStep(t, p.Ctx, c, b.Recorder, step)
			}
			xmlMapExec(t, p, c, `ROLLBACK`)
			xmlMapExec(t, p, c, `BEGIN`, `DECLARE xr_self_schema SCROLL CURSOR FOR SELECT cursor_to_xmlschema('xr_self_schema',false,true,'') AS x`)
			xmlMapCursorStep(t, p, b, c, prepared, `SELECT cursor_to_xml('xr_self_schema',1,false,true,'') AS v`, "XML", xmlMapForest("row", "", "  <x>"+xmlMapActiveSchema()+"</x>\n"))
			xmlMapExec(t, p, c, `ROLLBACK`)
		}
	})
}

func xmlMapActiveSchema() string {
	return xmlMapSchema("<xsd:complexType mixed=\"true\">\n  <xsd:sequence>\n    <xsd:any name=\"element\" minOccurs=\"0\" maxOccurs=\"unbounded\" processContents=\"skip\"/>\n  </xsd:sequence>\n</xsd:complexType>\n\n", xmlMapColumn("x", "XML", false), "row", "", "", true)
}
