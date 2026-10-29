package reference_test

import (
	"bytes"
	"database/sql"
	_ "embed"
	"encoding/json"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

// Independent exact PG18.6 observations, grounded in REL_18_6
// system_functions.sql and contrib/citext/earthdistance declaration sources.
// Captured with the approved fixture image, UTF8/C and lc_messages=C. Generated
// extension OIDs are deliberately discarded; identities are reacquired by name.
// Both quote policies were observed independently, not rewritten native output.
//go:embed testdata/routine_deparse_standard.json
var routineDeparseStandardData []byte

type routineDeparseStandardFact struct { Identity,Normal,Quoted string }

func routineDeparseStandardCheck(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn,facts []routineDeparseStandardFact) {
	t.Helper()
	for _,quoted := range []bool{false,true} {
		setting := "off"
		if quoted { setting = "on" }
		resultsExec(t,p,b,c,"SET quote_all_identifiers="+setting)
		for _,fact := range facts {
			t.Run(setting+"/"+fact.Identity,func(t *testing.T){
				want := fact.Normal
				if quoted { want = fact.Quoted }
				q := "SELECT pg_get_functiondef('"+strings.ReplaceAll(fact.Identity,"'","''")+"'::regprocedure) AS definition"
				routineDeparseText(t,p,b,c,q,want)
			})
		}
		// REL_18_6 cube--1.2.sql and cube.control independently specify the
		// retained C symbol/library pair; upgrades through 1.5 preserve it.
		cube := "CREATE OR REPLACE FUNCTION public.cube(double precision)\n RETURNS cube\n LANGUAGE c\n IMMUTABLE PARALLEL SAFE STRICT\nAS '$libdir/cube', $function$cube_f8$function$\n"
		if quoted { cube = "CREATE OR REPLACE FUNCTION \"public\".\"cube\"(double precision)\n RETURNS \"cube\"\n LANGUAGE \"c\"\n IMMUTABLE PARALLEL SAFE STRICT\nAS '$libdir/cube', $function$cube_f8$function$\n" }
		routineDeparseText(t,p,b,c,"SELECT pg_get_functiondef('cube(double precision)'::regprocedure) AS definition",cube)
	}
	resultsExec(t,p,b,c,"RESET quote_all_identifiers")
}

func TestReferenceRoutineDeparseStandardBodies(t *testing.T) {
	for _, pass := range []string{"first", "repeat"} {
		t.Run(pass, routineDeparseStandardBodiesScenario)
	}
}

func routineDeparseStandardBodiesScenario(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "rd_shadow")
	var facts []routineDeparseStandardFact
	if err := json.Unmarshal(routineDeparseStandardData,&facts);err != nil { t.Fatal(err) }
	if len(facts) != 60 { t.Fatal("incomplete independent represented recipe inventory",len(facts)) }
	p := fixture.NewScenario(t)
	backends := p.Backends()
	for i := len(backends)-1;i >= 0;i-- {
		b := backends[i]
		t.Run(b.Name,func(t *testing.T){
			c,err := b.DB.Conn(p.Ctx)
			if err != nil { t.Fatal(err) };defer c.Close()
			resultsExec(t,p,b,c,"CREATE EXTENSION citext","CREATE EXTENSION cube","CREATE EXTENSION earthdistance")
			phase := "initial"
			check := func(conn *sql.Conn,backend fixture.Backend){
				t.Run(phase,func(t *testing.T){routineDeparseStandardCheck(t,p,backend,conn,facts)})
			}
			check(c,b)
			if !b.PG {
				phase = "restored"
				emitted := resultsNativeReplayRound(t,p,b,c,check)
				connector,err := stuntgres.NewDriver().OpenConnector(t.Name()+"/metadata-fresh")
				if err != nil { t.Fatal(err) }
				raw := sql.OpenDB(connector);defer raw.Close()
				recorded,rec := fixture.RecordedNativePool(t,connector)
				fixture.RunStep(t,p.Ctx,recorded,rec,fixture.Exec("CREATE TYPE rd_recipe_pressure AS (x integer)",0))
				if err := stuntgres.Restore(p.Ctx,raw,bytes.NewReader(emitted));err != nil { t.Fatal(err) }
				clone,err := recorded.Conn(p.Ctx)
				if err != nil { t.Fatal(err) };defer clone.Close()
				phase = "fresh"
				check(clone,fixture.Backend{Recorder:rec})
			}
			// Metadata bodies follow actual installed identities without invoking
			// unavailable trigonometric providers or rebinding raw body SQL.
			resultsExec(t,p,b,c,"ALTER FUNCTION earth() RENAME TO rd_radius", "ALTER FUNCTION regexp_match(citext,citext,text) RENAME TO rd_regexp")
			routineDeparseText(t,p,b,c,"SELECT pg_get_functiondef('rd_regexp(citext,citext,text)'::regprocedure) AS definition","CREATE OR REPLACE FUNCTION public.rd_regexp(string citext, pattern citext, flags text)\n RETURNS text[]\n LANGUAGE sql\n IMMUTABLE PARALLEL SAFE STRICT\nRETURN regexp_match((string)::text, (pattern)::text, CASE WHEN (strpos(flags, 'c'::text) = 0) THEN (flags || 'i'::text) ELSE flags END)\n")
			routineDeparseText(t,p,b,c,"SELECT pg_get_functiondef('sec_to_gc(double precision)'::regprocedure) AS definition","CREATE OR REPLACE FUNCTION public.sec_to_gc(double precision)\n RETURNS double precision\n LANGUAGE sql\n IMMUTABLE PARALLEL SAFE STRICT\nRETURN CASE WHEN ($1 < '0'::double precision) THEN '0'::double precision WHEN (($1 / ('2'::double precision * rd_radius())) > '1'::double precision) THEN (pi() * rd_radius()) ELSE (('2'::double precision * rd_radius()) * asin(($1 / ('2'::double precision * rd_radius())))) END\n")
			resultsExec(t,p,b,c,"CREATE SCHEMA rd_shadow", "CREATE FUNCTION rd_shadow.rd_radius() RETURNS double precision LANGUAGE SQL AS 'SELECT 1::double precision'", "SET search_path TO rd_shadow,public,pg_catalog")
			routineDeparseText(t,p,b,c,"SELECT pg_get_functiondef('public.sec_to_gc(double precision)'::regprocedure) AS definition","CREATE OR REPLACE FUNCTION public.sec_to_gc(double precision)\n RETURNS double precision\n LANGUAGE sql\n IMMUTABLE PARALLEL SAFE STRICT\nRETURN CASE WHEN ($1 < '0'::double precision) THEN '0'::double precision WHEN (($1 / ('2'::double precision * public.rd_radius())) > '1'::double precision) THEN (pi() * public.rd_radius()) ELSE (('2'::double precision * public.rd_radius()) * asin(($1 / ('2'::double precision * public.rd_radius())))) END\n")
			resultsExec(t,p,b,c,"RESET search_path")
		})
	}
}
