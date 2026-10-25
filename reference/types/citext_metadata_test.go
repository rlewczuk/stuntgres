package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func citextCatalog(t *testing.T, p *fixture.Pair, b fixture.Backend, c fixture.Executor) {
	t.Helper()
	// Read this backend's current identities before joining catalogs. These are
	// locators, not expected facts or OIDs shared across backends/restores.
	readIDs := func(query string, ids ...*int64) {
		t.Helper()
		rows, err := c.QueryContext(p.Ctx, query)
		if err != nil {
			t.Fatalf("citext catalog identities: %s: %v", query, err)
		}
		defer rows.Close()
		if !rows.Next() {
			t.Fatalf("citext catalog identities: %s: missing row: %v", query, rows.Err())
		}
		dest := make([]any, len(ids))
		for i, id := range ids {
			dest[i] = id
		}
		if err := rows.Scan(dest...); err != nil {
			t.Fatalf("citext catalog identities: %s: %v", query, err)
		}
		for _, id := range ids {
			if *id <= 0 {
				t.Fatalf("citext catalog identities: %s: nonpositive OID %d", query, *id)
			}
		}
		if rows.Next() {
			t.Fatalf("citext catalog identities: %s: multiple rows", query)
		}
		if err := rows.Err(); err != nil {
			t.Fatalf("citext catalog identities: %s: %v", query, err)
		}
	}
	var scalar, array, input, output, extension int64
	readIDs("SELECT oid::bigint,typarray::bigint,typinput::oid::bigint,typoutput::oid::bigint FROM pg_type WHERE oid='citext'::regtype::oid", &scalar, &array, &input, &output)
	readIDs("SELECT oid::bigint FROM pg_extension WHERE extname='citext'", &extension)
	run := func(s fixture.Step) {
		fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
	}
	run(fixture.Select("SELECT t.typname,t.typtype,t.typlen,t.typbyval,t.typcategory,t.typispreferred,t.typcollation::bigint,t.typtypmod,t.typalign,t.typstorage,i.proname AS input,o.proname AS output FROM (SELECT * FROM pg_type WHERE oid=$1::oid) t JOIN (SELECT * FROM pg_proc WHERE oid=$2::oid) i ON i.oid=t.typinput JOIN (SELECT * FROM pg_proc WHERE oid=$3::oid) o ON o.oid=t.typoutput", []string{"typname", "typtype", "typlen", "typbyval", "typcategory", "typispreferred", "typcollation", "typtypmod", "typalign", "typstorage", "input", "output"}, []string{"NAME", "CHAR", "INT2", "BOOL", "CHAR", "BOOL", "INT8", "INT4", "CHAR", "CHAR", "NAME", "NAME"}, [][]any{{"citext", "b", int64(-1), false, "S", false, int64(100), int64(-1), "i", "x", "citextin", "citextout"}}, scalar, input, output))
	run(fixture.Select("SELECT t.oid>0 AS scalar_positive,a.oid>0 AS array_positive,t.oid<>a.oid AS distinct_ids,a.typname,a.typelem=t.oid AS reciprocal FROM (SELECT * FROM pg_type WHERE oid=$1::oid) t JOIN (SELECT * FROM pg_type WHERE oid=$2::oid) a ON a.oid=t.typarray", []string{"scalar_positive", "array_positive", "distinct_ids", "typname", "reciprocal"}, []string{"BOOL", "BOOL", "BOOL", "NAME", "BOOL"}, [][]any{{true, true, true, "_citext", true}}, scalar, array))
	run(fixture.Select("SELECT d.deptype FROM (SELECT * FROM pg_depend WHERE classid=1247 AND objid=$2::oid AND refclassid=1247 AND refobjid=$1::oid) d JOIN (SELECT * FROM pg_type WHERE oid=$1::oid) t ON d.objid=t.typarray", []string{"deptype"}, []string{"CHAR"}, [][]any{{"i"}}, scalar, array))
	run(fixture.Select("SELECT d.deptype,e.extname FROM (SELECT * FROM pg_depend WHERE classid=1247 AND objid=$1::oid AND refclassid=3079) d JOIN (SELECT * FROM pg_extension WHERE oid=$2::oid) e ON e.oid=d.refobjid", []string{"deptype", "extname"}, []string{"CHAR", "NAME"}, [][]any{{"e", "citext"}}, scalar, extension))
	run(fixture.Select("SELECT p.proname,p.prorettype='citext'::regtype::oid AS returns_citext,p.proisstrict,p.provolatile,p.proparallel,d.deptype,p.proowner=t.typowner AS same_owner,p.pronamespace=t.typnamespace AS same_namespace FROM (SELECT * FROM pg_type WHERE oid=$1::oid) t JOIN (SELECT * FROM pg_proc WHERE oid IN ($2::oid,$3::oid)) p ON p.oid IN (t.typinput::oid,t.typoutput::oid) JOIN (SELECT * FROM pg_depend WHERE classid=1255 AND objid IN ($2::oid,$3::oid) AND refclassid=3079) d ON d.objid=p.oid JOIN (SELECT * FROM pg_extension WHERE oid=$4::oid) e ON e.oid=d.refobjid WHERE e.extname='citext' ORDER BY p.proname", []string{"proname", "returns_citext", "proisstrict", "provolatile", "proparallel", "deptype", "same_owner", "same_namespace"}, []string{"NAME", "BOOL", "BOOL", "CHAR", "CHAR", "CHAR", "BOOL", "BOOL"}, [][]any{{"citextin", true, true, "i", "s", "e", true, true}, {"citextout", false, true, "i", "s", "e", true, true}}, scalar, input, output, extension))
	run(fixture.Select("SELECT t.typinput::oid='public.citextin(cstring)'::regprocedure::oid AS input_signature,t.typoutput::oid='public.citextout(public.citext)'::regprocedure::oid AS output_signature,p.pronargs AS input_arguments,o.pronargs AS output_arguments,o.prorettype='cstring'::regtype::oid AS returns_cstring FROM (SELECT * FROM pg_type WHERE oid=$1::oid) t JOIN (SELECT * FROM pg_proc WHERE oid=$2::oid) p ON p.oid=t.typinput JOIN (SELECT * FROM pg_proc WHERE oid=$3::oid) o ON o.oid=t.typoutput", []string{"input_signature", "output_signature", "input_arguments", "output_arguments", "returns_cstring"}, []string{"BOOL", "BOOL", "INT2", "INT2", "BOOL"}, [][]any{{true, true, int64(1), int64(1), true}}, scalar, input, output))
	if !b.PG {
		run(fixture.Select("SELECT typreceive::oid::bigint,typsend::oid::bigint FROM pg_type WHERE oid='citext'::regtype::oid", []string{"typreceive", "typsend"}, []string{"INT8", "INT8"}, [][]any{{int64(0), int64(0)}}))
	}
}

