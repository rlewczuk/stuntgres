package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func segIntervalCatalogChecks(schema string) []fixture.Step {
	// Each exact regprocedure/regoperator spelling independently identifies both
	// actual seg arguments. Single-catalog projections and scalar subqueries
	// reuse the existing catalog assertion shape without oidvector or join
	// expansion requirements.
	arguments := schema + ".seg," + schema + ".seg"
	procedure := func(name string) string {
		return queryIntLiteral(schema+"."+name+"("+arguments+")") + "::regprocedure::oid"
	}
	operator := func(name string) string {
		return queryIntLiteral(schema+"."+name+"("+arguments+")") + "::regoperator::oid"
	}
	namespace := queryIntLiteral(schema) + "::regnamespace::oid"
	seg := queryIntLiteral(schema+".seg") + "::regtype::oid"
	extensionID := "(SELECT oid FROM pg_extension WHERE extname='seg')"
	extensionNamespace := "(SELECT extnamespace FROM pg_extension WHERE extname='seg')"
	extensionOwner := "(SELECT extowner FROM pg_extension WHERE extname='seg')"
	var checks []fixture.Step
	for i, name := range segIntervalRoutines {
		member := struct {
			procedure, operator, commutator string
			result                          int64
		}{name, segIntervalOperators[i], []string{">>", "<<", "", "", "=", "&&", "<@", "@>"}[i], 16}
		procID, opID := procedure(member.procedure), operator(member.operator)
		commutator := "0::oid"
		if member.commutator != "" {
			commutator = operator(member.commutator)
		}
		procFacts := "SELECT proname AS name,pronargs::integer AS nargs,prorettype::integer AS result,proisstrict AS strict,provolatile::text AS volatility,proparallel::text AS parallel,prokind::text AS kind,oid>0 AS identity,(pronamespace=" + namespace + " AND pronamespace=" + extensionNamespace + " AND proowner=" + extensionOwner + ") AS authority FROM pg_proc WHERE oid=" + procID
		checks = append(checks, fixture.Select(procFacts,
			[]string{"name", "nargs", "result", "strict", "volatility", "parallel", "kind", "identity", "authority"},
			[]string{"NAME", "INT4", "INT4", "BOOL", "TEXT", "TEXT", "TEXT", "BOOL", "BOOL"},
			[][]any{{member.procedure, int64(2), member.result, true, "i", "s", "f", true, true}}))
		checks = append(checks, fixture.Select("SELECT deptype AS dependency FROM pg_depend WHERE classid=1255 AND objid="+procID+" AND refclassid=3079 AND refobjid="+extensionID,
			[]string{"dependency"}, []string{"CHAR"}, [][]any{{"e"}}))
		opFacts := "SELECT oprname AS name,oprresult::integer AS result,oprcode::oid=" + procID + " AS procedure,(oprleft=" + seg + " AND oprright=" + seg + ") AS signature,oprcom=" + commutator + " AS commutator,oid>0 AS identity,(oprnamespace=" + namespace + " AND oprnamespace=" + extensionNamespace + " AND oprowner=" + extensionOwner + ") AS authority,oprcanhash AS hash,oprcanmerge AS merge FROM pg_operator WHERE oid=" + opID
		checks = append(checks, fixture.Select(opFacts,
			[]string{"name", "result", "procedure", "signature", "commutator", "identity", "authority", "hash", "merge"},
			[]string{"NAME", "INT4", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL"},
			[][]any{{member.operator, member.result, true, true, true, true, true, false, member.operator == "="}}))
		checks = append(checks, fixture.Select("SELECT oprrest::oid::integer AS restriction,oprjoin::oid::integer AS join_estimator FROM pg_operator WHERE oid="+opID, []string{"restriction", "join_estimator"}, []string{"INT4", "INT4"}, [][]any{{[]int64{1300, 1300, 1300, 1300, 101, 139, 1302, 1302}[i], []int64{1301, 1301, 1301, 1301, 105, 140, 1303, 1303}[i]}}))
		checks = append(checks, fixture.Select("SELECT prosrc,probin,prolang=(SELECT oid FROM pg_language WHERE lanname='c') AS language FROM pg_proc WHERE oid="+procID, []string{"prosrc", "probin", "language"}, []string{"TEXT", "TEXT", "BOOL"}, [][]any{{member.procedure, "$libdir/seg", true}}))
		checks = append(checks, fixture.Select("SELECT deptype AS dependency FROM pg_depend WHERE classid=2617 AND objid="+opID+" AND refclassid=3079 AND refobjid="+extensionID,
			[]string{"dependency"}, []string{"CHAR"}, [][]any{{"e"}}))
	}
	return checks
}

// Construction order is a fresh-install contract, not a requirement on
// selectively reminted native identities. Keep it separate from recipe checks.
func segIntervalConstructionChecks(schema string) []fixture.Step {
	scalar := queryIntLiteral(schema+".seg") + "::regtype::oid"
	input := queryIntLiteral(schema+".seg_in(cstring)") + "::regprocedure::oid"
	output := queryIntLiteral(schema+".seg_out("+schema+".seg)") + "::regprocedure::oid"
	array := "(SELECT typarray FROM pg_type WHERE oid=" + scalar + ")"
	checks := []fixture.Step{fixture.Select(
		"SELECT oid>0 AS scalar_identity,typarray>0 AS array_identity,oid<"+input+" AS input_order,"+input+"<"+output+" AS output_order,"+output+"<typarray AS array_order,(SELECT typelem FROM pg_type WHERE oid="+array+")="+scalar+" AS element FROM pg_type WHERE oid="+scalar,
		[]string{"scalar_identity", "array_identity", "input_order", "output_order", "array_order", "element"},
		[]string{"BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL"},
		[][]any{{true, true, true, true, true, true}},
	)}
	arguments := schema + ".seg," + schema + ".seg"
	for i, name := range segIntervalRoutines {
		procedure := queryIntLiteral(schema+"."+name+"("+arguments+")") + "::regprocedure::oid"
		operator := queryIntLiteral(schema+"."+segIntervalOperators[i]+"("+arguments+")") + "::regoperator::oid"
		checks = append(checks, fixture.Select(
			"SELECT "+array+"<"+procedure+" AS procedure_order,"+array+"<"+operator+" AS operator_order",
			[]string{"procedure_order", "operator_order"}, []string{"BOOL", "BOOL"}, [][]any{{true, true}},
		))
	}
	return checks
}

func TestReferenceSegIntervalInstallation(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "seg_interval_provider")
	fixture.SchemaReferenceCleanup(t, "seg_interval_shadow")
	queryIntScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE SCHEMA seg_interval_provider", 0))
		run(fixture.Exec("CREATE SCHEMA seg_interval_shadow", 0))
		run(fixture.Exec("CREATE EXTENSION seg WITH SCHEMA seg_interval_provider", 0))
		for _, s := range append(segIntervalCatalogChecks("seg_interval_provider"), segIntervalConstructionChecks("seg_interval_provider")...) {
			segIntervalBoth(t, p, b, c, s)
		}
		run(fixture.Exec("SET search_path=pg_catalog", 0))
		q := "SELECT seg_interval_provider.seg_overlap('0 .. 1'::seg_interval_provider.seg,'1 .. 2'::seg_interval_provider.seg) AS overlap,'1'::seg_interval_provider.seg OPERATOR(seg_interval_provider.=) '1'::seg_interval_provider.seg AS same"
		segIntervalBoth(t, p, b, c, segIntervalSelect(b, q, []string{"overlap", "same"}, []string{"BOOL", "BOOL"}, [][]any{{true, true}}, false))
		run(fixture.Exec("SET search_path=seg_interval_shadow,seg_interval_provider,pg_catalog", 0))
		q = "SELECT seg_overlap('1'::seg,'1'::seg) AS overlap,'1'::seg && '1'::seg AS hit"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		selected := segIntervalSelect(b, q, []string{"overlap", "hit"}, []string{"BOOL", "BOOL"}, [][]any{{true, true}}, false)
		fixture.RunStep(t, p.Ctx, held, b.Recorder, selected)
		run(fixture.Exec("CREATE VIEW public.seg_interval_resolved AS "+q, 0))
		run(fixture.Exec("CREATE FUNCTION seg_interval_shadow.seg_overlap(seg_interval_provider.seg,seg_interval_provider.seg) RETURNS boolean LANGUAGE SQL AS 'SELECT false'", 0))
		run(fixture.Exec("CREATE OPERATOR seg_interval_shadow.&& (LEFTARG=seg_interval_provider.seg,RIGHTARG=seg_interval_provider.seg,FUNCTION=seg_interval_shadow.seg_overlap)", 0))
		shadow := segIntervalSelect(b, q, []string{"overlap", "hit"}, []string{"BOOL", "BOOL"}, [][]any{{false, false}}, false)
		segIntervalBoth(t, p, b, c, shadow)
		fixture.RunStep(t, p.Ctx, held, b.Recorder, shadow)
		segIntervalBoth(t, p, b, c, fixture.Select("SELECT overlap,hit FROM public.seg_interval_resolved", []string{"overlap", "hit"}, []string{"BOOL", "BOOL"}, [][]any{{true, true}}))
		run(fixture.Exec("ALTER EXTENSION seg SET SCHEMA public", 0))
		for _, s := range append(segIntervalCatalogChecks("public"), segIntervalConstructionChecks("public")...) {
			segIntervalBoth(t, p, b, c, s)
		}
		run(fixture.Exec("SET search_path=public,pg_catalog", 0))
		segIntervalBoth(t, p, b, c, segIntervalSelect(b, "SELECT seg_overlap('1'::seg,'1'::seg) AS v", []string{"v"}, []string{"BOOL"}, [][]any{{true}}, false))
		run(fixture.Exec("DROP EXTENSION seg CASCADE", 0))
		run(fixture.Select("SELECT relname FROM pg_class WHERE relname='seg_interval_resolved'", []string{"relname"}, []string{"NAME"}, [][]any{}))
		run(fixture.Exec("CREATE EXTENSION seg", 0))
		for _, s := range append(segIntervalCatalogChecks("public"), segIntervalConstructionChecks("public")...) {
			segIntervalBoth(t, p, b, c, s)
		}
	})
}
