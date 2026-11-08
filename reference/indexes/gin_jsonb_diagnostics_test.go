package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

// TestReferenceGINJSONBDiagnostics pins the PostgreSQL 18.6 jsonb GIN DDL
// diagnostics. No failure may leave a relation, index or dependency behind.
func TestReferenceGINJSONBDiagnostics(t *testing.T) {
	const hint = "You must specify an operator class for the index or define a default operator class for the data type."
	for _, tc := range []struct {
		name, clause, code, message, hint string
	}{
		{"jsonb_array_class", "INDEX gin_bad ON gin_jdiag USING gin(j array_ops)", "42804", `operator class "array_ops" does not accept data type jsonb`, ""},
		{"text_jsonb_class", "INDEX gin_bad ON gin_jdiag USING gin(t jsonb_ops)", "42804", `operator class "jsonb_ops" does not accept data type text`, ""},
		{"array_path_class", "INDEX gin_bad ON gin_jdiag USING gin(a jsonb_path_ops)", "42804", `operator class "jsonb_path_ops" does not accept data type integer[]`, ""},
		{"integer_default", "INDEX gin_bad ON gin_jdiag USING gin(n)", "42704", `data type integer has no default operator class for access method "gin"`, hint},
		{"key_option", "INDEX gin_bad ON gin_jdiag USING gin(j jsonb_ops(siglen=1))", "22023", `operator class jsonb_ops has no options`, ""},
		{"fillfactor", "INDEX gin_bad ON gin_jdiag USING gin(j) WITH (fillfactor=80)", "22023", `unrecognized parameter "fillfactor"`, ""},
		{"collation", `INDEX gin_bad ON gin_jdiag USING gin(j COLLATE "C")`, "42804", `collations are not supported by type jsonb`, ""},
		{"desc", "INDEX gin_bad ON gin_jdiag USING gin(j DESC)", "0A000", `access method "gin" does not support ASC/DESC options`, ""},
		{"unique", "UNIQUE INDEX gin_bad ON gin_jdiag USING gin(j)", "0A000", `access method "gin" does not support unique indexes`, ""},
		{"include", "INDEX gin_bad ON gin_jdiag USING gin(j) INCLUDE(id)", "0A000", `access method "gin" does not support included columns`, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			classBackends(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				conn, run := classConn(t, p, b)
				run(fixture.Exec("CREATE TABLE gin_jdiag(id integer,j jsonb,a integer[],n integer,t text)", 0))
				run(fixture.Exec(`INSERT INTO gin_jdiag VALUES(1,'{"cat":1}','{1}',7,'x')`, 1))
				d := fixture.ControlsReferenceDiagnostic(tc.code, tc.message)
				d.Hint = tc.hint
				for _, prefix := range []string{"", "/*é*/ "} {
					ginUtilityError(t, p, b, conn, prefix+"CREATE "+tc.clause, d)
				}
				run(fixture.Select("SELECT relname FROM pg_class WHERE relname='gin_bad' AND relnamespace='public'::regnamespace", []string{"relname"}, []string{"NAME"}, nil))
				run(amCount("SELECT count(*) FROM pg_index WHERE indrelid='gin_jdiag'::regclass", 0))
				run(amCount("SELECT count(*) FROM pg_depend d JOIN pg_class c ON c.oid=d.objid WHERE d.classid='pg_class'::regclass AND c.relname='gin_bad' AND c.relnamespace='public'::regnamespace", 0))
				run(ginIDs(`SELECT id FROM gin_jdiag WHERE j @> '{"cat":1}' ORDER BY id`, []int64{1}))
				run(fixture.Exec("CREATE INDEX gin_jdiag_ok ON gin_jdiag USING gin(j)", 0))
				run(fixture.Exec("DROP INDEX gin_jdiag_ok", 0))
			})
		})
	}
}