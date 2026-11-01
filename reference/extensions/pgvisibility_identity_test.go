package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePgvisibilityIdentity(t *testing.T) {
	pvCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		pvRun(t,p,b,c,fixture.Exec("CREATE SCHEMA pv_moved",0))
		pvRun(t,p,b,c,fixture.Exec("INSERT INTO pv_part.heap VALUES(1)",1)); pvFreeze(t,p,b,c)
		original:=pvOID(t,p,c,"pv_part.heap")
		for _, q:=range []string{
			"CREATE VIEW pv_part.bound_scalar AS SELECT (pv_part.pg_visibility_map_summary('pv_part.heap'::regclass)).all_visible AS n",
			"CREATE VIEW pv_part.dynamic_scalar AS SELECT (pv_part.pg_visibility_map_summary('pv_part.heap'::text::regclass)).all_visible AS n",
			"CREATE VIEW pv_part.bound_source AS SELECT * FROM pv_part.pg_visibility('pv_part.heap'::regclass)",
			"CREATE VIEW pv_part.bound_map AS SELECT * FROM pv_part.pg_visibility_map('pv_part.heap'::regclass)",
			"CREATE VIEW pv_part.bound_page AS SELECT * FROM pv_part.pg_visibility('pv_part.heap'::regclass,0)",
			"CREATE VIEW pv_part.bound_map_page AS SELECT * FROM pv_part.pg_visibility_map('pv_part.heap'::regclass,0)",
		} { pvRun(t,p,b,c,fixture.Exec(q,0)) }
		q:="SELECT (pv_part.pg_visibility_map_summary($1::oid)).all_visible AS n"
		st,err:=c.PrepareContext(p.Ctx,q); if err!=nil {t.Fatal(err)}; defer st.Close()
		held:=fixture.HeldStatement{Executor:c,Statement:st,SQL:q}
		readOriginal:=func(){fixture.RunStep(t,p.Ctx,held,b.Recorder,fixture.Select(q,[]string{"n"},[]string{"INT8"},[][]any{{int64(1)}},original))}
		post:=func(){
			pvBoth(t,p,b,c,fixture.Select("SELECT n FROM pv_part.bound_scalar",[]string{"n"},[]string{"INT8"},[][]any{{int64(1)}}))
			pvBoth(t,p,b,c,fixture.Select("SELECT * FROM pv_part.bound_source ORDER BY blkno",pvPageRowsNames,pvPageRowsTypes,[][]any{{int64(0),true,true,true}}))
			pvBoth(t,p,b,c,fixture.Select("SELECT * FROM pv_part.bound_map ORDER BY blkno",pvMapRowsNames,pvMapRowsTypes,[][]any{{int64(0),true,true}}))
			pvBoth(t,p,b,c,fixture.Select("SELECT * FROM pv_part.bound_page",pvPageNames,pvPageTypes,[][]any{{true,true,true}}))
			pvBoth(t,p,b,c,fixture.Select("SELECT * FROM pv_part.bound_map_page",pvMapNames,pvMapTypes,[][]any{{true,true}}))
		}
		readOriginal(); post()
		for _, q:=range []string{"ALTER TABLE pv_part.heap RENAME TO kept","CREATE TABLE pv_part.heap(k integer)","ALTER TABLE pv_part.kept SET SCHEMA pv_moved"} {pvRun(t,p,b,c,fixture.Exec(q,0))}
		post(); readOriginal()
		pvBoth(t,p,b,c,fixture.Select("SELECT n FROM pv_part.dynamic_scalar",[]string{"n"},[]string{"INT8"},[][]any{{int64(0)}}))
		pvBoth(t,p,b,c,fixture.Select("SELECT (pv_part.pg_visibility_map_summary($1::text::regclass)).all_visible AS n",[]string{"n"},[]string{"INT8"},[][]any{{int64(0)}},"pv_part.heap"))
		// Genuine function identities survive each rename and namespace move;
		// neither current SQL spelling nor extension membership selects code.
		for _, member:=range []struct{name,args,renamed string}{
			{"pg_visibility_map","regclass,bigint","map_page"},
			{"pg_visibility","regclass,bigint","page"},
			{"pg_visibility_map","regclass","map_rows"},
			{"pg_visibility","regclass","page_rows"},
			{"pg_visibility_map_summary","regclass","summary"},
		} {
			pvRun(t,p,b,c,fixture.Exec("ALTER FUNCTION pv_part."+member.name+"("+member.args+") RENAME TO "+member.renamed,0))
			post()
			pvRun(t,p,b,c,fixture.Exec("ALTER FUNCTION pv_part."+member.renamed+"("+member.args+") SET SCHEMA pv_moved",0))
			post()
			pvRun(t,p,b,c,fixture.Exec("ALTER FUNCTION pv_moved."+member.renamed+"("+member.args+") SET SCHEMA pv_part",0))
			pvRun(t,p,b,c,fixture.Exec("ALTER FUNCTION pv_part."+member.renamed+"("+member.args+") RENAME TO "+member.name,0))
		}
		readOriginal()
		pvRun(t,p,b,c,fixture.Exec("ALTER EXTENSION pg_visibility SET SCHEMA pv_moved",0)); post()
		pvBoth(t,p,b,c,fixture.Select("SELECT * FROM pv_moved.pg_visibility_map_summary($1::oid)",pvMapNames,pvSummaryTypes,[][]any{{int64(1),int64(1)}},original))
		pvRun(t,p,b,c,fixture.Exec("CREATE FUNCTION pv_part.pg_visibility_map_summary(regclass) RETURNS integer LANGUAGE SQL AS 'SELECT 77'",0))
		pvBoth(t,p,b,c,fixture.Select("SELECT pv_part.pg_visibility_map_summary('pv_part.heap') AS n",[]string{"n"},[]string{"INT4"},[][]any{{int64(77)}})); post()
		pvRun(t,p,b,c,fixture.Exec("DROP FUNCTION pv_part.pg_visibility_map_summary(regclass)",0))
		pvRun(t,p,b,c,fixture.Exec("ALTER EXTENSION pg_visibility DROP FUNCTION pv_moved.pg_visibility_map_summary(regclass)",0))
		// A detached but genuine provider must remain callable, not an executable
		// fiction selected by an extension-member count.
		pvBoth(t,p,b,c,fixture.Select("SELECT * FROM pv_moved.pg_visibility_map_summary($1::oid)",pvMapNames,pvSummaryTypes,[][]any{{int64(1),int64(1)}},original))
		pvRun(t,p,b,c,fixture.Exec("ALTER EXTENSION pg_visibility ADD FUNCTION pv_moved.pg_visibility_map_summary(regclass)",0))
		pvRun(t,p,b,c,fixture.Exec("DROP EXTENSION pg_visibility CASCADE",0))
		pvRun(t,p,b,c,fixture.Exec("CREATE EXTENSION pg_visibility SCHEMA pv_part",0))
		pvBoth(t,p,b,c,fixture.Select("SELECT * FROM pv_part.pg_visibility_map_summary($1::oid)",pvMapNames,pvSummaryTypes,[][]any{{int64(1),int64(1)}},original))
		pvBoth(t,p,b,c,fixture.Select("SELECT count(*) AS n FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='pv_part' AND c.relname IN ('bound_scalar','bound_source','bound_map','bound_page','bound_map_page','dynamic_scalar')",[]string{"n"},[]string{"INT8"},[][]any{{int64(0)}}))
	})
}
