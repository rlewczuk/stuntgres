package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceBtreeGINScalarClasses(t *testing.T) {
	for _, schema := range []string{"public", "scalar_classes_alt"} {
		t.Run(schema, func(t *testing.T) {
			if schema != "public" {
				fixture.SchemaReferenceCleanup(t, schema)
			}
			classBackends(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				conn, run := classConn(t, p, b)
				compoundCleanup(t, p, b, conn)
				run(amCount("SELECT count(*) FROM pg_opclass WHERE opcmethod=2742 AND opcname IN "+scalarClasses, 0))
				run(fixture.Exec("CREATE TABLE scalar_classes(id integer NOT NULL,v integer)", 0))
				d := fixture.ControlsReferenceDiagnostic("42704", `data type integer has no default operator class for access method "gin"`)
				d.Hint = compoundMissingDefaultHint
				for _, prefix := range []string{"", "/*é*/ "} {
					ginUtilityError(t, p, b, conn, prefix+"CREATE INDEX scalar_retry ON scalar_classes USING gin(v)", d)
					compoundIndexAbsent(run, "scalar_classes", "scalar_retry", 0)
				}
				if schema != "public" {
					run(fixture.Exec("CREATE SCHEMA "+schema, 0))
				}
				run(fixture.Prepared(fixture.Exec("CREATE EXTENSION btree_gin WITH SCHEMA "+schema+" VERSION '1.3'", 0)))
				classRoutes(t, p, b, conn, fixture.Select("SELECT e.extversion,e.extrelocatable,n.nspname,obj_description(e.oid,'pg_extension') AS comment FROM pg_extension e JOIN pg_namespace n ON n.oid=e.extnamespace WHERE e.extname='btree_gin'", []string{"extversion", "extrelocatable", "nspname", "comment"}, []string{"TEXT", "BOOL", "NAME", "TEXT"}, [][]any{{"1.3", true, schema, "support for indexing common datatypes in GIN"}}))
				classRoutes(t, p, b, conn, fixture.Select("SELECT name,version,superuser,trusted,relocatable FROM pg_available_extension_versions WHERE name='btree_gin' AND version='1.3'", []string{"name", "version", "superuser", "trusted", "relocatable"}, []string{"NAME", "TEXT", "BOOL", "BOOL", "BOOL"}, [][]any{{"btree_gin", "1.3", true, true, true}}))
				scalarGraph(t, p, b, conn, schema)
				// Retain the preceding families, not merely the aggregate class count.
				networkGraph(t, p, b, conn, schema)
				classRoutes(t, p, b, conn, fixture.Select("SELECT c.opcname,f.opfname FROM pg_opclass c JOIN pg_opfamily f ON f.oid=c.opcfamily WHERE c.opcmethod=2742 AND c.opcnamespace=$1::regnamespace AND c.opcname IN"+compoundClasses+" ORDER BY c.opcname", []string{"opcname", "opfname"}, []string{"NAME", "NAME"}, [][]any{{"bit_ops", "bit_ops"}, {"enum_ops", "enum_ops"}, {"money_ops", "money_ops"}, {"varbit_ops", "varbit_ops"}}, schema))
				for _, tc := range scalarCases {
					table := "scalar_class_" + tc.typ
					run(fixture.Exec("CREATE TABLE "+table+"(id integer NOT NULL,v "+tc.typ+")", 0))
					scalarSeed(run, table, tc)
					run(fixture.Exec("CREATE INDEX "+table+"_i ON "+table+" USING gin(v "+schema+"."+tc.typ+"_ops)", 0))
					scalarValueRoutes(t, p, b, conn, scalarValueStep(b, "SELECT id,v FROM "+table+" ORDER BY id", tc, scalarRows(b, tc)))
					run(fixture.Exec("CREATE TABLE "+table+"_empty(id integer NOT NULL,v "+tc.typ+")", 0))
					run(fixture.Prepared(fixture.Exec("CREATE INDEX "+table+"_empty_i ON "+table+"_empty USING gin(v)", 0)))
					scalarValueRoutes(t, p, b, conn, scalarValueStep(b, "SELECT id,v FROM "+table+"_empty ORDER BY id", tc, nil))
					scalarSeed(run, table+"_empty", tc)
					scalarValueRoutes(t, p, b, conn, scalarValueStep(b, "SELECT id,v FROM "+table+"_empty ORDER BY id", tc, scalarRows(b, tc)))
					for i, op := range []string{"<", "<=", "=", ">=", ">", "<>"} {
						classRoutes(t, p, b, conn, ginIDs("SELECT id FROM "+table+"_empty WHERE v"+op+"$1::"+tc.typ+" ORDER BY id", scalarTruth(tc)[i], tc.boundary))
					}
				}
				run(amCount("SELECT count(*) FROM pg_amop a JOIN pg_opclass c ON c.opcfamily=a.amopfamily WHERE c.opcmethod=2742 AND c.opcintype IN"+scalarInputs+" AND (a.amopstrategy IN(6,15) OR a.amoppurpose<>'s')", 0))
				run(amCount("SELECT count(*) FROM pg_amproc a JOIN pg_opclass c ON c.opcfamily=a.amprocfamily WHERE c.opcmethod=2742 AND c.opcintype IN"+scalarInputs+" AND a.amprocnum=6", 0))
			})
		})
	}
}
