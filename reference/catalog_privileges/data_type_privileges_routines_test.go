package reference_test

import (
	"database/sql"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"stuntgres/reference/fixture"
)

// Resolve the real callable identity independently of every information-schema view.
func dtpRoutineIdentity(t *testing.T, p *fixture.Pair, c *sql.Conn, schema, name, inputs string) string {
	t.Helper()
	var oid int64
	if err := c.QueryRowContext(p.Ctx, "SELECT $1::regprocedure::oid", `"`+schema+`"."`+name+`"(`+inputs+`)`).Scan(&oid); err != nil || oid <= 0 {
		t.Fatalf("routine %s.%s(%s): oid=%d error=%v", schema, name, inputs, oid, err)
	}
	return dtpSpecificName(t, name, oid)
}

func dtpSpecificName(t *testing.T, name string, oid int64) string {
	t.Helper()
	suffix := "_" + strconv.FormatInt(oid, 10)
	prefix := []rune(name)
	for len(string(prefix))+len(suffix) > 63 {
		prefix = prefix[:len(prefix)-1]
	}
	identity := string(prefix) + suffix
	if !utf8.ValidString(identity) || len(identity) > 63 {
		t.Fatal("invalid specific identity", identity)
	}
	return identity
}

func dtpRoutineRows(schema, identity string, ids ...any) [][]any {
	var rows [][]any
	for _, id := range ids {
		rows = append(rows, []any{schema, identity, "ROUTINE", id})
	}
	return rows
}

func dtpRoutineWhere(schema, identity string) string {
	return "object_schema='"+schema+"' AND object_type='ROUTINE' AND object_name='"+identity+"'"
}

