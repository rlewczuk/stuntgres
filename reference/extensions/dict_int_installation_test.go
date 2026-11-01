package reference_test

import (
	"database/sql"
	"strconv"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func dictIntInstallation(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, schema string) {
	t.Helper()
	ext := dictIntID(t, p, c, "SELECT oid::bigint FROM pg_extension WHERE extname='dict_int'")
	ns := dictIntID(t, p, c, "SELECT oid::bigint FROM pg_namespace WHERE nspname=$1", schema)
	initOID := dictIntID(t, p, c, "SELECT oid::bigint FROM pg_proc WHERE pronamespace=$1 AND proname='dintdict_init'", ns)
	lexize := dictIntID(t, p, c, "SELECT oid::bigint FROM pg_proc WHERE pronamespace=$1 AND proname='dintdict_lexize'", ns)
	template := dictIntID(t, p, c, "SELECT oid::bigint FROM pg_ts_template WHERE tmplnamespace=$1 AND tmplname='intdict_template'", ns)
	dictionary := dictIntID(t, p, c, "SELECT oid::bigint FROM pg_ts_dict WHERE dictnamespace=$1 AND dictname='intdict'", ns)
	seen := map[int64]bool{}
	for _, id := range []int64{ext, initOID, lexize, template, dictionary} {
		if seen[id] {
			t.Fatal("installation reused a member identity", id)
		}
		seen[id] = true
	}
	dictIntSelect(t, p, b, c, "SELECT extname,extversion,extrelocatable,extnamespace FROM pg_extension WHERE oid=$1", []string{"extname", "extversion", "extrelocatable", "extnamespace"}, []string{"NAME", "TEXT", "BOOL", "OID"}, [][]any{{"dict_int", "1.0", true, ns}}, ext)
	dictIntSelect(t, p, b, c, "SELECT proname,pronargs,proargtypes::text,prorettype,proisstrict,provolatile,proparallel,probin,prosrc,l.lanname FROM pg_proc p JOIN pg_language l ON l.oid=p.prolang WHERE p.oid=$1 OR p.oid=$2 ORDER BY proname", []string{"proname", "pronargs", "proargtypes", "prorettype", "proisstrict", "provolatile", "proparallel", "probin", "prosrc", "lanname"}, []string{"NAME", "INT2", "TEXT", "OID", "BOOL", "CHAR", "CHAR", "TEXT", "TEXT", "NAME"}, [][]any{{"dintdict_init", int64(1), "2281", int64(2281), true, "v", "u", "$libdir/dict_int", "dintdict_init", "c"}, {"dintdict_lexize", int64(4), "2281 2281 2281 2281", int64(2281), true, "v", "u", "$libdir/dict_int", "dintdict_lexize", "c"}}, initOID, lexize)
	dictIntSelect(t, p, b, c, "SELECT tmplname,tmplnamespace,tmplinit::oid,tmpllexize::oid FROM pg_ts_template WHERE oid=$1", []string{"tmplname", "tmplnamespace", "tmplinit", "tmpllexize"}, []string{"NAME", "OID", "OID", "OID"}, [][]any{{"intdict_template", ns, initOID, lexize}}, template)
	dictIntSelect(t, p, b, c, "SELECT dictname,dictnamespace,dicttemplate,dictinitoption FROM pg_ts_dict WHERE oid=$1", []string{"dictname", "dictnamespace", "dicttemplate", "dictinitoption"}, []string{"NAME", "OID", "OID", "TEXT"}, [][]any{{"intdict", ns, template, nil}}, dictionary)
	dictIntSelect(t, p, b, c, "SELECT classid::bigint,objid::bigint FROM pg_depend WHERE refclassid=3079 AND refobjid=$1 AND deptype='e' ORDER BY classid,objid", []string{"classid", "objid"}, []string{"INT8", "INT8"}, [][]any{{int64(1255), initOID}, {int64(1255), lexize}, {int64(3600), dictionary}, {int64(3764), template}}, ext)
	dictIntSelect(t, p, b, c, "SELECT count(*) FROM pg_depend WHERE classid=3600 AND objid=$1 AND refclassid=3764 AND refobjid=$2 AND deptype='n'", []string{"count"}, []string{"INT8"}, [][]any{{int64(1)}}, dictionary, template)
	dictIntSelect(t, p, b, c, "SELECT count(*) FROM pg_depend WHERE classid=3764 AND objid=$1 AND refclassid=1255 AND (refobjid=$2 OR refobjid=$3) AND deptype='n'", []string{"count"}, []string{"INT8"}, [][]any{{int64(2)}}, template, initOID, lexize)
	dictIntLexemes(t, p, b, c, "{123456}", schema+".intdict", "1234567")
	owner := dictIntID(t, p, c, "SELECT extowner::bigint FROM pg_extension WHERE oid=$1", ext)
	for _, prepared := range []bool{false, true} {
		for _, present := range []bool{false, true} {
			predicate, count := "false", 0
			var expected [][]any
			if present {
				predicate, count = "true", 1
				row := []any{dictionary, "intdict", ns, owner, template, nil}
				if b.PG {
					for _, i := range []int{0, 2, 3, 4} {
						row[i] = strconv.FormatInt(row[i].(int64), 10)
					}
				}
				expected = [][]any{row}
			}
			s := fixture.Select("SELECT oid,dictname,dictnamespace,dictowner,dicttemplate,dictinitoption FROM pg_ts_dict WHERE dictname='intdict' AND dictnamespace="+strconv.FormatInt(ns, 10)+" AND "+predicate, tsDictNames, tsDictTypes, expected)
			s.Prepared = prepared
			s.CheckMetadata = func(t *testing.T, rows *sql.Rows) { tsDictMetadata(t, rows, b.PG) }
			dictIntRun(t, p, b, c, s)
			extensionMemberMetadata(t, p, c, "SELECT oid,proname,pronamespace,proowner,pronargs,prorettype,provolatile,proisstrict,proparallel FROM pg_proc WHERE (oid="+strconv.FormatInt(initOID, 10)+" OR oid="+strconv.FormatInt(lexize, 10)+") AND "+predicate, prepared, b.PG, []string{"oid", "proname", "pronamespace", "proowner", "pronargs", "prorettype", "provolatile", "proisstrict", "proparallel"}, []string{"OID", "NAME", "OID", "OID", "INT2", "OID", "CHAR", "BOOL", "CHAR"}, false, count*2)
			extensionMemberMetadata(t, p, c, "SELECT oid,tmplname,tmplnamespace FROM pg_ts_template WHERE oid="+strconv.FormatInt(template, 10)+" AND "+predicate, prepared, b.PG, []string{"oid", "tmplname", "tmplnamespace"}, []string{"OID", "NAME", "OID"}, false, count)
		}
	}
}

func TestReferenceDictIntInstallation(t *testing.T) {
	dictIntCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		const availability = "SELECT name,default_version,installed_version,comment FROM pg_available_extensions WHERE name='dict_int'"
		stmt, err := c.PrepareContext(p.Ctx, availability)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: availability}
		check := func(installed any, enabled bool) {
			for _, e := range []fixture.Executor{c, held} {
				dictIntRun(t, p, b, e, fixture.Select(availability, []string{"name", "default_version", "installed_version", "comment"}, []string{"NAME", "TEXT", "TEXT", "TEXT"}, [][]any{{"dict_int", "1.0", installed, "text search dictionary template for integers"}}))
			}
			dictIntSelect(t, p, b, c, "SELECT name,version,installed,superuser,trusted,relocatable,schema,requires,comment FROM pg_available_extension_versions WHERE name='dict_int' ORDER BY version", []string{"name", "version", "installed", "superuser", "trusted", "relocatable", "schema", "requires", "comment"}, []string{"NAME", "TEXT", "BOOL", "BOOL", "BOOL", "BOOL", "NAME", "_NAME", "TEXT"}, [][]any{{"dict_int", "1.0", enabled, true, true, true, nil, nil, "text search dictionary template for integers"}})
		}
		check(nil, false)
		dictIntExec(t, p, b, c, "BEGIN", 0)
		dictIntExec(t, p, b, c, "CREATE EXTENSION dict_int VERSION '1.0'", 0)
		check("1.0", true)
		dictIntExec(t, p, b, c, "ROLLBACK", 0)
		check(nil, false)
		dictIntExec(t, p, b, c, "CREATE EXTENSION dict_int", 0)
		check("1.0", true)
		dictIntInstallation(t, p, b, c, "public")
		if !b.PG {
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			check(nil, false)
			dictIntExec(t, p, b, c, "CREATE EXTENSION dict_int", 0)
			dictIntInstallation(t, p, b, c, "public")
		}
	})
}