func TestReferenceCitextMetadata(t *testing.T) {
	citextScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		citextInstall(t, p, b, c)
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE citext_metadata(id integer NOT NULL,v citext,n citext NOT NULL)", 0))
		run(fixture.Exec("CREATE TABLE citext_vacant(v citext NOT NULL)", 0))
		run(fixture.Exec("INSERT INTO citext_metadata VALUES (1,'MiXeD',''),(2,NULL,'Kept')", 2))
		for _, tc := range []struct {
			q      string
			names  []string
			nulls  []bool
			values [][]any
		}{
			{"SELECT v,n FROM citext_metadata ORDER BY id", []string{"v", "n"}, []bool{true, false}, [][]any{{"MiXeD", ""}, {nil, "Kept"}}},
			{"SELECT v,n FROM citext_metadata WHERE id=99", []string{"v", "n"}, []bool{true, false}, [][]any{}},
			{"SELECT v AS alias FROM citext_metadata WHERE id=2", []string{"alias"}, []bool{true}, [][]any{{nil}}},
			{"SELECT v FROM citext_vacant", []string{"v"}, []bool{false}, [][]any{}},
			{"SELECT NULL::citext AS v", []string{"v"}, nil, [][]any{{nil}}},
			{"SELECT 'MiXeD'::citext AS v", []string{"v"}, nil, [][]any{{"MiXeD"}}},
		} {
			for _, prepared := range []bool{false, true} {
				s := fixture.CitextSelect(b, tc.q, tc.names, tc.values)
				s.Prepared = prepared
				s.CheckMetadata = func(t *testing.T, rows *sql.Rows) { citextMetadata(t, b.PG, rows, tc.names, tc.nulls) }
				run(s)
			}
		}
		run(fixture.Select("SELECT a.attname,a.atttypid='citext'::regtype::oid AS same_type,a.attnotnull,a.atttypmod,a.attcollation::bigint FROM pg_attribute a WHERE a.attrelid='citext_metadata'::regclass::oid AND a.attnum>1 AND NOT a.attisdropped ORDER BY a.attnum", []string{"attname", "same_type", "attnotnull", "atttypmod", "attcollation"}, []string{"NAME", "BOOL", "BOOL", "INT4", "INT8"}, [][]any{{"v", true, false, int64(-1), int64(100)}, {"n", true, true, int64(-1), int64(100)}}))
		citextCatalog(t, p, b, c)
		run(fixture.Exec("CREATE TABLE citext_collation(id integer,v citext,c citext COLLATE \"C\",p citext COLLATE \"POSIX\")", 0))
		run(fixture.Exec("INSERT INTO citext_collation VALUES (1,$1,$1,$1)", 1, "  Straße İ Σς é🙂  "))
		run(fixture.CitextSelect(b, "SELECT v,c,p FROM citext_collation ORDER BY id", []string{"v", "c", "p"}, [][]any{{"  Straße İ Σς é🙂  ", "  Straße İ Σς é🙂  ", "  Straße İ Σς é🙂  "}}))
		run(fixture.Select("SELECT a.attname,c.collname FROM pg_attribute a JOIN pg_collation c ON c.oid=a.attcollation WHERE a.attrelid='citext_collation'::regclass::oid AND a.attnum>1 ORDER BY a.attnum", []string{"attname", "collname"}, []string{"NAME", "NAME"}, [][]any{{"v", "default"}, {"c", "C"}, {"p", "POSIX"}}))
	})
}