func TestReferenceDataTypePrivilegesRoutines(t *testing.T) {
	dtpScenario(t, func(t *testing.T, p *fixture.Pair, _ fixture.Backend, c *sql.Conn, run func(fixture.Step)) {
		for _, q := range []string{"CREATE DOMAIN dtp_rdomain AS integer[]", "CREATE TYPE dtp_rtype AS (a integer)", "CREATE SEQUENCE dtp_no_execution"} {
			run(fixture.Exec(q, 0))
		}
		cases := []struct {
			ddl, name, inputs string
			ids []any
		}{
			{"CREATE FUNCTION dtp_zero() RETURNS integer LANGUAGE SQL AS 'SELECT 1'", "dtp_zero", "", []any{"0"}},
			{"CREATE FUNCTION dtp_two(a integer,b text) RETURNS integer LANGUAGE SQL AS 'SELECT a'", "dtp_two", "integer,text", []any{"0","1","2"}},
			{"CREATE FUNCTION dtp_out(OUT answer integer) LANGUAGE SQL AS 'SELECT 1'", "dtp_out", "", []any{"0","1"}},
			{"CREATE FUNCTION dtp_mix(IN a integer,INOUT b integer,OUT c text) LANGUAGE SQL AS 'SELECT b,a::text'", "dtp_mix", "integer,integer", []any{"0","1","2","3"}},
			{"CREATE FUNCTION dtp_table(a integer) RETURNS TABLE(k integer,v text) LANGUAGE SQL AS 'SELECT a,a::text'", "dtp_table", "integer", []any{"0","1","2","3"}},
			{"CREATE FUNCTION dtp_variadic(VARIADIC a integer[]) RETURNS integer LANGUAGE SQL AS 'SELECT 1'", "dtp_variadic", "integer[]", []any{"0","1"}},
			{"CREATE PROCEDURE dtp_proc(a integer,b text) LANGUAGE SQL AS 'SELECT 1'", "dtp_proc", "integer,text", []any{"1","2",nil}},
			{"CREATE PROCEDURE dtp_pzero() LANGUAGE SQL AS 'SELECT 1'", "dtp_pzero", "", []any{nil}},
			{"CREATE FUNCTION dtp_types(a dtp_rdomain,b dtp_rtype,c integer[][]) RETURNS dtp_rdomain LANGUAGE SQL AS 'SELECT a'", "dtp_types", "dtp_rdomain,dtp_rtype,integer[]", []any{"0","1","2","3"}},
			{"CREATE FUNCTION dtp_composite(a dtp_rtype) RETURNS dtp_rtype LANGUAGE SQL AS 'SELECT a'", "dtp_composite", "dtp_rtype", []any{"0","1"}},
			{"CREATE FUNCTION dtp_pl(a integer) RETURNS integer LANGUAGE plpgsql AS 'BEGIN RETURN a; END'", "dtp_pl", "integer", []any{"0","1"}},
			{"CREATE FUNCTION dtp_effect() RETURNS bigint LANGUAGE SQL AS $$SELECT nextval('dtp_no_execution')$$", "dtp_effect", "", []any{"0"}},
		}
		for _, x := range cases {
			run(fixture.Exec(x.ddl, 0))
			id := dtpRoutineIdentity(t, p, c, "public", x.name, x.inputs)
			run(dtpProjection(t, p, c, dtpRoutineWhere("public", id), dtpRoutineRows("public", id, x.ids...)))
		}
		// These are genuine declaration types, not just matching identifier counts.
		// Resolve dynamic type identities on each backend, without executing a body.
		run(fixture.Select("SELECT pronargs,proargtypes[0]='public.dtp_rdomain'::regtype::oid AS domain_input,proargtypes[1]='public.dtp_rtype'::regtype::oid AS composite_input,proargtypes[2]='pg_catalog.int4[]'::regtype::oid AS array_input,prorettype='public.dtp_rdomain'::regtype::oid AS domain_result FROM pg_catalog.pg_proc WHERE oid='public.dtp_types(public.dtp_rdomain,public.dtp_rtype,pg_catalog.int4[])'::regprocedure::oid",
			[]string{"pronargs","domain_input","composite_input","array_input","domain_result"}, []string{"INT2","BOOL","BOOL","BOOL","BOOL"}, [][]any{{int64(3),true,true,true,true}}))
		run(fixture.Select("SELECT pronargs,proargtypes[0]='public.dtp_rtype'::regtype::oid AS composite_input,prorettype='public.dtp_rtype'::regtype::oid AS composite_result FROM pg_catalog.pg_proc WHERE oid='public.dtp_composite(public.dtp_rtype)'::regprocedure::oid",
			[]string{"pronargs","composite_input","composite_result"}, []string{"INT2","BOOL","BOOL"}, [][]any{{int64(1),true,true}}))
		run(fixture.Select("SELECT p.pronargs,p.proargtypes[0]='pg_catalog.int4'::regtype::oid AS scalar_input,p.prorettype='pg_catalog.int4'::regtype::oid AS scalar_result,l.lanname FROM pg_catalog.pg_proc p JOIN pg_catalog.pg_language l ON l.oid=p.prolang WHERE p.oid='public.dtp_pl(pg_catalog.int4)'::regprocedure::oid",
			[]string{"pronargs","scalar_input","scalar_result","lanname"}, []string{"INT2","BOOL","BOOL","NAME"}, [][]any{{int64(1),true,true,"plpgsql"}}))
		run(fixture.Select("SELECT d.typtype,a.oid='pg_catalog.int4[]'::regtype::oid AS array_base,a.typelem='pg_catalog.int4'::regtype::oid AS element_identity,e.typarray=a.oid AS reciprocal FROM pg_catalog.pg_type d JOIN pg_catalog.pg_type a ON a.oid=d.typbasetype JOIN pg_catalog.pg_type e ON e.oid=a.typelem WHERE d.oid='public.dtp_rdomain'::regtype::oid",
			[]string{"typtype","array_base","element_identity","reciprocal"}, []string{"CHAR","BOOL","BOOL","BOOL"}, [][]any{{"d",true,true,true}}))
		run(fixture.Select("SELECT t.typtype,c.relkind,c.reltype=t.oid AS reciprocal FROM pg_catalog.pg_type t JOIN pg_catalog.pg_class c ON c.oid=t.typrelid WHERE t.oid='public.dtp_rtype'::regtype::oid",
			[]string{"typtype","relkind","reciprocal"}, []string{"CHAR","CHAR","BOOL"}, [][]any{{"c","c",true}}))
		for _, x := range []struct{name, inputs string; ids []any}{
			{"abs","integer",[]any{"0","1"}}, {"sum","integer",[]any{"0","1"}}, {"row_number","",[]any{"0"}}, {"jsonb_each","jsonb",[]any{"0","1","2","3"}},
		} {
			id := dtpRoutineIdentity(t, p, c, "pg_catalog", x.name, x.inputs)
			run(dtpProjection(t, p, c, dtpRoutineWhere("pg_catalog", id), dtpRoutineRows("pg_catalog", id, x.ids...)))
		}
		long := strings.Repeat("é", 28)+"long"
		var previous string
		for _, typ := range []string{"integer","text"} {
			run(fixture.Exec("CREATE FUNCTION \""+long+"\"(x "+typ+") RETURNS "+typ+" LANGUAGE SQL AS 'SELECT x'", 0))
			id := dtpRoutineIdentity(t, p, c, "public", long, typ)
			if id == previous { t.Fatal("overloads share identity", id) }
			previous = id
			run(dtpProjection(t, p, c, dtpRoutineWhere("public", id), dtpRoutineRows("public", id, "0","1")))
		}
		run(dtpProjection(t, p, c, "object_schema='public' AND object_name='dtp_rdomain'", dtpRows("public","dtp_rdomain","DOMAIN","1")))
		run(fixture.Select("SELECT nextval('dtp_no_execution') AS n", []string{"n"}, []string{"INT8"}, [][]any{{int64(1)}}))
		// NULL result identity must remain a typed NULL, not empty text.
		id := dtpRoutineIdentity(t,p,c,"public","dtp_pzero","")
		rows, err := c.QueryContext(p.Ctx,"SELECT dtd_identifier FROM information_schema.data_type_privileges WHERE "+dtpRoutineWhere("public",id))
		if err != nil { t.Fatal(err) }
		defer rows.Close()
		if !rows.Next() { t.Fatal("missing procedure result") }
		var result sql.NullString
		if err := rows.Scan(&result); err != nil || result.Valid { t.Fatal("procedure NULL",result,err) }
		if rows.Next() || rows.Err()!=nil { t.Fatal("procedure result multiplicity",rows.Err()) }
	})
}

