package reference_test

import (
	"database/sql"
	"fmt"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func ownershipLtreeHierarchySignature(index int, renamed bool) string {
	name := []string{"ltree_isparent", "ltree_risparent"}[index]
	if renamed {
		name = fmt.Sprintf("eo_hierarchy_%d", index)
	}
	return "eo_new." + name + "(eo_new.ltree,eo_new.ltree)"
}

func ownershipLtreeHierarchyOperator(index int) string {
	return "eo_new." + []string{"@>", "<@"}[index] + "(eo_new.ltree,eo_new.ltree)"
}

func ownershipLtreeHierarchyTargets(renamed bool) []string {
	targets := []string{"TYPE eo_new.ltree", "FUNCTION eo_new.ltree_in(cstring)", "FUNCTION eo_new.ltree_out(eo_new.ltree)"}
	for i := range 2 {
		targets = append(targets, "FUNCTION "+ownershipLtreeHierarchySignature(i, renamed), "OPERATOR "+ownershipLtreeHierarchyOperator(i))
	}
	return targets
}

// The disjoint relation inventory preserves ancestry, self, empty ancestor,
// whole-label/non-prefix and case semantics for BOTH genuine direct APIs.
var ownershipLtreeHierarchyPairs = []struct {
	left, right string
	want        bool
}{
	{"Top", "Top.Child", true},
	{"Top.Child", "Top.Child", true},
	{"", "Top.Child", true},
	{"Top", "Topical.Child", false},
	{"Top", "top.Child", false},
	{"Top.Child", "Top.Childish", false},
}

func ownershipLtreeHierarchyStep(index, pair int, renamed, direct, prepared bool, null int) fixture.Step {
	tc := ownershipLtreeHierarchyPairs[pair]
	left, right := tc.left, tc.right
	if index == 1 {
		left, right = right, left
	}
	l, r := "'"+left+"'::eo_new.ltree", "'"+right+"'::eo_new.ltree"
	var args []any
	if prepared {
		l, r = "$1::eo_new.ltree", "$2::eo_new.ltree"
		args = []any{left, right}
	}
	var want any = tc.want
	if null == 1 {
		want = nil
		if prepared {
			args[0] = nil
		} else {
			l = "NULL::eo_new.ltree"
		}
	}
	if null == 2 {
		want = nil
		if prepared {
			args[1] = nil
		} else {
			r = "NULL::eo_new.ltree"
		}
	}
	expression := l + " OPERATOR(eo_new." + []string{"@>", "<@"}[index] + ") " + r
	if direct {
		expression = strings.Split(ownershipLtreeHierarchySignature(index, renamed), "(")[0] + "(" + l + "," + r + ")"
	}
	s := fixture.Select("SELECT "+expression+" AS v", []string{"v"}, []string{"BOOL"}, [][]any{{want}}, args...)
	if prepared {
		return fixture.Prepared(s)
	}
	return s
}

func TestReferenceExtensionOwnershipLtreeHierarchySurvivors(t *testing.T) {
	for _, foreign := range []bool{false, true} {
		state := "detached"
		if foreign {
			state = "foreign"
		}
		t.Run(state, func(t *testing.T) {
			for pair := range ownershipLtreeHierarchyPairs {
				t.Run(fmt.Sprintf("relation_%02d", pair), func(t *testing.T) { ownershipLtreeHierarchySurvivors(t, foreign, state, pair) })
			}
		})
	}
}

func ownershipLtreeHierarchySurvivors(t *testing.T, foreign bool, state string, pair int) {
	t.Helper()
	fixture.RoleReferenceCleanup(t, []string{"eo_survivor_owner", "eo_survivor_acl"})
	ownershipScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := ownershipRun(t, p, b, c)
		ownershipSurvivorRoles(run)
		run(fixture.Exec("CREATE EXTENSION ltree SCHEMA eo_old", 0))
		run(fixture.Exec("ALTER EXTENSION ltree SET SCHEMA eo_new", 0))
		var invariants []string
		var original [][][]any
		for i := range 2 {
			sig := ownershipLtreeHierarchySignature(i, false)
			ownershipSurvivorACL(run, sig)
			id := ownershipID(t, p, c, "SELECT '"+sig+"'::regprocedure::bigint")
			q := `SELECT p.oid::bigint,p.proowner::bigint,p.proacl::text,p.proargtypes::text,p.prorettype::bigint,p.proisstrict,p.provolatile,p.proretset,p.proparallel,p.procost,p.prorows,p.prosrc,p.probin FROM pg_proc p WHERE p.oid=` + ownershipNumber(id)
			invariants = append(invariants, q)
			original = append(original, ownershipSnapshot(t, p, c, q))
			ownershipSurvivorMutation(t, p, c, run, "ltree", sig)
		}
		var held []fixture.HeldStatement
		var heldSteps []fixture.Step
		for i := range 2 {
			for _, direct := range []bool{false, true} {
				for null := range 3 {
					s := ownershipLtreeHierarchyStep(i, pair, false, direct, false, null)
					view := fmt.Sprintf("eo_old.lh_witness_%d_%t_%d", i, direct, null)
					run(fixture.Exec("CREATE VIEW "+view+" AS "+s.Query, 0))
					run(fixture.Exec("GRANT SELECT ON "+view+" TO eo_survivor_acl", 0))
					s.Query = "SELECT * FROM " + view
					held = append(held, crosstabHeld(t, p, c, s.Query))
					heldSteps = append(heldSteps, s)
				}
			}
		}
		// This ordinary SQL function is an explicit operand occurrence, not
		// the selected hierarchy operator's implementation carrier.
		operand := "eo_old.lh_operand(eo_new.ltree)"
		run(fixture.Exec("CREATE FUNCTION eo_old.lh_operand(v eo_new.ltree) RETURNS eo_new.ltree LANGUAGE SQL IMMUTABLE AS 'SELECT $1'", 0))
		for i := range 2 {
			s := ownershipLtreeHierarchyStep(i, pair, false, false, false, 0)
			s.Query = strings.Replace(s.Query, "SELECT ", "SELECT eo_old.lh_operand(", 1)
			s.Query = strings.Replace(s.Query, " OPERATOR(", ") OPERATOR(", 1)
			view := fmt.Sprintf("eo_old.lh_nested_%d", i)
			run(fixture.Exec("CREATE VIEW "+view+" AS "+s.Query, 0))
			run(fixture.Exec("GRANT SELECT ON "+view+" TO eo_survivor_acl", 0))
			s.Query = "SELECT * FROM " + view
			held = append(held, crosstabHeld(t, p, c, s.Query))
			heldSteps = append(heldSteps, s)
		}
		values := func(conn *sql.Conn, recorder *fixture.Recorder, renamed bool) {
			r := ownershipRunWithRecorder(t, p, conn, recorder)
			for i := range 2 {
				for _, direct := range []bool{false, true} {
					for null := range 3 {
						for _, prepared := range []bool{false, true} {
							r(ownershipLtreeHierarchyStep(i, pair, renamed, direct, prepared, null))
						}
					}
				}
			}
			for _, s := range heldSteps {
				r(s)
				r(fixture.Prepared(s))
			}
		}
		for _, target := range ownershipLtreeHierarchyTargets(false) {
			run(fixture.Exec("ALTER EXTENSION ltree DROP "+target, 0))
		}
		for i := range 2 {
			ownershipSurvivorNonmember(run, "ltree", ownershipLtreeHierarchySignature(i, false))
		}
		values(c, b.Recorder, false)
		var signatures []string
		for i := range 2 {
			run(fixture.Exec(fmt.Sprintf("ALTER FUNCTION %s RENAME TO eo_hierarchy_%d", ownershipLtreeHierarchySignature(i, false), i), 0))
			signatures = append(signatures, ownershipLtreeHierarchySignature(i, true))
		}
		run(fixture.Exec("ALTER TYPE eo_new.ltree OWNER TO eo_survivor_owner", 0))
		for i := range 2 {
			run(fixture.Exec("ALTER OPERATOR "+ownershipLtreeHierarchyOperator(i)+" OWNER TO eo_survivor_owner", 0))
		}
		for _, target := range ownershipLtreeHierarchyTargets(true) {
			run(fixture.Exec(`ALTER EXTENSION "uuid-ossp" ADD `+target, 0))
		}
		for _, sig := range signatures {
			ownershipSurvivorMemberRestriction(run, sig, "uuid-ossp")
		}
		values(c, b.Recorder, true)
		// Retain the actual released normal codec/selectivity dependencies. There
		// is no guessed helper inventory or native-only surrogate declaration.
		query := `SELECT DISTINCT p.oid::regprocedure::text FROM pg_depend d JOIN pg_proc p ON p.oid=d.refobjid JOIN pg_depend em ON em.classid='pg_proc'::regclass AND em.objid=p.oid AND em.refclassid='pg_extension'::regclass AND em.deptype='e' JOIN pg_extension e ON e.oid=em.refobjid WHERE d.refclassid='pg_proc'::regclass AND d.deptype='n' AND e.extname='ltree' AND ((d.classid='pg_type'::regclass AND d.objid='eo_new.ltree'::regtype) OR (d.classid='pg_operator'::regclass AND d.objid IN ('` + ownershipLtreeHierarchyOperator(0) + `'::regoperator,'` + ownershipLtreeHierarchyOperator(1) + `'::regoperator)))`
		rows, err := c.QueryContext(p.Ctx, query)
		if err != nil {
			t.Fatal(err)
		}
		var helpers []string
		for rows.Next() {
			var sig string
			if err := rows.Scan(&sig); err != nil {
				rows.Close()
				t.Fatal(err)
			}
			helpers = append(helpers, sig)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		if err := rows.Close(); err != nil {
			t.Fatal(err)
		}
		for _, sig := range helpers {
			run(fixture.Exec("ALTER EXTENSION ltree DROP FUNCTION "+sig, 0))
		}
		run(fixture.Exec("DROP EXTENSION ltree", 0))
		if !foreign {
			for _, target := range ownershipLtreeHierarchyTargets(true) {
				run(fixture.Exec(`ALTER EXTENSION "uuid-ossp" DROP `+target, 0))
			}
		}
		for i, h := range held {
			fixture.RunStep(t, p.Ctx, h, b.Recorder, heldSteps[i])
			if err := h.Statement.Close(); err != nil {
				t.Fatal(err)
			}
		}
		for i, q := range invariants {
			ownershipUnchanged(t, p, c, q, original[i])
		}
		var logicalQueries []string
		for i := range 2 {
			logicalQueries = append(logicalQueries, `SELECT n.nspname,o.oprname,r.rolname,o.oprleft::regtype::text,o.oprright::regtype::text,o.oprresult::regtype::text,o.oprcode::regprocedure::text,o.oprcom::regoperator::text,o.oprnegate::regoperator::text,o.oprrest::regproc::text,o.oprjoin::regproc::text,o.oprcanhash,o.oprcanmerge FROM pg_operator o JOIN pg_namespace n ON n.oid=o.oprnamespace JOIN pg_roles r ON r.oid=o.oprowner WHERE o.oid='`+ownershipLtreeHierarchyOperator(i)+`'::regoperator`)
		}
		logicalQueries = append(logicalQueries, `SELECT n.nspname,t.typname,r.rolname,t.typacl::text,t.typinput::regprocedure::text,t.typoutput::regprocedure::text,a.typname,a.typelem::regtype::text,a.typinput::regprocedure::text,a.typoutput::regprocedure::text,t.typstorage FROM pg_type t JOIN pg_type a ON a.oid=t.typarray JOIN pg_namespace n ON n.oid=t.typnamespace JOIN pg_roles r ON r.oid=t.typowner WHERE t.oid='eo_new.ltree'::regtype`)
		logical := make([][][]any, len(logicalQueries))
		for i, q := range logicalQueries {
			logical[i] = ownershipSnapshot(t, p, c, q)
			if len(logical[i]) != 1 {
				t.Fatal("missing current hierarchy graph")
			}
		}
		check := func(conn *sql.Conn, recorder *fixture.Recorder) {
			r := ownershipRunWithRecorder(t, p, conn, recorder)
			values(conn, recorder, true)
			r(fixture.Exec("SET ROLE eo_survivor_acl", 0))
			values(conn, recorder, true)
			r(fixture.Exec("RESET ROLE", 0))
			r(ownershipCount("SELECT count(*) FROM pg_extension WHERE extname='ltree'", 0))
			for i, q := range logicalQueries {
				ownershipUnchanged(t, p, conn, q, logical[i])
			}
			member := ""
			if foreign {
				member = "uuid-ossp"
			}
			dependencies := &ownershipLtreeDependencies{}
			for i, sig := range signatures[:2] {
				dependencies.links(r, sig, foreign)
				dependencies.count("classid='pg_proc'::regclass AND objid='"+sig+"'::regprocedure AND refclassid='pg_type'::regclass AND refobjid='eo_new.ltree'::regtype AND deptype='n'", 1)
				r(fixture.Select("SELECT proisstrict,provolatile,proretset,prosrc,probin FROM pg_proc WHERE oid='"+sig+"'::regprocedure", []string{"proisstrict", "provolatile", "proretset", "prosrc", "probin"}, []string{"BOOL", "CHAR", "BOOL", "TEXT", "TEXT"}, [][]any{{true, "i", false, []string{"ltree_isparent", "ltree_risparent"}[i], "$libdir/ltree"}}))
				op := ownershipLtreeHierarchyOperator(i)
				r(ownershipCount("SELECT count(*) FROM pg_operator WHERE oid='"+op+"'::regoperator AND oprcode='"+sig+"'::regprocedure", 1))
				dependencies.count("classid='pg_operator'::regclass AND objid='"+op+"'::regoperator AND refclassid='pg_proc'::regclass AND refobjid='"+sig+"'::regprocedure AND deptype='n'", 1)
				dependencies.count("classid='pg_operator'::regclass AND objid='"+op+"'::regoperator AND refclassid='pg_namespace'::regclass AND refobjid='eo_new'::regnamespace AND deptype='n'", 1)
				dependencies.member(r, "pg_operator", "'"+op+"'::regoperator", member)
				for null := range 3 {
					ownershipLtreeCallDependencies(dependencies, fmt.Sprintf("eo_old.lh_witness_%d_true_%d", i, null), fmt.Sprintf("eo_old.lh_witness_%d_false_%d", i, null), sig, op)
				}
				ownershipLtreeNestedDependencies(dependencies, fmt.Sprintf("eo_old.lh_nested_%d", i), operand, sig, op)
			}
			for index, identity := range []string{"'eo_new.ltree'::regtype", "(SELECT typarray FROM pg_type WHERE oid='eo_new.ltree'::regtype)"} {
				dependencies.member(r, "pg_type", identity, member)
				r(ownershipCount("SELECT count(*) FROM pg_type WHERE oid="+identity+" AND typowner=(SELECT oid FROM pg_roles WHERE rolname='eo_survivor_owner')", 1))
				dependencies.count("classid='pg_type'::regclass AND objid="+identity+" AND refclassid='pg_namespace'::regclass AND refobjid='eo_new'::regnamespace AND deptype='n'", int64(1-index))
				dependencies.count("classid='pg_type'::regclass AND objid="+identity+" AND refclassid='pg_type'::regclass AND refobjid='eo_new.ltree'::regtype AND deptype='i'", int64(index))
			}
			dependencies.check(r)
		}
		signatures = append(signatures, "eo_new.ltree_in(cstring)", "eo_new.ltree_out(eo_new.ltree)", operand)
		var retainedRows []*sql.Rows
		if !b.PG {
			// Capture one real source resource per destination before either reset.
			for range 2 {
				oldRows, err := c.QueryContext(p.Ctx, heldSteps[0].Query)
				if err != nil {
					t.Fatal(err)
				}
				defer oldRows.Close()
				retainedRows = append(retainedRows, oldRows)
			}
		}
		ownershipLtreeHierarchyControls(t, p, b, c, "", state, signatures, check)
		for _, oldRows := range retainedRows {
			columns, err := oldRows.ColumnTypes()
			if err != nil || len(columns) != 1 || columns[0].Name() != "v" || columns[0].DatabaseTypeName() != "BOOL" {
				t.Fatal("retained hierarchy metadata", err)
			}
			var got bool
			if !oldRows.Next() {
				t.Fatal("retained hierarchy row missing", oldRows.Err())
			}
			if err := oldRows.Scan(&got); err != nil || got != ownershipLtreeHierarchyPairs[pair].want {
				t.Fatal("retained hierarchy value", got, err)
			}
			if oldRows.Next() || oldRows.Err() != nil {
				t.Fatal("retained hierarchy EOF", oldRows.Err())
			}
			if err := oldRows.Close(); err != nil {
				t.Fatal(err)
			}
		}
	})
}

