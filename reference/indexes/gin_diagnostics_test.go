package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceGINDiagnostics(t *testing.T) {
	const hint = "You must specify an operator class for the index or define a default operator class for the data type."
	for _, tc := range []struct {
		name, clause, code, message, hint string
	}{
		{"integer", "INDEX gin_bad ON gin_diag USING gin(n)", "42704", `data type integer has no default operator class for access method "gin"`, hint},
		{"tsquery", "INDEX gin_bad ON gin_diag USING gin(q)", "42704", `data type tsquery has no default operator class for access method "gin"`, hint},
		{"missing", "INDEX gin_bad ON gin_diag USING gin(a missing_ops)", "42704", `operator class "missing_ops" does not exist for access method "gin"`, ""},
		{"qualified_missing", "INDEX gin_bad ON gin_diag USING gin(a pg_catalog.missing_ops)", "42704", `operator class "pg_catalog.missing_ops" does not exist for access method "gin"`, ""},
		{"btree_class", "INDEX gin_bad ON gin_diag USING gin(a int4_ops)", "42704", `operator class "int4_ops" does not exist for access method "gin"`, ""},
		{"scalar_array_class", "INDEX gin_bad ON gin_diag USING gin(n array_ops)", "42804", `operator class "array_ops" does not accept data type integer`, ""},
		{"array_vector_class", "INDEX gin_bad ON gin_diag USING gin(a tsvector_ops)", "42804", `operator class "tsvector_ops" does not accept data type integer[]`, ""},
		{"vector_array_class", "INDEX gin_bad ON gin_diag USING gin(v array_ops)", "42804", `operator class "array_ops" does not accept data type tsvector`, ""},
		{"qualified_wrong_class", "INDEX gin_bad ON gin_diag USING gin(n pg_catalog.array_ops)", "42804", `operator class "pg_catalog.array_ops" does not accept data type integer`, ""},
		{"asc", "INDEX gin_bad ON gin_diag USING gin(a ASC)", "0A000", `access method "gin" does not support ASC/DESC options`, ""},
		{"desc", "INDEX gin_bad ON gin_diag USING gin(a DESC)", "0A000", `access method "gin" does not support ASC/DESC options`, ""},
		{"nulls_first", "INDEX gin_bad ON gin_diag USING gin(a NULLS FIRST)", "0A000", `access method "gin" does not support NULLS FIRST/LAST options`, ""},
		{"nulls_last", "INDEX gin_bad ON gin_diag USING gin(a NULLS LAST)", "0A000", `access method "gin" does not support NULLS FIRST/LAST options`, ""},
		{"unique", "UNIQUE INDEX gin_bad ON gin_diag USING gin(a)", "0A000", `access method "gin" does not support unique indexes`, ""},
		{"include", "INDEX gin_bad ON gin_diag USING gin(a) INCLUDE(n)", "0A000", `access method "gin" does not support included columns`, ""},
		{"array_key_option", "INDEX gin_bad ON gin_diag USING gin(a array_ops(siglen=1))", "22023", `operator class array_ops has no options`, ""},
		{"vector_key_option", "INDEX gin_bad ON gin_diag USING gin(v tsvector_ops(siglen=1))", "22023", `operator class tsvector_ops has no options`, ""},
		{"later_key", "INDEX gin_bad ON gin_diag USING gin(a,v array_ops)", "42804", `operator class "array_ops" does not accept data type tsvector`, ""},
		{"option_before_class", "INDEX gin_bad ON gin_diag USING gin(a missing_ops) WITH(unknown=1)", "22023", `unrecognized parameter "unknown"`, ""},
		{"class_before_direction", "INDEX gin_bad ON gin_diag USING gin(a tsvector_ops DESC)", "42804", `operator class "tsvector_ops" does not accept data type integer[]`, ""},
		{"unique_before_option", "UNIQUE INDEX gin_bad ON gin_diag USING gin(a) WITH(unknown=1)", "0A000", `access method "gin" does not support unique indexes`, ""},
		{"include_before_option", "INDEX gin_bad ON gin_diag USING gin(a) INCLUDE(n) WITH(unknown=1)", "0A000", `access method "gin" does not support included columns`, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			classBackends(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				conn, run := classConn(t, p, b)
				run(fixture.Exec("CREATE TABLE gin_diag(id integer,a integer[],v tsvector,n integer,q tsquery)", 0))
				run(fixture.Exec("INSERT INTO gin_diag VALUES(1,'{1,2}','cat:1',7,'cat')", 1))
				d := fixture.ControlsReferenceDiagnostic(tc.code, tc.message)
				d.Hint = tc.hint
				for _, prefix := range []string{"", "/*é*/ "} {
					ginUtilityError(t, p, b, conn, prefix+"CREATE "+tc.clause, d)
				}
				run(fixture.Select("SELECT relname FROM pg_class WHERE relname='gin_bad' AND relnamespace='public'::regnamespace", []string{"relname"}, []string{"NAME"}, nil))
				run(amCount("SELECT count(*) FROM pg_index WHERE indrelid='gin_diag'::regclass", 0))
				run(amCount("SELECT count(*) FROM pg_depend d JOIN pg_class c ON c.oid=d.objid WHERE d.classid='pg_class'::regclass AND c.relname='gin_bad' AND c.relnamespace='public'::regnamespace", 0))
				run(ginIDs("SELECT id FROM gin_diag WHERE a @> '{2}'::integer[] AND v @@ 'cat'::tsquery ORDER BY id", []int64{1}))
				run(fixture.Exec("CREATE INDEX gin_bad ON gin_diag USING gin(a,v)", 0))
			})
		})
	}
	classBackends(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		conn, run := classConn(t, p, b)
		run(fixture.Exec("CREATE TABLE gin_exclusion(a integer[])", 0))
		ginUtilityError(t, p, b, conn, "ALTER TABLE gin_exclusion ADD CONSTRAINT gin_exclusion_bad EXCLUDE USING gin(a WITH &&)", fixture.ControlsReferenceDiagnostic("0A000", `access method "gin" does not support exclusion constraints`))
		run(fixture.Select("SELECT conname FROM pg_constraint WHERE conrelid='gin_exclusion'::regclass", []string{"conname"}, []string{"NAME"}, nil))
	})
	classBackends(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		_, run := classConn(t, p, b)
		run(fixture.Exec("CREATE TABLE gin_adjacent(id integer,a integer[],j jsonb)", 0))
		run(fixture.Exec(`INSERT INTO gin_adjacent VALUES(1,'{1}','{"cat":1}')`, 1))
		for _, key := range []string{"j", "j pg_catalog.jsonb_ops", "j pg_catalog.jsonb_path_ops"} {
			run(fixture.Exec("CREATE INDEX gin_adjacent_i ON gin_adjacent USING gin("+key+")", 0))
			run(ginIDs(`SELECT id FROM gin_adjacent WHERE j @> '{"cat":1}'::jsonb ORDER BY id`, []int64{1}))
			run(fixture.Exec("DROP INDEX gin_adjacent_i", 0))
			run(amCount("SELECT count(*) FROM pg_index WHERE indrelid='gin_adjacent'::regclass", 0))
		}
		run(fixture.Exec("CREATE INDEX gin_adjacent_i ON gin_adjacent USING gin(a)", 0))
	})
}
