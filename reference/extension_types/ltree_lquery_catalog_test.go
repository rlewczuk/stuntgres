package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func ltreeLqueryCatalogChecks(schema string) []fixture.Step {
	cases := []struct{ left, right, op, function string }{
		{"ltree","lquery","~","ltq_regex"}, {"lquery","ltree","~","ltq_rregex"},
		{"ltree[]","lquery","~","_ltq_regex"}, {"lquery","ltree[]","~","_ltq_rregex"},
		{"ltree","lquery[]","?","lt_q_regex"}, {"lquery[]","ltree","?","lt_q_rregex"},
		{"ltree[]","lquery[]","?","_lt_q_regex"}, {"lquery[]","ltree[]","?","_lt_q_rregex"},
	}
	ns := "('"+schema+"'::regnamespace)::oid"
	owner := "(SELECT extowner FROM pg_extension WHERE extname='ltree')"
	ext := "(SELECT oid FROM pg_extension WHERE extname='ltree')"
	var steps []fixture.Step
	for _, tc := range cases {
		left,right := schema+"."+tc.left,schema+"."+tc.right
		proc := "('"+schema+"."+tc.function+"("+left+","+right+")'::regprocedure)::oid"
		op := "('"+schema+"."+tc.op+"("+left+","+right+")'::regoperator)::oid"
		comm := "('"+schema+"."+tc.op+"("+right+","+left+")'::regoperator)::oid"
		steps=append(steps,fixture.Select("SELECT proname,prorettype=16::oid AS result,pronargs,proisstrict,provolatile,proparallel,pronamespace="+ns+" AS namespace,proowner="+owner+" AS owner,prolang=13::oid AS c_language,prosrc,probin FROM pg_proc WHERE oid="+proc,
			[]string{"proname","result","pronargs","proisstrict","provolatile","proparallel","namespace","owner","c_language","prosrc","probin"},
			[]string{"NAME","BOOL","INT2","BOOL","CHAR","CHAR","BOOL","BOOL","BOOL","TEXT","TEXT"},
			[][]any{{tc.function,true,int64(2),true,"i","s",true,true,true,tc.function,"$libdir/ltree"}}))
		steps=append(steps,fixture.Select("SELECT oprname,oprcode::oid="+proc+" AS backer,oprcom="+comm+" AS commutator,oprnegate=0::oid AS negator,oprleft=('"+left+"'::regtype)::oid AS leftarg,oprright=('"+right+"'::regtype)::oid AS rightarg,oprresult=16::oid AS result,oprnamespace="+ns+" AS namespace,oprowner="+owner+" AS owner,oprcanhash,oprcanmerge,oprrest::oid=5040::oid AS restrictor,oprjoin::oid=5041::oid AS joiner FROM pg_operator WHERE oid="+op,
			[]string{"oprname","backer","commutator","negator","leftarg","rightarg","result","namespace","owner","oprcanhash","oprcanmerge","restrictor","joiner"},
			[]string{"NAME","BOOL","BOOL","BOOL","BOOL","BOOL","BOOL","BOOL","BOOL","BOOL","BOOL","BOOL","BOOL"},
			[][]any{{tc.op,true,true,true,true,true,true,true,true,false,false,true,true}}))
		for _, member := range []struct{ class, oid string }{{"1255",proc},{"2617",op}} {
			steps=append(steps,fixture.Select("SELECT refobjid="+ext+" AS extension FROM pg_depend WHERE classid="+member.class+" AND objid="+member.oid+" AND objsubid=0 AND refclassid=3079 AND refobjsubid=0 AND deptype='e'",[]string{"extension"},[]string{"BOOL"},[][]any{{true}}))
		}
	}
	inventory:=fixture.Select("SELECT oprname,oprleft::regtype::text AS leftarg,oprright::regtype::text AS rightarg,oprresult=16::oid AS result FROM pg_operator WHERE oprnamespace="+ns+" AND oprname IN ('~','?')",[]string{"oprname","leftarg","rightarg","result"},[]string{"NAME","TEXT","TEXT","BOOL"},nil)
	for _,tc:=range cases {inventory.Rows=append(inventory.Rows,[]any{tc.op,tc.left,tc.right,true})}; inventory.Bag=true
	steps=append(steps,inventory)
	return steps
}

func TestReferenceLtreeLqueryCatalog(t *testing.T) {
	ltreeLqueryScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
		ltreeInstall(t,p,b,c)
		for _,s:=range ltreeLqueryCatalogChecks("public"){ltreeLqueryRun(t,p,b,c,s);ltreeLqueryRun(t,p,b,c,fixture.Prepared(s))}
	})
}
