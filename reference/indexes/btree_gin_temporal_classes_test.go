package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceBtreeGINTemporalClasses(t *testing.T) {
	for _, schema := range []string{"public", "temporal_classes_alt"} {
		t.Run(schema, func(t *testing.T) {
			if schema != "public" {
				fixture.SchemaReferenceCleanup(t, schema)
			}
			classBackends(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				conn, run := classConn(t, p, b)
				compoundCleanup(t, p, b, conn)
				run(amCount("SELECT count(*) FROM pg_opclass WHERE opcmethod=2742 AND opcname IN "+temporalClasses, 0))
				for _, tc := range temporalCases {
					table := "temporal_classes_" + tc.typ
					run(fixture.Exec("CREATE TABLE "+table+"(id integer NOT NULL,v "+tc.typ+")", 0))
					d := fixture.ControlsReferenceDiagnostic("42704", "data type "+temporalTypeLabel(tc.typ)+" has no default operator class for access method \"gin\"")
					d.Hint = compoundMissingDefaultHint
					ginUtilityError(t, p, b, conn, "CREATE INDEX "+table+"_retry ON "+table+" USING gin(v)", d)
					compoundIndexAbsent(run, table, table+"_retry", 0)
				}
				if schema != "public" {
					run(fixture.Exec("CREATE SCHEMA "+schema, 0))
				}
				run(fixture.Prepared(fixture.Exec("CREATE EXTENSION btree_gin WITH SCHEMA "+schema+" VERSION '1.3'", 0)))
				classRoutes(t, p, b, conn, fixture.Select("SELECT e.extversion,e.extrelocatable,n.nspname FROM pg_extension e JOIN pg_namespace n ON n.oid=e.extnamespace WHERE e.extname='btree_gin'", []string{"extversion", "extrelocatable", "nspname"}, []string{"TEXT", "BOOL", "NAME"}, [][]any{{"1.3", true, schema}}))
				temporalGraph(t, p, b, conn, schema)
				for _, tc := range temporalCases {
					table := "temporal_classes_" + tc.typ
					run(fixture.Exec("CREATE INDEX "+table+"_retry ON "+table+" USING gin(v "+schema+"."+tc.typ+"_ops)", 0))
					run(fixture.Prepared(fixture.Exec("CREATE INDEX "+table+"_default ON "+table+" USING gin(v)", 0)))
					temporalSeed(run, table, tc)
					classRoutes(t, p, b, conn, ginIDs("SELECT id FROM "+table+" WHERE v=$1::"+tc.typ+" ORDER BY id", []int64{3, 4}, tc.boundary))
				}
				run(amCount("SELECT count(*) FROM pg_opclass c JOIN pg_depend d ON d.objid=c.oid AND d.classid='pg_opclass'::regclass WHERE c.opcmethod=783 AND d.refclassid='pg_extension'::regclass AND d.refobjid=(SELECT oid FROM pg_extension WHERE extname='btree_gin')", 0))
			})
		})
	}
}

func temporalTypeLabel(typ string) string {
	switch typ {
	case "time":
		return "time without time zone"
	case "timetz":
		return "time with time zone"
	case "timestamp":
		return "timestamp without time zone"
	case "timestamptz":
		return "timestamp with time zone"
	default:
		return typ
	}
}
