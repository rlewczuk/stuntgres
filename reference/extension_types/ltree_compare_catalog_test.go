package reference_test

import (
	"database/sql"
	"strconv"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func ltreeComparisonCatalogChecks(schema string) []fixture.Step {
	typ := schema+".ltree"
	typeOID := "('"+typ+"'::regtype)::oid"
	ns := "('"+schema+"'::regnamespace)::oid"
	ext := "(SELECT oid FROM pg_extension WHERE extname='ltree')"
	owner := "(SELECT extowner FROM pg_extension WHERE extname='ltree')"
	proc := func(name,args string) string { return "('"+schema+"."+name+"("+args+")'::regprocedure)::oid" }
	op := func(name string) string { return "('"+schema+"."+name+"("+typ+","+typ+")'::regoperator)::oid" }
	var steps []fixture.Step
	var procedures,operators,classes,families []string
	for _, tc := range []struct{ name,args,result string; arity int64 }{
		{"ltree_eq",typ+","+typ,"16",2}, {"ltree_ne",typ+","+typ,"16",2},
		{"ltree_lt",typ+","+typ,"16",2}, {"ltree_le",typ+","+typ,"16",2},
		{"ltree_gt",typ+","+typ,"16",2}, {"ltree_ge",typ+","+typ,"16",2},
		{"ltree_cmp",typ+","+typ,"23",2}, {"hash_ltree",typ,"23",1}, {"hash_ltree_extended",typ+",pg_catalog.int8","20",2},
	} {
		identity := proc(tc.name,tc.args)
		procedures = append(procedures, identity)
		steps = append(steps, fixture.Select("SELECT proname,prorettype="+tc.result+"::oid AS result,pronargs,proisstrict,provolatile,proparallel,pronamespace="+ns+" AS namespace,proowner="+owner+" AS owner,prolang=13::oid AS c_language,prosrc,probin FROM pg_proc WHERE oid="+identity,
			[]string{"proname","result","pronargs","proisstrict","provolatile","proparallel","namespace","owner","c_language","prosrc","probin"},
			[]string{"NAME","BOOL","INT2","BOOL","CHAR","CHAR","BOOL","BOOL","BOOL","TEXT","TEXT"},
			[][]any{{tc.name,true,tc.arity,true,"i","s",true,true,true,tc.name,"$libdir/ltree"}}))
	}
	for _, tc := range []struct{ name,backer,commutator,negator,restrictor,joiner string; equality bool }{
		{"=","eq","=","<>","eqsel","eqjoinsel",true},
		{"<>","ne","<>","=","neqsel","neqjoinsel",false},
		{"<","lt",">",">=","scalarltsel","scalarltjoinsel",false},
		{"<=","le",">=",">","scalarlesel","scalarlejoinsel",false},
		{">","gt","<","<=","scalargtsel","scalargtjoinsel",false},
		{">=","ge","<=","<","scalargesel","scalargejoinsel",false},
	} {
		identity := op(tc.name)
		operators = append(operators,identity)
		steps = append(steps,fixture.Select("SELECT oprname,oprcode::oid="+proc("ltree_"+tc.backer,typ+","+typ)+" AS procedure_identity,oprcom="+op(tc.commutator)+" AS commutator_identity,oprnegate="+op(tc.negator)+" AS negator_identity,oprnamespace="+ns+" AS namespace,oprleft="+typeOID+" AS leftarg,oprright="+typeOID+" AS rightarg,oprresult=16::oid AS result,oprcanmerge,oprcanhash,oprrest::oid='pg_catalog."+tc.restrictor+"'::regproc::oid AS restrictor_identity,oprjoin::oid='pg_catalog."+tc.joiner+"'::regproc::oid AS joiner_identity,oprowner="+owner+" AS owner FROM pg_operator WHERE oid="+identity,
			[]string{"oprname","procedure_identity","commutator_identity","negator_identity","namespace","leftarg","rightarg","result","oprcanmerge","oprcanhash","restrictor_identity","joiner_identity","owner"},
			[]string{"NAME","BOOL","BOOL","BOOL","BOOL","BOOL","BOOL","BOOL","BOOL","BOOL","BOOL","BOOL","BOOL"},
			[][]any{{tc.name,true,true,true,true,true,true,true,tc.equality,tc.equality,true,true,true}}))
	}
	for _, tc := range []struct{ name string; method int; ops []string }{
		{"ltree_ops",403,[]string{"<","<=","=",">=",">"}}, {"hash_ltree_ops",405,[]string{"="}},
	} {
		method := strconv.Itoa(tc.method)
		cf := "opcname='"+tc.name+"' AND opcnamespace="+ns+" AND opcmethod="+method
		ff := "opfname='"+tc.name+"' AND opfnamespace="+ns+" AND opfmethod="+method
		class := "(SELECT oid FROM pg_opclass WHERE "+cf+")"
		family := "(SELECT oid FROM pg_opfamily WHERE "+ff+")"
		classes,families = append(classes,class),append(families,family)
		steps = append(steps,
			fixture.Select("SELECT opcname,opcdefault,opcmethod::int AS method,opcnamespace="+ns+" AS namespace,opcintype="+typeOID+" AS input,opcfamily="+family+" AS family,opckeytype::int AS keytype,opcowner="+owner+" AS owner FROM pg_opclass WHERE "+cf,
				[]string{"opcname","opcdefault","method","namespace","input","family","keytype","owner"}, []string{"NAME","BOOL","INT4","BOOL","BOOL","BOOL","INT4","BOOL"}, [][]any{{tc.name,true,int64(tc.method),true,true,true,int64(0),true}}),
			fixture.Select("SELECT opfname,opfmethod::int AS method,opfnamespace="+ns+" AS namespace,opfowner="+owner+" AS owner FROM pg_opfamily WHERE "+ff, []string{"opfname","method","namespace","owner"}, []string{"NAME","INT4","BOOL","BOOL"}, [][]any{{tc.name,int64(tc.method),true,true}}))
		strategy := "CASE amopstrategy"
		var members [][]any
		for i,name := range tc.ops {
			strategy += " WHEN "+strconv.Itoa(i+1)+" THEN "+op(name)
			members = append(members,[]any{int64(i+1),true,true,true,int64(tc.method),"s"})
		}
		strategy += " END"
		steps = append(steps,fixture.Select("SELECT amopstrategy,amopopr="+strategy+" AS operator_identity,amoplefttype="+typeOID+" AS leftarg,amoprighttype="+typeOID+" AS rightarg,amopmethod::int AS method,amoppurpose FROM pg_amop WHERE amopfamily="+family+" ORDER BY amopstrategy", []string{"amopstrategy","operator_identity","leftarg","rightarg","method","amoppurpose"}, []string{"INT2","BOOL","BOOL","BOOL","INT4","CHAR"},members))
		support := proc("ltree_cmp",typ+","+typ)
		rows := [][]any{{int64(1),true,true,true}}
		if tc.method == 405 {
			support = "CASE amprocnum WHEN 1 THEN "+proc("hash_ltree",typ)+" WHEN 2 THEN "+proc("hash_ltree_extended",typ+",pg_catalog.int8")+" END"
			rows = append(rows,[]any{int64(2),true,true,true})
		}
		steps = append(steps,fixture.Select("SELECT amprocnum,amproc::oid="+support+" AS signature,amproclefttype="+typeOID+" AS leftarg,amprocrighttype="+typeOID+" AS rightarg FROM pg_amproc WHERE amprocfamily="+family+" ORDER BY amprocnum", []string{"amprocnum","signature","leftarg","rightarg"}, []string{"INT2","BOOL","BOOL","BOOL"},rows))
		// hashadjustmembers makes optional hash support2 a loose member:
		// AUTO to its family and routine, not INTERNAL to the class.
		for _, member := range []struct{ catalog,table,filter string; count int64 }{
			{"2602","pg_amop","amopfamily="+family,int64(len(members))},
			{"2603","pg_amproc","amprocfamily="+family+" AND amprocnum=1",1},
		} {
			steps = append(steps,fixture.Select("SELECT count(*) AS n FROM pg_depend WHERE classid="+member.catalog+" AND objid IN (SELECT oid FROM "+member.table+" WHERE "+member.filter+") AND objsubid=0 AND refclassid=2616 AND refobjid="+class+" AND refobjsubid=0 AND deptype='i'", []string{"n"}, []string{"INT8"},[][]any{{member.count}}))
		}
		for number := 1; number<=len(rows); number++ {
			identity := "(SELECT oid FROM pg_amproc WHERE amprocfamily="+family+" AND amprocnum="+strconv.Itoa(number)+")"
			backer,dependency := proc("ltree_cmp",typ+","+typ),"n"
			if tc.method==405 { backer=proc("hash_ltree",typ) }
			if number==2 {
				backer,dependency=proc("hash_ltree_extended",typ+",pg_catalog.int8"),"a"
				steps=append(steps,fixture.Select("SELECT refobjid="+family+" AS family FROM pg_depend WHERE classid=2603 AND objid="+identity+" AND objsubid=0 AND refclassid=2753 AND refobjsubid=0 AND deptype='a'",[]string{"family"},[]string{"BOOL"},[][]any{{true}}))
				steps=append(steps,fixture.Select("SELECT count(*) AS n FROM pg_depend WHERE classid=2603 AND objid="+identity+" AND refclassid=2616",[]string{"n"},[]string{"INT8"},[][]any{{int64(0)}}))
			}
			steps=append(steps,fixture.Select("SELECT refobjid="+backer+" AS routine FROM pg_depend WHERE classid=2603 AND objid="+identity+" AND objsubid=0 AND refclassid=1255 AND refobjsubid=0 AND deptype='"+dependency+"'",[]string{"routine"},[]string{"BOOL"},[][]any{{true}}))
		}
	}
	for _, group := range []struct{ class string; ids []string }{{"1255",procedures},{"2617",operators},{"2616",classes},{"2753",families}} {
		steps = append(steps,fixture.Select("SELECT count(*) AS n FROM pg_depend WHERE classid="+group.class+" AND objid IN ("+strings.Join(group.ids,",")+") AND objsubid=0 AND refclassid=3079 AND refobjid="+ext+" AND refobjsubid=0 AND deptype='e'", []string{"n"}, []string{"INT8"},[][]any{{int64(len(group.ids))}}))
	}
	return steps
}

func TestReferenceLtreeComparisonCatalog(t *testing.T) {
	ltreeComparisonScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		ltreeInstall(t, p, b, c)
		for _, s := range ltreeComparisonCatalogChecks("public") {
			ltreeComparisonRun(t, p, b, c, s)
			ltreeComparisonRun(t, p, b, c, fixture.Prepared(s))
		}
	})
}
