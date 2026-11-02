package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceBtreeGINTextClasses(t *testing.T) {
	for _, schema := range []string{"public", "text_classes_alt"} {
		t.Run(schema, func(t *testing.T) {
			if schema != "public" {
				fixture.SchemaReferenceCleanup(t, schema)
			}
			classBackends(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				conn, run := classConn(t, p, b)
				compoundCleanup(t, p, b, conn)
				run(amCount("SELECT count(*) FROM pg_opclass WHERE opcmethod=2742 AND opcname IN"+textClasses, 0))
				run(fixture.Exec("CREATE TABLE text_class_retry(id integer NOT NULL,v text)", 0))
				d := fixture.ControlsReferenceDiagnostic("42704", `data type text has no default operator class for access method "gin"`)
				d.Hint = compoundMissingDefaultHint
				ginUtilityError(t, p, b, conn, "CREATE INDEX text_class_retry_i ON text_class_retry USING gin(v)", d)
				compoundIndexAbsent(run, "text_class_retry", "text_class_retry_i", 0)
				if schema != "public" {
					run(fixture.Exec("CREATE SCHEMA "+schema, 0))
				}
				run(fixture.Prepared(fixture.Exec("CREATE EXTENSION btree_gin WITH SCHEMA "+schema+" VERSION '1.3'", 0)))
				classRoutes(t, p, b, conn, fixture.Select("SELECT e.extversion,e.extrelocatable,n.nspname,obj_description(e.oid,'pg_extension') AS comment FROM pg_extension e JOIN pg_namespace n ON n.oid=e.extnamespace WHERE e.extname='btree_gin'", []string{"extversion", "extrelocatable", "nspname", "comment"}, []string{"TEXT", "BOOL", "NAME", "TEXT"}, [][]any{{"1.3", true, schema, "support for indexing common datatypes in GIN"}}))
				classRoutes(t, p, b, conn, fixture.Select("SELECT name,version,superuser,trusted,relocatable FROM pg_available_extension_versions WHERE name='btree_gin' AND version='1.3'", []string{"name", "version", "superuser", "trusted", "relocatable"}, []string{"NAME", "TEXT", "BOOL", "BOOL", "BOOL"}, [][]any{{"btree_gin", "1.3", true, true, true}}))
				textGraph(t, p, b, conn, schema)
				run(fixture.Exec("CREATE INDEX text_class_retry_i ON text_class_retry USING gin(v "+schema+".text_ops)", 0))
				for _, tc := range textCases {
					table := "text_class_" + tc.typ
					run(fixture.Exec("CREATE TABLE "+table+"(id integer NOT NULL,v "+textSQLType(tc)+")", 0))
					textSeed(run, table, tc)
					run(fixture.Exec("CREATE INDEX "+table+"_explicit ON "+table+" USING gin(v "+schema+"."+tc.typ+"_ops)", 0))
					run(fixture.Exec("CREATE TABLE "+table+"_empty(id integer NOT NULL,v "+textSQLType(tc)+")", 0))
					run(fixture.Exec("CREATE INDEX "+table+"_default ON "+table+"_empty USING gin(v)", 0))
					textSeed(run, table+"_empty", tc)
					classRoutes(t, p, b, conn, ginIDs("SELECT id FROM "+table+"_empty WHERE v=$1::"+textSQLType(tc)+" ORDER BY id", textTruth(tc)[2], textArgument(tc, tc.boundary)))
				}
			})
		})
	}
}