// Retain the shared owning declaration/recorder conventions while checking the
// canonical redump on the actual restored destination, not its source pool.
func ownershipLtreeHierarchyControls(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, phase, state string, signatures []string, check func(*sql.Conn, *fixture.Recorder)) {
	t.Helper()
	check(c, b.Recorder)
	if phase == "source" || b.PG {
		return
	}
	saved := extensionDump(t, p.Ctx, p.Native)
	queries := make([]string, len(signatures))
	before := make([][][]any, len(signatures))
	for i, signature := range signatures {
		queries[i] = ownershipSurvivorDeclaration(signature)
		before[i] = ownershipSnapshot(t, p, c, queries[i])
		if len(before[i]) != 1 {
			t.Fatal("missing genuine hierarchy declaration", signature)
		}
	}
	restore := func(independent bool) {
		control, recorded, recorder := p.Native, b.DB, b.Recorder
		if independent {
			connector, err := stuntgres.NewDriver().OpenConnector("eo_hierarchy_" + state)
			if err != nil {
				t.Fatal(err)
			}
			recorded, recorder = fixture.RecordedNativePool(t, connector)
			control = sql.OpenDB(connector)
			defer control.Close()
			for _, q := range []string{`CREATE ROLE eo_survivor_owner SUPERUSER`, `CREATE ROLE eo_survivor_acl`} {
				if _, err := control.ExecContext(p.Ctx, q); err != nil {
					t.Fatal(err)
				}
			}
		}
		if err := stuntgres.Reset(p.Ctx, control); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Restore(p.Ctx, control, strings.NewReader(saved)); err != nil {
			t.Fatal(err)
		}
		target, err := recorded.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer target.Close()
		check(target, recorder)
		for i, q := range queries {
			ownershipUnchanged(t, p, target, q, before[i])
		}
		if got := extensionDump(t, p.Ctx, control); got != saved {
			t.Fatal("hierarchy destination redump changed canonical state")
		}
		for _, script := range []string{strings.Replace(saved, "stuntgres-extension-owners-v3", "stuntgres-extension-owners-v999", 1), saved + "\nCREATE EXTENSION missing_provider;\n"} {
			if err := stuntgres.Restore(p.Ctx, control, strings.NewReader(script)); err == nil {
				t.Fatal("invalid destination restore accepted")
			}
			if got := extensionDump(t, p.Ctx, control); got != saved {
				t.Fatal("failed destination restore published partial state")
			}
		}
		check(target, recorder)
	}
	if phase == "" || phase == "same_driver" {
		restore(false)
	}
	if phase == "" || phase == "independent_driver" {
		restore(true)
	}
}
