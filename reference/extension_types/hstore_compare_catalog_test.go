package reference_test

import (
	"database/sql"
	"strconv"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func hstoreComparisonCatalogChecks(schema string) []fixture.Step {
	hstore := schema + ".hstore"
	typeOID := "('" + hstore + "'::regtype)::oid"
	ns := "('" + schema + "'::regnamespace)::oid"
	owner := "(SELECT extowner FROM pg_extension WHERE extname='hstore')"
	ext := "(SELECT oid FROM pg_extension WHERE extname='hstore')"
	proc := func(name, args string) string {
		return "('" + schema + "." + name + "(" + args + ")'::regprocedure)::oid"
	}
	op := func(name string) string {
		return "('" + schema + "." + name + "(" + hstore + "," + hstore + ")'::regoperator)::oid"
	}
	steps := []fixture.Step{
		fixture.Select("SELECT extnamespace="+ns+" AS namespace FROM pg_extension WHERE extname='hstore'", []string{"namespace"}, []string{"BOOL"}, [][]any{{true}}),
		fixture.Select("SELECT typname,typnamespace="+ns+" AS namespace,typowner="+owner+" AS owner FROM pg_type WHERE oid="+typeOID, []string{"typname", "namespace", "owner"}, []string{"NAME", "BOOL", "BOOL"}, [][]any{{"hstore", true, true}}),
		fixture.Select("SELECT oprname FROM pg_operator WHERE oprleft="+typeOID+" AND oprright="+typeOID+" AND oprname IN ('<','<=','>','>=') ORDER BY oprname", []string{"oprname"}, []string{"NAME"}, [][]any{}),
	}
	var procedures, operators, classes, families []string
	for _, tc := range []struct {
		name, args, result string
		arity              int64
	}{
		{"hstore_eq", hstore + "," + hstore, "16", 2},
		{"hstore_ne", hstore + "," + hstore, "16", 2},
		{"hstore_lt", hstore + "," + hstore, "16", 2},
		{"hstore_le", hstore + "," + hstore, "16", 2},
		{"hstore_gt", hstore + "," + hstore, "16", 2},
		{"hstore_ge", hstore + "," + hstore, "16", 2},
		{"hstore_cmp", hstore + "," + hstore, "23", 2},
		{"hstore_hash", hstore, "23", 1},
		{"hstore_hash_extended", hstore + ",pg_catalog.int8", "20", 2},
	} {
		identity := proc(tc.name, tc.args)
		procedures = append(procedures, identity)
		steps = append(steps, fixture.Select("SELECT proname,prorettype="+tc.result+"::oid AS result,pronargs,proisstrict,provolatile,proparallel,pronamespace="+ns+" AS namespace,proowner="+owner+" AS owner,prolang=13::oid AS c_language,prosrc,probin FROM pg_proc WHERE oid="+identity,
			[]string{"proname", "result", "pronargs", "proisstrict", "provolatile", "proparallel", "namespace", "owner", "c_language", "prosrc", "probin"},
			[]string{"NAME", "BOOL", "INT2", "BOOL", "CHAR", "CHAR", "BOOL", "BOOL", "BOOL", "TEXT", "TEXT"},
			[][]any{{tc.name, true, tc.arity, true, "i", "s", true, true, true, tc.name, "$libdir/hstore"}}))
	}
	// 1.4--1.5 upgrades <= and >= to their proper inclusive estimators.
	for _, tc := range []struct {
		name, function, commutator, negator, restrictor, joiner string
		equality                                              bool
	}{
		{"=", "eq", "=", "<>", "eqsel", "eqjoinsel", true},
		{"<>", "ne", "<>", "=", "neqsel", "neqjoinsel", false},
		{"#<#", "lt", "#>#", "#>=#", "scalarltsel", "scalarltjoinsel", false},
		{"#<=#", "le", "#>=#", "#>#", "scalarlesel", "scalarlejoinsel", false},
		{"#>#", "gt", "#<#", "#<=#", "scalargtsel", "scalargtjoinsel", false},
		{"#>=#", "ge", "#<=#", "#<#", "scalargesel", "scalargejoinsel", false},
	} {
		identity := op(tc.name)
		operators = append(operators, identity)
		steps = append(steps, fixture.Select("SELECT oprname,oprcode::oid="+proc("hstore_"+tc.function, hstore+","+hstore)+" AS procedure_identity,oprcom="+op(tc.commutator)+" AS commutator_identity,oprnegate="+op(tc.negator)+" AS negator_identity,oprnamespace="+ns+" AS namespace,oprleft="+typeOID+" AS leftarg,oprright="+typeOID+" AS rightarg,oprresult=16::oid AS result,oprcanmerge,oprcanhash,oprrest::oid='pg_catalog."+tc.restrictor+"'::regproc::oid AS restrictor_identity,oprjoin::oid='pg_catalog."+tc.joiner+"'::regproc::oid AS joiner_identity,oprowner="+owner+" AS owner FROM pg_operator WHERE oid="+identity,
			[]string{"oprname", "procedure_identity", "commutator_identity", "negator_identity", "namespace", "leftarg", "rightarg", "result", "oprcanmerge", "oprcanhash", "restrictor_identity", "joiner_identity", "owner"},
			[]string{"NAME", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL"},
			[][]any{{tc.name, true, true, true, true, true, true, true, tc.equality, tc.equality, true, true, true}}))
	}
	for _, tc := range []struct {
		name   string
		method int
		ops    []string
	}{
		{"btree_hstore_ops", 403, []string{"#<#", "#<=#", "=", "#>=#", "#>#"}},
		{"hash_hstore_ops", 405, []string{"="}},
	} {
		method := strconv.Itoa(tc.method)
		classFilter := "opcname='" + tc.name + "' AND opcnamespace=" + ns + " AND opcmethod=" + method
		familyFilter := "opfname='" + tc.name + "' AND opfnamespace=" + ns + " AND opfmethod=" + method
		class := "(SELECT oid FROM pg_opclass WHERE " + classFilter + ")"
		family := "(SELECT oid FROM pg_opfamily WHERE " + familyFilter + ")"
		classes, families = append(classes, class), append(families, family)
		steps = append(steps,
			fixture.Select("SELECT opcname,opcdefault,opcmethod::int AS method,opcnamespace="+ns+" AS namespace,opcintype="+typeOID+" AS input,opcfamily="+family+" AS family,opckeytype::int AS keytype,opcowner="+owner+" AS owner FROM pg_opclass WHERE "+classFilter, []string{"opcname", "opcdefault", "method", "namespace", "input", "family", "keytype", "owner"}, []string{"NAME", "BOOL", "INT4", "BOOL", "BOOL", "BOOL", "INT4", "BOOL"}, [][]any{{tc.name, true, int64(tc.method), true, true, true, int64(0), true}}),
			fixture.Select("SELECT opfname,opfmethod::int AS method,opfnamespace="+ns+" AS namespace,opfowner="+owner+" AS owner FROM pg_opfamily WHERE "+familyFilter, []string{"opfname", "method", "namespace", "owner"}, []string{"NAME", "INT4", "BOOL", "BOOL"}, [][]any{{tc.name, int64(tc.method), true, true}}),
		)
		strategy := "CASE amopstrategy"
		var members [][]any
		for i, name := range tc.ops {
			strategy += " WHEN " + strconv.Itoa(i+1) + " THEN " + op(name)
			members = append(members, []any{int64(i + 1), true, true, true, int64(tc.method), "s"})
		}
		strategy += " END"
		steps = append(steps, fixture.Select("SELECT amopstrategy,amopopr="+strategy+" AS operator_identity,amoplefttype="+typeOID+" AS leftarg,amoprighttype="+typeOID+" AS rightarg,amopmethod::int AS method,amoppurpose FROM pg_amop WHERE amopfamily="+family+" ORDER BY amopstrategy", []string{"amopstrategy", "operator_identity", "leftarg", "rightarg", "method", "amoppurpose"}, []string{"INT2", "BOOL", "BOOL", "BOOL", "INT4", "CHAR"}, members))
		support := proc("hstore_cmp", hstore+","+hstore)
		rows := [][]any{{int64(1), true, true, true}}
		if tc.method == 405 {
			support = "CASE amprocnum WHEN 1 THEN " + proc("hstore_hash", hstore) + " WHEN 2 THEN " + proc("hstore_hash_extended", hstore+",pg_catalog.int8") + " END"
			rows = append(rows, []any{int64(2), true, true, true})
		}
		steps = append(steps, fixture.Select("SELECT amprocnum,amproc::oid="+support+" AS signature,amproclefttype="+typeOID+" AS leftarg,amprocrighttype="+typeOID+" AS rightarg FROM pg_amproc WHERE amprocfamily="+family+" ORDER BY amprocnum", []string{"amprocnum", "signature", "leftarg", "rightarg"}, []string{"INT2", "BOOL", "BOOL", "BOOL"}, rows))
	}
	for _, group := range []struct {
		class string
		ids   []string
	}{{"1255", procedures}, {"2617", operators}, {"2616", classes}, {"2753", families}} {
		steps = append(steps, fixture.Select("SELECT count(*) AS n FROM pg_depend WHERE classid="+group.class+" AND objid IN ("+strings.Join(group.ids, ",")+") AND objsubid=0 AND refclassid=3079 AND refobjid="+ext+" AND refobjsubid=0 AND deptype='e'", []string{"n"}, []string{"INT8"}, [][]any{{int64(len(group.ids))}}))
	}
	return steps
}

func TestReferenceHstoreComparisonCatalog(t *testing.T) {
	hstoreComparisonScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		hstoreInstall(t, p, b, c)
		for _, s := range hstoreComparisonCatalogChecks("public") {
			hstoreRun(t, p, b, c, s)
			hstoreRun(t, p, b, c, fixture.Prepared(s))
		}
	})
}
