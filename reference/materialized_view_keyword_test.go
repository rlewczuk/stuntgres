package reference_test

import "testing"

func TestReferenceMaterializedViewKeywordTypes(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
			p := newReferenceScenario(t)
			for _, backend := range p.backends() {
				t.Run(backend.name, func(t *testing.T) {
					conn, execute := queryTableCreateSession(t, p, backend)
					defer func() {
						if _, err := conn.ExecContext(p.ctx, "DROP SCHEMA IF EXISTS materialized CASCADE"); err != nil {
							t.Error(err)
						}
					}()
					run := func(step referenceStep) { step.prepared = prepared; execute(step) }
					dropType := func(name string) {
						if backend.pg {
							run(referenceExec("DROP TYPE "+name, 0))
						} else {
							// DROP TYPE is an existing native utility exclusion, not
							// part of unreserved type-name legality. Fixture reset and
							// supported schema CASCADE own native type cleanup.
							run(referenceImplicitError("DROP TYPE "+name, referenceDiagnostic{
								Code: "0A000", Message: "statement is unsupported", Position: "1",
							}))
						}
					}
					run(referenceSelect("SELECT 7 AS materialized", []string{"materialized"}, []string{"INT4"}, [][]any{{int64(7)}}))
					run(referenceExec("CREATE DOMAIN materialized AS integer", 0))
					run(referenceExec("CREATE TABLE mv_keyword_domain(v materialized)", 0))
					run(referenceExec("INSERT INTO mv_keyword_domain VALUES(7)", 1))
					run(referenceSelect("SELECT v FROM mv_keyword_domain", []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}}))
					run(referenceSelect("SELECT 8::materialized AS v,CAST(9 AS materialized) AS w", []string{"v", "w"}, []string{"INT4", "INT4"}, [][]any{{int64(8), int64(9)}}))
					run(referenceExec("DROP TABLE mv_keyword_domain", 0))
					run(referenceExec("DROP DOMAIN materialized", 0))
					run(referenceExec("CREATE TYPE materialized AS ENUM ('keyword_value')", 0))
					run(referenceExec("CREATE TABLE mv_keyword_enum(v materialized)", 0))
					run(referenceExec("INSERT INTO mv_keyword_enum VALUES('keyword_value')", 1))
					run(referenceSelect("SELECT v::text AS v FROM mv_keyword_enum", []string{"v"}, []string{"TEXT"}, [][]any{{"keyword_value"}}))
					run(referenceSelect("SELECT CAST('keyword_value' AS materialized)::text AS v", []string{"v"}, []string{"TEXT"}, [][]any{{"keyword_value"}}))
					run(referenceExec("DROP TABLE mv_keyword_enum", 0))
					dropType("materialized")
					run(referenceExec("CREATE SCHEMA materialized", 0))
					run(referenceExec("CREATE DOMAIN materialized.mv_keyword_domain AS integer", 0))
					run(referenceExec("CREATE TABLE mv_keyword_schema(v materialized.mv_keyword_domain)", 0))
					run(referenceExec("INSERT INTO mv_keyword_schema VALUES(10)", 1))
					run(referenceSelect("SELECT v FROM mv_keyword_schema", []string{"v"}, []string{"INT4"}, [][]any{{int64(10)}}))
					run(referenceSelect("SELECT 11::materialized.mv_keyword_domain AS v,CAST(12 AS materialized.mv_keyword_domain) AS w", []string{"v", "w"}, []string{"INT4", "INT4"}, [][]any{{int64(11), int64(12)}}))
					run(referenceExec("DROP TABLE mv_keyword_schema", 0))
					run(referenceExec("DROP DOMAIN materialized.mv_keyword_domain", 0))
					run(referenceExec("CREATE TYPE materialized.mv_keyword_type AS ENUM ('schema_value')", 0))
					run(referenceExec("CREATE TABLE mv_keyword_schema_type(v materialized.mv_keyword_type)", 0))
					run(referenceExec("INSERT INTO mv_keyword_schema_type VALUES('schema_value')", 1))
					run(referenceSelect("SELECT v::text AS v FROM mv_keyword_schema_type", []string{"v"}, []string{"TEXT"}, [][]any{{"schema_value"}}))
					run(referenceSelect("SELECT 'schema_value'::materialized.mv_keyword_type::text AS v", []string{"v"}, []string{"TEXT"}, [][]any{{"schema_value"}}))
					run(referenceExec("DROP TABLE mv_keyword_schema_type", 0))
					dropType("materialized.mv_keyword_type")
					run(referenceExec("DROP SCHEMA materialized CASCADE", 0))
				})
			}
		})
	}
}
