package reference_test

import (
	"database/sql"
	"strconv"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func intarraySliceCatalog(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn,owner string) []int64 {
	t.Helper()
	// Assert the preserved provider and SET prefix as well as the new subset;
	// never assert the full upstream extension inventory.
	intarraySetCatalog(t,p,b,c,owner)
	var ids []int64
	identity := func(tc intarraySliceCase) string { return "('ia_provider."+tc.name+"("+strings.Join(tc.types,",")+")'::regprocedure)::oid" }
	read := func(expression string) {
		var id int64
		if err := c.QueryRowContext(p.Ctx,"SELECT "+expression).Scan(&id); err != nil || id <= 0 { t.Fatalf("identity %s=%d: %v",expression,id,err) }
		for _,previous := range ids { if previous == id { t.Fatalf("selected identities alias %d",id) } }
		ids = append(ids,id)
	}
	member := func(class,object string) {
		q := "SELECT refobjid=(SELECT oid FROM pg_extension WHERE extname='intarray') AS extension FROM pg_depend WHERE classid="+class+" AND objid="+object+" AND objsubid=0 AND refclassid=3079 AND refobjsubid=0 AND deptype='e'"
		intarrayBoth(t,p,b,c,fixture.Select(q,[]string{"extension"},[]string{"BOOL"},[][]any{{true}}))
	}
	for _,tc := range intarraySliceCases[:5] {
		object := identity(tc); read(object)
		args := make([]string,len(tc.types)); for i,typ := range tc.types { args[i] = "23"; if typ == "integer[]" { args[i] = "1007" } }
		result := "23"; if tc.family == "_INT4" { result = "1007" }
		q := "SELECT proname,proargtypes::text AS args,prorettype::text AS result,pronargs,proretset,proisstrict,provolatile,proparallel,l.lanname,prosrc,probin,r.rolname,pronamespace='ia_provider'::regnamespace AS namespace,provariadic=0::oid AS nonvariadic,pronargdefaults,proargnames IS NULL AS unnamed,proallargtypes IS NULL AS inputs_only,proargmodes IS NULL AS no_modes FROM (SELECT * FROM pg_proc WHERE oid="+object+") p JOIN pg_language l ON l.oid=p.prolang JOIN pg_roles r ON r.oid=p.proowner"
		intarrayBoth(t,p,b,c,fixture.Select(q,[]string{"proname","args","result","pronargs","proretset","proisstrict","provolatile","proparallel","lanname","prosrc","probin","rolname","namespace","nonvariadic","pronargdefaults","unnamed","inputs_only","no_modes"},[]string{"NAME","TEXT","TEXT","INT2","BOOL","BOOL","CHAR","CHAR","NAME","TEXT","TEXT","NAME","BOOL","BOOL","INT2","BOOL","BOOL","BOOL"},[][]any{{tc.name,strings.Join(args," "),result,int64(len(args)),false,true,"i","s","c",tc.name,"$libdir/_int",owner,true,true,int64(0),true,true,true}}))
		member("1255",object)
	}
	for _,unary := range []bool{true,false} {
		signature,left,right,kind,backer := "NONE,integer[]","0","1007","l",identity(intarraySliceCases[0])
		if !unary { signature,left,right,kind,backer = "integer[],integer","1007","23","b",identity(intarraySliceCases[1]) }
		object := "('ia_provider.#("+signature+")'::regoperator)::oid"; read(object)
		q := "SELECT oprname,oprkind,oprleft="+left+"::oid AS leftarg,oprright="+right+"::oid AS rightarg,oprresult=23::oid AS result,oprcode::oid="+backer+" AS backer,oprcom=0::oid AS commutator,oprnegate=0::oid AS negator,oprrest::oid=0::oid AS restrictor,oprjoin::oid=0::oid AS joiner,oprcanhash,oprcanmerge,oprnamespace='ia_provider'::regnamespace AS namespace,r.rolname FROM (SELECT * FROM pg_operator WHERE oid="+object+") o JOIN pg_roles r ON r.oid=o.oprowner"
		intarrayBoth(t,p,b,c,fixture.Select(q,[]string{"oprname","oprkind","leftarg","rightarg","result","backer","commutator","negator","restrictor","joiner","oprcanhash","oprcanmerge","namespace","rolname"},[]string{"NAME","CHAR","BOOL","BOOL","BOOL","BOOL","BOOL","BOOL","BOOL","BOOL","BOOL","BOOL","BOOL","NAME"},[][]any{{"#",kind,true,true,true,true,true,true,true,true,false,false,true,owner}}))
		member("2617",object)
		q = "SELECT refobjid="+backer+" AS backer FROM pg_depend WHERE classid=2617 AND objid="+object+" AND objsubid=0 AND refclassid=1255 AND refobjid="+backer+" AND refobjsubid=0 AND deptype='n'"
		intarrayBoth(t,p,b,c,fixture.Select(q,[]string{"backer"},[]string{"BOOL"},[][]any{{true}}))
	}
	return ids
}

func TestReferenceIntarraySliceCatalog(t *testing.T) {
	t.Run("declarations",func(t *testing.T) {
		intarrayScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn) {
			intarrayInstall(t,p,b,c)
			if ids := intarraySliceCatalog(t,p,b,c,intarrayFixtureOwner(b)); len(ids) != 7 { t.Fatalf("selected declarations %v",ids) }
		})
	})
	for _,tc := range []struct{ name,args,result,body,script string; line int; operator bool }{
		{"intset","integer","integer[]","ARRAY[$1]","intset(int4)\nRETURNS _int4",228,false},
		{"icount","integer[]","integer","0","icount(_int4)\nRETURNS int4",233,false},
		{"idx","integer[],integer","integer","0","idx(_int4, int4)\nRETURNS int4",268,false},
		{"subarray","integer[],integer","integer[]","$1","subarray(_int4, int4)\nRETURNS _int4",284,false},
		{"subarray","integer[],integer,integer","integer[]","$1","subarray(_int4, int4, int4)\nRETURNS _int4",279,false},
		{"#","NONE,integer[]","integer","0","CREATE OPERATOR # (\n\tRIGHTARG = _int4,\n\tPROCEDURE = icount\n)",238,true},
		{"#","integer[],integer","integer","0","CREATE OPERATOR # (\n\tLEFTARG = _int4,\n\tRIGHTARG = int4,\n\tPROCEDURE = idx\n)",273,true},
	} {
		t.Run("collision_"+tc.name+strconv.Itoa(tc.line),func(t *testing.T) {
			intarrayScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn) {
				run := func(s fixture.Step) { fixture.RunStep(t,p.Ctx,c,b.Recorder,s) }
				run(fixture.Exec("CREATE SCHEMA ia_provider",0))
				// An unrelated user type and its generated companion must survive.
				run(fixture.Exec("CREATE TYPE ia_provider.slice_marker AS (v integer)",0))
				base,array := intarrayOID(t,p,c,"ia_provider.slice_marker"),intarrayOID(t,p,c,"ia_provider._slice_marker")
				object := "('ia_provider."+tc.name+"("+tc.args+")'::regprocedure)::oid"
				want := fixture.ControlsReferenceDiagnostic("42723","function \""+tc.name+"\" already exists with same argument types")
				statement := "CREATE FUNCTION "+tc.script+"\nAS '$libdir/_int'\nLANGUAGE C STRICT IMMUTABLE PARALLEL SAFE"
				if tc.operator {
					args := tc.args; if strings.HasPrefix(args,"NONE,") { args = "integer[]" }
					run(fixture.Exec("CREATE FUNCTION ia_provider.slice_collider("+args+") RETURNS integer LANGUAGE SQL IMMUTABLE STRICT AS 'SELECT 0'",0))
					definition := "RIGHTARG=integer[],PROCEDURE=ia_provider.slice_collider"
					if tc.args == "integer[],integer" { definition = "LEFTARG=integer[],RIGHTARG=integer,PROCEDURE=ia_provider.slice_collider" }
					run(fixture.Exec("CREATE OPERATOR ia_provider.# ("+definition+")",0))
					object = "('ia_provider.#("+tc.args+")'::regoperator)::oid"
					want = fixture.ControlsReferenceDiagnostic("42723","operator # already exists")
					statement = tc.script
				} else { run(fixture.Exec("CREATE FUNCTION ia_provider."+tc.name+"("+tc.args+") RETURNS "+tc.result+" LANGUAGE SQL IMMUTABLE STRICT AS 'SELECT "+tc.body+"'",0)) }
				var before int64; if err := c.QueryRowContext(p.Ctx,"SELECT "+object).Scan(&before); err != nil { t.Fatal(err) }
				// Full Context, including script tabs and near-line values,
				// independently confirmed against pinned PostgreSQL 18.6.
				want.Context = "SQL statement \""+statement+"\"\nextension script file \"intarray--1.2.sql\", near line "+strconv.Itoa(tc.line)
				for _,prepared := range []bool{false,true} {
					intarrayError(t,p,b,c,"CREATE EXTENSION intarray SCHEMA ia_provider",prepared,"execute",want)
					intarrayBoth(t,p,b,c,fixture.Select("SELECT "+object+"=$1::oid AS original",[]string{"original"},[]string{"BOOL"},[][]any{{true}},before))
					intarrayBoth(t,p,b,c,fixture.Select("SELECT extname FROM pg_extension WHERE extname='intarray'",[]string{"extname"},[]string{"NAME"},[][]any{}))
					intarrayBoth(t,p,b,c,fixture.Select("SELECT typname FROM pg_type WHERE typnamespace='ia_provider'::regnamespace AND typname IN ('query_int','_query_int','intbig_gkey','_intbig_gkey')",[]string{"typname"},[]string{"NAME"},[][]any{}))
					names := [][]any{{tc.name}}; if tc.operator { names = [][]any{{"slice_collider"}} }
					intarrayBoth(t,p,b,c,fixture.Select("SELECT proname FROM pg_proc WHERE pronamespace='ia_provider'::regnamespace ORDER BY proname",[]string{"proname"},[]string{"NAME"},names))
					operators := [][]any{}
					if tc.operator { operators = [][]any{{"#"}} }
					intarrayBoth(t,p,b,c,fixture.Select("SELECT oprname FROM pg_operator WHERE oprnamespace='ia_provider'::regnamespace ORDER BY oprname",[]string{"oprname"},[]string{"NAME"},operators))
					if base != intarrayOID(t,p,c,"ia_provider.slice_marker") || array != intarrayOID(t,p,c,"ia_provider._slice_marker") { t.Fatal("collision replaced unrelated type/companion") }
				}
			})
		})
	}
}