func TestReferenceDataTypePrivilegesRoutineVisibility(t *testing.T) {
	dtpScenario(t, func(t *testing.T, p *fixture.Pair, _ fixture.Backend, c *sql.Conn, run func(fixture.Step)) {
		run(fixture.Exec("CREATE SCHEMA dtp_rprivate AUTHORIZATION dtp_owner",0))
		dtpActor(run,"dtp_owner",func(){
			for _, q := range []string{
				"CREATE DOMAIN dtp_rprivate.hidden AS integer", "REVOKE USAGE ON DOMAIN dtp_rprivate.hidden FROM PUBLIC",
				"CREATE FUNCTION dtp_rprivate.over(a dtp_rprivate.hidden) RETURNS dtp_rprivate.hidden LANGUAGE SQL AS 'SELECT a'",
				"CREATE FUNCTION dtp_rprivate.over(a text) RETURNS text LANGUAGE SQL AS 'SELECT a'",
				"REVOKE ALL ON FUNCTION dtp_rprivate.over(dtp_rprivate.hidden),dtp_rprivate.over(text) FROM PUBLIC",
				"REVOKE ALL ON FUNCTION dtp_rprivate.over(dtp_rprivate.hidden),dtp_rprivate.over(text) FROM dtp_owner",
			} { run(fixture.Exec(q,0)) }
		})
		a := dtpRoutineIdentity(t,p,c,"dtp_rprivate","over","dtp_rprivate.hidden")
		z := dtpRoutineIdentity(t,p,c,"dtp_rprivate","over","text")
		where := "object_schema='dtp_rprivate' AND object_type='ROUTINE'"
		visible := dtpRoutineRows("dtp_rprivate",a,"0","1")
		all := append(append([][]any{},visible...),dtpRoutineRows("dtp_rprivate",z,"0","1")...)
		check := func(actor string,want [][]any){ dtpActor(run,actor,func(){run(dtpProjection(t,p,c,where,want))}) }
		check("dtp_owner",all)
		check("dtp_reader",nil)
		for _, q := range []string{"GRANT EXECUTE ON FUNCTION dtp_rprivate.over(dtp_rprivate.hidden) TO dtp_reader","REVOKE EXECUTE ON FUNCTION dtp_rprivate.over(dtp_rprivate.hidden) FROM dtp_reader","GRANT EXECUTE ON FUNCTION dtp_rprivate.over(dtp_rprivate.hidden) TO dtp_reader"} {
			run(fixture.Exec(q,0))
			if strings.HasPrefix(q,"REVOKE") { check("dtp_reader",nil) } else { check("dtp_reader",visible) }
		}
		run(fixture.Exec("GRANT dtp_reader TO dtp_member WITH INHERIT TRUE,SET TRUE",0))
		run(fixture.Exec("GRANT dtp_reader TO dtp_set WITH INHERIT FALSE,SET TRUE",0))
		check("dtp_member",visible)
		check("dtp_set",nil)
		dtpActor(run,"dtp_set",func(){
			run(fixture.Exec("SET ROLE dtp_reader",0)); run(dtpProjection(t,p,c,where,visible)); run(fixture.Exec("RESET ROLE",0))
		})
		run(fixture.Exec("GRANT EXECUTE ON FUNCTION dtp_rprivate.over(text) TO PUBLIC",0))
		check("dtp_reader",all)
		run(fixture.Exec("REVOKE EXECUTE ON FUNCTION dtp_rprivate.over(text) FROM PUBLIC",0))
		check("dtp_reader",visible)
		run(fixture.Exec("GRANT dtp_owner TO dtp_member WITH INHERIT TRUE",0))
		check("dtp_member",all)
	})
}
