package reference_test

import (
	"database/sql"
	"strconv"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func cubeComparisonCatalogChecks(schema string) []fixture.Step {
	// Probe each real declaration by an independently specified identity. Avoid
	// multiplying whole virtual catalogs in the repeated replacement controls.
	cube := schema + ".cube"
	cubeOID := "('" + cube + "'::regtype)::oid"
	namespaceOID := "('" + schema + "'::regnamespace)::oid"
	extensionOID := "(SELECT oid FROM pg_extension WHERE extname='cube')"
	ownerOID := "(SELECT extowner FROM pg_extension WHERE extname='cube')"
	classFilter := "opcname='cube_ops' AND opcnamespace=" + namespaceOID + " AND opcmethod=403"
	familyFilter := "opfname='cube_ops' AND opfnamespace=" + namespaceOID + " AND opfmethod=403"
	classOID := "(SELECT oid FROM pg_opclass WHERE " + classFilter + ")"
	familyOID := "(SELECT oid FROM pg_opfamily WHERE " + familyFilter + ")"
	procedureOID := func(name, args string) string {
		return "('" + schema + "." + name + "(" + args + ")'::regprocedure)::oid"
	}
	operatorOID := func(name string) string {
		return "('" + schema + "." + name + "(" + cube + "," + cube + ")'::regoperator)::oid"
	}
	steps := []fixture.Step{
		fixture.Select("SELECT extnamespace="+namespaceOID+" AS namespace FROM pg_extension WHERE extname='cube'", []string{"namespace"}, []string{"BOOL"}, [][]any{{true}}),
		fixture.Select("SELECT typname,typnamespace="+namespaceOID+" AS namespace,typowner="+ownerOID+" AS owner FROM pg_type WHERE oid="+cubeOID, []string{"typname", "namespace", "owner"}, []string{"NAME", "BOOL", "BOOL"}, [][]any{{"cube", true, true}}),
	}
	var procedures []string
	for _, tc := range []struct {
		name, args, result string
		arity              int64
	}{
		{"cube_in", "pg_catalog.cstring", cubeOID, 1},
		{"cube_out", cube, "2275::oid", 1},
		{"cube_cmp", cube + "," + cube, "23::oid", 2},
		{"cube_eq", cube + "," + cube, "16::oid", 2},
		{"cube_ne", cube + "," + cube, "16::oid", 2},
		{"cube_lt", cube + "," + cube, "16::oid", 2},
		{"cube_le", cube + "," + cube, "16::oid", 2},
		{"cube_gt", cube + "," + cube, "16::oid", 2},
		{"cube_ge", cube + "," + cube, "16::oid", 2},
	} {
		identity := procedureOID(tc.name, tc.args)
		procedures = append(procedures, identity)
		steps = append(steps, fixture.Select(
			"SELECT proname,prorettype="+tc.result+" AS result,pronargs,proisstrict,provolatile,proparallel,pronamespace="+namespaceOID+" AS namespace,proowner="+ownerOID+" AS owner FROM pg_proc WHERE oid="+identity,
			[]string{"proname", "result", "pronargs", "proisstrict", "provolatile", "proparallel", "namespace", "owner"},
			[]string{"NAME", "BOOL", "INT2", "BOOL", "CHAR", "CHAR", "BOOL", "BOOL"},
			[][]any{{tc.name, true, tc.arity, true, "i", "s", true, true}},
		))
	}
	var operators []string
	for _, tc := range []struct {
		name, procedure, commutator, negator, restrictor, joiner string
		merge                                                    bool
	}{
		{"<", "cube_lt", ">", ">=", "scalarltsel", "scalarltjoinsel", false},
		{"<=", "cube_le", ">=", ">", "scalarlesel", "scalarlejoinsel", false},
		{"<>", "cube_ne", "<>", "=", "neqsel", "neqjoinsel", false},
		{"=", "cube_eq", "=", "<>", "eqsel", "eqjoinsel", true},
		{">", "cube_gt", "<", "<=", "scalargtsel", "scalargtjoinsel", false},
		{">=", "cube_ge", "<=", "<", "scalargesel", "scalargejoinsel", false},
	} {
		identity := operatorOID(tc.name)
		operators = append(operators, identity)
		// These are actual opcode/commutator/negator references, not names
		// inferred from output. The exact declarations are probed separately.
		// Estimators remain lookup-only identities, never phantom pg_proc rows.
		steps = append(steps, fixture.Select(
			"SELECT oprname,oprcode::oid="+procedureOID(tc.procedure, cube+","+cube)+" AS procedure_identity,oprcom="+operatorOID(tc.commutator)+" AS commutator_identity,oprnegate="+operatorOID(tc.negator)+" AS negator_identity,oprnamespace="+namespaceOID+" AS namespace,oprleft="+cubeOID+" AS leftarg,oprright="+cubeOID+" AS rightarg,oprcanmerge,oprcanhash,oprrest::oid='pg_catalog."+tc.restrictor+"'::regproc::oid AS restrictor_identity,oprjoin::oid='pg_catalog."+tc.joiner+"'::regproc::oid AS joiner_identity,oprowner="+ownerOID+" AS owner FROM pg_operator WHERE oid="+identity,
			[]string{"oprname", "procedure_identity", "commutator_identity", "negator_identity", "namespace", "leftarg", "rightarg", "oprcanmerge", "oprcanhash", "restrictor_identity", "joiner_identity", "owner"},
			[]string{"NAME", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL"},
			[][]any{{tc.name, true, true, true, true, true, true, tc.merge, false, true, true, true}},
		))
	}
	steps = append(steps,
		fixture.Select("SELECT opcname,opcdefault,opcmethod::int AS opcmethod,opcnamespace="+namespaceOID+" AS namespace,opcintype="+cubeOID+" AS input,opcfamily="+familyOID+" AS family_identity,opckeytype::int AS opckeytype,opcowner="+ownerOID+" AS owner FROM pg_opclass WHERE "+classFilter, []string{"opcname", "opcdefault", "opcmethod", "namespace", "input", "family_identity", "opckeytype", "owner"}, []string{"NAME", "BOOL", "INT4", "BOOL", "BOOL", "BOOL", "INT4", "BOOL"}, [][]any{{"cube_ops", true, int64(403), true, true, true, int64(0), true}}),
		fixture.Select("SELECT opfname,opfmethod::int AS opfmethod,opfnamespace="+namespaceOID+" AS namespace,opfowner="+ownerOID+" AS owner FROM pg_opfamily WHERE "+familyFilter, []string{"opfname", "opfmethod", "namespace", "owner"}, []string{"NAME", "INT4", "BOOL", "BOOL"}, [][]any{{"cube_ops", int64(403), true, true}}),
		fixture.Select("SELECT opcname FROM pg_opclass WHERE opcintype="+cubeOID+" AND opcmethod=405", []string{"opcname"}, []string{"NAME"}, [][]any{}),
	)
	strategy := "CASE amopstrategy"
	for i, name := range []string{"<", "<=", "=", ">=", ">"} {
		strategy += " WHEN " + strconv.Itoa(i+1) + " THEN " + operatorOID(name)
	}
	strategy += " END"
	steps = append(steps,
		fixture.Select("SELECT amopstrategy,amopopr="+strategy+" AS operator_identity,amoplefttype="+cubeOID+" AS leftarg,amoprighttype="+cubeOID+" AS rightarg,amopmethod::int AS amopmethod,amoppurpose FROM pg_amop WHERE amopfamily="+familyOID+" ORDER BY amopstrategy", []string{"amopstrategy", "operator_identity", "leftarg", "rightarg", "amopmethod", "amoppurpose"}, []string{"INT2", "BOOL", "BOOL", "BOOL", "INT4", "CHAR"}, [][]any{{int64(1), true, true, true, int64(403), "s"}, {int64(2), true, true, true, int64(403), "s"}, {int64(3), true, true, true, int64(403), "s"}, {int64(4), true, true, true, int64(403), "s"}, {int64(5), true, true, true, int64(403), "s"}}),
		fixture.Select("SELECT amprocnum,amproc::oid="+procedureOID("cube_cmp", cube+","+cube)+" AS signature,amproclefttype="+cubeOID+" AS leftarg,amprocrighttype="+cubeOID+" AS rightarg FROM pg_amproc WHERE amprocfamily="+familyOID+" ORDER BY amprocnum", []string{"amprocnum", "signature", "leftarg", "rightarg"}, []string{"INT2", "BOOL", "BOOL", "BOOL"}, [][]any{{int64(1), true, true, true}}),
	)
	// Check all18 direct members by catalog class and their exact declaration
	// IDs, retaining both ownership and membership proofs without catalog joins.
	for _, group := range []struct {
		class string
		ids   []string
		count int64
	}{
		{"1247", []string{cubeOID}, 1},
		{"1255", procedures, 9},
		{"2617", operators, 6},
		{"2616", []string{classOID}, 1},
		{"2753", []string{familyOID}, 1},
	} {
		steps = append(steps, fixture.Select(
			"SELECT count(*) AS n FROM pg_depend WHERE classid="+group.class+" AND objid IN ("+strings.Join(group.ids, ",")+") AND objsubid=0 AND refclassid=3079 AND refobjid="+extensionOID+" AND refobjsubid=0 AND deptype='e'",
			[]string{"n"}, []string{"INT8"}, [][]any{{group.count}},
		))
	}
	return steps
}

func TestReferenceCubeComparisonIndexes(t *testing.T) {
	cubeComparisonScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		cubeInstall(t, p, b, c)
		cubeComparisonTable(t, p, b, c)
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		for _, s := range cubeComparisonCatalogChecks("public") {
			t.Run(s.Query, func(t *testing.T) {
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
			})
		}
		run(fixture.Exec("CREATE INDEX cube_compare_default ON cube_compare_data(v)", 0))
		run(fixture.Prepared(fixture.Exec("CREATE INDEX cube_compare_explicit ON cube_compare_data USING btree(v public.cube_ops)", 0)))
		for _, s := range []fixture.Step{
			cubeComparisonIDs("SELECT id FROM cube_compare_data WHERE v>='(0)'::cube AND v<'(2)'::cube ORDER BY v,id", 6, 7, 2, 3),
			cubeComparisonIDs("SELECT id FROM cube_compare_data WHERE v>'(0)'::cube ORDER BY v DESC,id", 1, 2, 3),
			cubeComparisonIDs("SELECT id FROM cube_compare_data WHERE v='[(1),(1)]'::cube ORDER BY id", 2, 3),
		} {
			run(s)
			run(fixture.Prepared(s))
		}
		cubeComparisonHeldConsumer(t, p, b, c)
		run(fixture.Exec("CREATE TABLE cube_compare_unique(id integer,v cube)", 0))
		run(fixture.Exec("CREATE UNIQUE INDEX cube_compare_unique_v ON cube_compare_unique(v)", 0))
		run(fixture.Exec("INSERT INTO cube_compare_unique VALUES (1,'(3,1),(1,4)'),(2,'-0'),(3,'2'),(4,'3'),(5,NULL),(6,NULL)", 6))
		baseline := cubeComparisonIDs("SELECT id FROM cube_compare_unique ORDER BY id", 1, 2, 3, 4, 5, 6)
		for _, tc := range []struct {
			name, q, value string
			args           []any
		}{
			{"reversed", "INSERT INTO cube_compare_unique VALUES (7,'(1,4),(3,1)')", "(1, 4),(3, 1)", nil},
			{"per_axis", "INSERT INTO cube_compare_unique VALUES (7,$1)", "(1, 1),(3, 4)", []any{"(1,1),(3,4)"}},
			{"signed_zero", "INSERT INTO cube_compare_unique VALUES (7,$1)", "(0)", []any{[]byte("0")}},
			{"late_duplicate", "INSERT INTO cube_compare_unique VALUES (7,'4'),(8,'2')", "(2)", nil},
		} {
			t.Run(tc.name, func(t *testing.T) {
				for _, prepared := range []bool{false, true} {
					want := cubeComparisonUniqueDiagnostic("cube_compare_unique", "cube_compare_unique_v", tc.value)
					cubeError(t, p, b, c, tc.q, prepared, false, false, want, tc.args...)
					fixture.RunStep(t, p.Ctx, c, b.Recorder, baseline)
				}
			})
		}
	})
}
