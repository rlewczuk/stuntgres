package reference_test

import (
	"database/sql"
	"fmt"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

// The nine released C declarations are metadata/backer authority, not extra
// selected SQL helper APIs. Runtime coverage uses the six documented operators
// and their genuine ordering and B-tree/hash index support routes.
var ownershipLtreeComparisonCases = []struct {
	name, args string
}{
	{"ltree_eq", "eo_new.ltree,eo_new.ltree"},
	{"ltree_ne", "eo_new.ltree,eo_new.ltree"},
	{"ltree_lt", "eo_new.ltree,eo_new.ltree"},
	{"ltree_le", "eo_new.ltree,eo_new.ltree"},
	{"ltree_gt", "eo_new.ltree,eo_new.ltree"},
	{"ltree_ge", "eo_new.ltree,eo_new.ltree"},
	{"ltree_cmp", "eo_new.ltree,eo_new.ltree"},
	{"hash_ltree", "eo_new.ltree"},
	{"hash_ltree_extended", "eo_new.ltree,bigint"},
}

func ownershipLtreeComparisonSignature(index int, renamed bool) string {
	tc := ownershipLtreeComparisonCases[index]
	name := tc.name
	if renamed {
		name = fmt.Sprintf("eo_compare_%d", index)
	}
	return "eo_new." + name + "(" + tc.args + ")"
}

func ownershipLtreeComparisonTargets(renamed bool) []string {
	targets := []string{"TYPE eo_new.ltree", "FUNCTION eo_new.ltree_in(cstring)", "FUNCTION eo_new.ltree_out(eo_new.ltree)"}
	for i := range ownershipLtreeComparisonCases {
		targets = append(targets, "FUNCTION "+ownershipLtreeComparisonSignature(i, renamed))
	}
	operatorSchema := "eo_new"
	if renamed {
		operatorSchema = "eo_other"
	}
	for _, op := range []string{"=", "<>", "<", "<=", ">", ">="} {
		targets = append(targets, "OPERATOR "+operatorSchema+"."+op+"(eo_new.ltree,eo_new.ltree)")
	}
	for _, entry := range []struct{ name, method string }{{"ltree_ops", "btree"}, {"hash_ltree_ops", "hash"}} {
		targets = append(targets, "OPERATOR CLASS eo_new."+entry.name+" USING "+entry.method, "OPERATOR FAMILY eo_new."+entry.name+" USING "+entry.method)
	}
	return targets
}

func ownershipLtreeComparisonStep(index int, relocated, prepared bool, null int) fixture.Step {
	left, right := "'a.b'::eo_new.ltree", "'a-b'::eo_new.ltree"
	var args []any
	if prepared {
		left, right = "$1::eo_new.ltree", "$2::eo_new.ltree"
		args = []any{"a.b", "a-b"}
	}
	want := any([]bool{false, true, true, true, false, false}[index])
	if null == 1 {
		want = nil
		if prepared {
			args[0] = nil
		} else {
			left = "NULL::eo_new.ltree"
		}
	}
	if null == 2 {
		want = nil
		if prepared {
			args[1] = nil
		} else {
			right = "NULL::eo_new.ltree"
		}
	}
	schema := "eo_new"
	if relocated {
		schema = "eo_other"
	}
	expression := left + " OPERATOR(" + schema + "." + []string{"=", "<>", "<", "<=", ">", ">="}[index] + ") " + right
	s := fixture.Select("SELECT "+expression+" AS v", []string{"v"}, []string{"BOOL"}, [][]any{{want}}, args...)
	if prepared {
		return fixture.Prepared(s)
	}
	return s
}

// Each leaf shares one complete genuine comparison/index source graph across
// both destinations. Disjoint three-routine groups bound values and metadata.
func TestReferenceExtensionOwnershipLtreeComparisonSurvivors(t *testing.T) {
	for _, foreign := range []bool{false, true} {
		state := "detached"
		if foreign {
			state = "foreign"
		}
		t.Run(state, func(t *testing.T) {
			for start := 0; start < len(ownershipLtreeComparisonCases); start += 3 {
				t.Run(fmt.Sprintf("inventory_%02d", start), func(t *testing.T) {
					ownershipLtreeComparisonSurvivors(t, foreign, state, start)
				})
			}
		})
	}
}

func ownershipLtreeComparisonSurvivors(t *testing.T, foreign bool, state string, start int) {
	t.Helper()
	fixture.RoleReferenceCleanup(t, []string{"eo_survivor_owner", "eo_survivor_acl"})
	ownershipScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := ownershipRun(t, p, b, c)
		ownershipSurvivorRoles(run)
		run(fixture.Exec("GRANT USAGE ON SCHEMA eo_other TO eo_survivor_acl", 0))
		run(fixture.Exec("CREATE EXTENSION ltree SCHEMA eo_old", 0))
		run(fixture.Exec("ALTER EXTENSION ltree SET SCHEMA eo_new", 0))
		for i := range ownershipLtreeComparisonCases {
			ownershipSurvivorACL(run, ownershipLtreeComparisonSignature(i, false))
		}
		var invariantQueries []string
		var originalDeclarations [][][]any
		for i := start; i < start+3; i++ {
			id := ownershipID(t, p, c, "SELECT '"+ownershipLtreeComparisonSignature(i, false)+"'::regprocedure::bigint")
			q := `SELECT p.oid::bigint,p.proowner::bigint,p.proacl::text,p.proargtypes::text,p.prorettype::bigint,p.proisstrict,p.provolatile,p.proretset,p.proparallel,p.procost,p.prorows,p.prosrc,p.probin FROM pg_proc p WHERE p.oid=` + ownershipNumber(id)
			invariantQueries = append(invariantQueries, q)
			originalDeclarations = append(originalDeclarations, ownershipSnapshot(t, p, c, q))
		}
		ownershipSurvivorMutation(t, p, c, run, "ltree", ownershipLtreeComparisonSignature(start, false))
		run(fixture.Exec("SET search_path=eo_new,public", 0))
		for _, q := range []string{
			"CREATE TABLE eo_old.lc_values(id integer,v eo_new.ltree)",
			"CREATE TABLE eo_old.lc_hash_values(id integer,v eo_new.ltree)",
			"CREATE INDEX lc_btree ON eo_old.lc_values(v eo_new.ltree_ops)",
			"CREATE INDEX lc_hash ON eo_old.lc_hash_values USING hash(v eo_new.hash_ltree_ops)",
			"INSERT INTO eo_old.lc_values VALUES (1,'a-b'),(2,'a.b'),(3,'a'),(4,''),(5,NULL),(6,'a.b'),(7,'c0K'),(8,'qek')",
			"INSERT INTO eo_old.lc_hash_values SELECT * FROM eo_old.lc_values",
			"GRANT SELECT ON eo_old.lc_values,eo_old.lc_hash_values TO eo_survivor_acl",
		} {
			count := int64(0)
			if strings.HasPrefix(q, "INSERT") {
				count = 8
			}
			run(fixture.Exec(q, count))
		}
		for _, q := range []string{
			"CREATE VIEW eo_old.lc_ordered AS SELECT id,v FROM eo_old.lc_values ORDER BY v,id LIMIT 4",
			"CREATE VIEW eo_old.lc_equal AS SELECT id FROM eo_old.lc_values WHERE v OPERATOR(eo_new.=) 'a.b'::eo_new.ltree",
			"CREATE VIEW eo_old.lc_collision AS SELECT id FROM eo_old.lc_hash_values WHERE v OPERATOR(eo_new.=) 'c0K'::eo_new.ltree",
			"CREATE VIEW eo_old.lc_null AS SELECT id FROM eo_old.lc_hash_values WHERE v OPERATOR(eo_new.=) NULL::eo_new.ltree",
			"GRANT SELECT ON eo_old.lc_ordered,eo_old.lc_equal,eo_old.lc_collision,eo_old.lc_null TO eo_survivor_acl",
		} {
			run(fixture.Exec(q, 0))
		}
		indexSteps := []fixture.Step{
			fixture.Select("SELECT id FROM eo_old.lc_values ORDER BY v,id LIMIT 4", []string{"id"}, []string{"INT4"}, [][]any{{int64(4)}, {int64(3)}, {int64(2)}, {int64(6)}}),
			fixture.Select("SELECT id FROM eo_old.lc_values WHERE v='a.b'::eo_new.ltree ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(2)}, {int64(6)}}),
			fixture.Select("SELECT id FROM eo_old.lc_hash_values WHERE v='c0K'::eo_new.ltree ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}),
			fixture.Select("SELECT id FROM eo_old.lc_ordered ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(2)}, {int64(3)}, {int64(4)}, {int64(6)}}),
			fixture.Select("SELECT id FROM eo_old.lc_equal ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(2)}, {int64(6)}}),
			fixture.Select("SELECT id FROM eo_old.lc_collision ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}),
			fixture.Select("SELECT id FROM eo_old.lc_null ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{}),
		}
		var held []fixture.HeldStatement
		var heldSteps []fixture.Step
		for _, s := range indexSteps[3:] {
			held = append(held, crosstabHeld(t, p, c, s.Query))
			heldSteps = append(heldSteps, s)
		}
		for i := start; i < min(start+3, 6); i++ {
			s := ownershipLtreeComparisonStep(i, false, false, 0)
			view := fmt.Sprintf("eo_old.lc_witness_%d_true", i)
			run(fixture.Exec("CREATE VIEW "+view+" AS "+s.Query, 0))
			run(fixture.Exec("GRANT SELECT ON "+view+" TO eo_survivor_acl", 0))
			s.Query = "SELECT * FROM " + view
			held = append(held, crosstabHeld(t, p, c, s.Query))
			heldSteps = append(heldSteps, s)
		}
		values := func(conn *sql.Conn, recorder *fixture.Recorder, renamed bool) {
			r := ownershipRunWithRecorder(t, p, conn, recorder)
			for i := start; i < min(start+3, 6); i++ {
				for null := range 3 {
					for _, prepared := range []bool{false, true} {
						r(ownershipLtreeComparisonStep(i, renamed, prepared, null))
					}
				}
				s := ownershipLtreeComparisonStep(i, renamed, false, 0)
				s.Query = fmt.Sprintf("SELECT * FROM eo_old.lc_witness_%d_true", i)
				r(s)
				r(fixture.Prepared(s))
			}
			// Separate index tables prevent a valid B-tree from masking a broken
			// hash support path. The unequal collision labels require equality recheck.
			for _, s := range indexSteps {
				r(s)
				r(fixture.Prepared(s))
			}
		}
		for _, target := range ownershipLtreeComparisonTargets(false) {
			run(fixture.Exec("ALTER EXTENSION ltree DROP "+target, 0))
		}
		run(fixture.Exec("SET search_path=public", 0))
		ownershipSurvivorNonmember(run, "ltree", ownershipLtreeComparisonSignature(start, false))
		run(fixture.Exec("SET search_path=eo_new,public", 0))
		values(c, b.Recorder, false)
		for i := range ownershipLtreeComparisonCases {
			run(fixture.Exec(fmt.Sprintf("ALTER FUNCTION %s RENAME TO eo_compare_%d", ownershipLtreeComparisonSignature(i, false), i), 0))
		}
		// Distinguish the current operator namespace from its implementing
		// procedure/type/class namespace, including in restored stored consumers.
		for _, op := range []string{"=", "<>", "<", "<=", ">", ">="} {
			run(fixture.Exec("ALTER OPERATOR eo_new."+op+"(eo_new.ltree,eo_new.ltree) SET SCHEMA eo_other", 0))
			run(fixture.Exec("ALTER OPERATOR eo_other."+op+"(eo_new.ltree,eo_new.ltree) OWNER TO eo_survivor_owner", 0))
		}
		run(fixture.Exec("ALTER TYPE eo_new.ltree OWNER TO eo_survivor_owner", 0))
		for _, entry := range []struct{ name, method string }{{"ltree_ops", "btree"}, {"hash_ltree_ops", "hash"}} {
			for _, kind := range []string{"CLASS", "FAMILY"} {
				run(fixture.Exec("ALTER OPERATOR "+kind+" eo_new."+entry.name+" USING "+entry.method+" OWNER TO eo_survivor_owner", 0))
			}
		}
		run(fixture.Exec("SET search_path=public", 0))
		for _, target := range ownershipLtreeComparisonTargets(true) {
			run(fixture.Exec(`ALTER EXTENSION "uuid-ossp" ADD `+target, 0))
		}
		ownershipSurvivorMemberRestriction(run, ownershipLtreeComparisonSignature(start, true), "uuid-ossp")
		run(fixture.Exec("SET search_path=eo_other,eo_new,public", 0))
		values(c, b.Recorder, true)
		// Upstream send/receive codecs remain actual normal prerequisites, not
		// guessed recipe names. Native has no phantom unrepresented helpers.
		rows, err := c.QueryContext(p.Ctx, `SELECT DISTINCT p.oid::regprocedure::text FROM pg_depend d JOIN pg_proc p ON p.oid=d.refobjid JOIN pg_depend em ON em.classid='pg_proc'::regclass AND em.objid=p.oid AND em.refclassid='pg_extension'::regclass AND em.deptype='e' JOIN pg_extension e ON e.oid=em.refobjid WHERE d.classid='pg_type'::regclass AND d.objid='eo_new.ltree'::regtype AND d.refclassid='pg_proc'::regclass AND d.deptype='n' AND e.extname='ltree'`)
		if err != nil {
			t.Fatal(err)
		}
		var helpers []string
		for rows.Next() {
			var signature string
			if err := rows.Scan(&signature); err != nil {
				rows.Close()
				t.Fatal(err)
			}
			helpers = append(helpers, signature)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		if err := rows.Close(); err != nil {
			t.Fatal(err)
		}
		for _, signature := range helpers {
			run(fixture.Exec("ALTER EXTENSION ltree DROP FUNCTION "+signature, 0))
		}
		run(fixture.Exec("DROP EXTENSION ltree", 0))
		if !foreign {
			for _, target := range ownershipLtreeComparisonTargets(true) {
				run(fixture.Exec(`ALTER EXTENSION "uuid-ossp" DROP `+target, 0))
			}
		}
		for i, h := range held {
			fixture.RunStep(t, p.Ctx, h, b.Recorder, heldSteps[i])
			if err := h.Statement.Close(); err != nil {
				t.Fatal(err)
			}
		}
		for i, q := range invariantQueries {
			ownershipUnchanged(t, p, c, q, originalDeclarations[i])
		}
		var signatures []string
		for i := range ownershipLtreeComparisonCases {
			signatures = append(signatures, ownershipLtreeComparisonSignature(i, true))
		}
		signatures = append(signatures, "eo_new.ltree_in(cstring)", "eo_new.ltree_out(eo_new.ltree)")
		check := func(conn *sql.Conn, recorder *fixture.Recorder) {
			r := ownershipRunWithRecorder(t, p, conn, recorder)
			r(fixture.Exec("SET search_path=eo_other,eo_new,public", 0))
			values(conn, recorder, true)
			r(fixture.Exec("SET ROLE eo_survivor_acl", 0))
			values(conn, recorder, true)
			r(fixture.Exec("RESET ROLE", 0))
			r(ownershipCount("SELECT count(*) FROM pg_extension WHERE extname='ltree'", 0))
			dependencies := &ownershipLtreeDependencies{}
			for i := start; i < start+3; i++ {
				signature := ownershipLtreeComparisonSignature(i, true)
				dependencies.links(r, signature, foreign)
				if i < 6 {
					dependencies.count("classid='pg_rewrite'::regclass AND objid IN (SELECT oid FROM pg_rewrite WHERE ev_class='eo_old.lc_witness_"+fmt.Sprint(i)+"_true'::regclass) AND refclassid='pg_operator'::regclass AND refobjid='eo_other."+[]string{"=", "<>", "<", "<=", ">", ">="}[i]+"(eo_new.ltree,eo_new.ltree)'::regoperator AND deptype='n'", 1)
				}
				r(fixture.Select("SELECT proisstrict,provolatile,proretset,prosrc,probin FROM pg_proc WHERE oid='"+signature+"'::regprocedure", []string{"proisstrict", "provolatile", "proretset", "prosrc", "probin"}, []string{"BOOL", "CHAR", "BOOL", "TEXT", "TEXT"}, [][]any{{true, "i", false, ownershipLtreeComparisonCases[i].name, "$libdir/ltree"}}))
			}
			ownershipLtreeComparisonGraph(r, dependencies, foreign)
			dependencies.check(r)
		}
		var retainedRows []*sql.Rows
		if !b.PG {
			// Capture one real source resource per destination before either reset.
			for range 2 {
				oldRows, err := c.QueryContext(p.Ctx, "SELECT id,v FROM eo_old.lc_values ORDER BY v,id LIMIT 4")
				if err != nil {
					t.Fatal(err)
				}
				defer oldRows.Close()
				retainedRows = append(retainedRows, oldRows)
			}
		}
		ownershipSurvivorControls(t, p, b, c, "ltree_comparison_"+state, signatures, check)
		for _, oldRows := range retainedRows {
			columns, err := oldRows.ColumnTypes()
			if err != nil || len(columns) != 2 || columns[0].Name() != "id" || columns[0].DatabaseTypeName() != "INT4" || columns[1].Name() != "v" || columns[1].DatabaseTypeName() != "LTREE" {
				t.Fatal("retained ordering metadata", err)
			}
			for _, want := range []struct {
				id    int64
				value string
			}{{4, ""}, {3, "a"}, {2, "a.b"}, {6, "a.b"}} {
				if !oldRows.Next() {
					t.Fatal("missing retained ordering row", oldRows.Err())
				}
				var got int64
				var value string
				if err := oldRows.Scan(&got, &value); err != nil || got != want.id || value != want.value {
					t.Fatal("retained ordering row", got, value, err)
				}
			}
			if oldRows.Next() || oldRows.Err() != nil {
				t.Fatal("retained ordering EOF", oldRows.Err())
			}
			if err := oldRows.Close(); err != nil {
				t.Fatal(err)
			}
		}
	})
}

func ownershipLtreeComparisonGraph(run func(fixture.Step), dependencies *ownershipLtreeDependencies, foreign bool) {
	member := ""
	if foreign {
		member = "uuid-ossp"
	}
	for index, identity := range []string{"'eo_new.ltree'::regtype", "(SELECT typarray FROM pg_type WHERE oid='eo_new.ltree'::regtype)"} {
		dependencies.member(run, "pg_type", identity, member)
		run(ownershipCount("SELECT count(*) FROM pg_type WHERE oid="+identity+" AND typowner=(SELECT oid FROM pg_roles WHERE rolname='eo_survivor_owner')", 1))
		// An associated array is internally owned by its scalar; it has no
		// independent namespace dependency (REL_18_6 GenerateTypeDependencies).
		dependencies.count("classid='pg_type'::regclass AND objid="+identity+" AND refclassid='pg_namespace'::regclass AND refobjid='eo_new'::regnamespace AND deptype='n'", int64(1-index))
		dependencies.count("classid='pg_type'::regclass AND objid="+identity+" AND refclassid='pg_type'::regclass AND refobjid='eo_new.ltree'::regtype AND deptype='i'", int64(index))
	}
	for i, op := range []string{"=", "<>", "<", "<=", ">", ">="} {
		identity := "'eo_other." + op + "(eo_new.ltree,eo_new.ltree)'::regoperator"
		run(ownershipCount("SELECT count(*) FROM pg_operator WHERE oid="+identity+" AND oprcode='"+ownershipLtreeComparisonSignature(i, true)+"'::regprocedure AND oprowner=(SELECT oid FROM pg_roles WHERE rolname='eo_survivor_owner')", 1))
		dependencies.count("classid='pg_operator'::regclass AND objid="+identity+" AND refclassid='pg_proc'::regclass AND refobjid='"+ownershipLtreeComparisonSignature(i, true)+"'::regprocedure AND deptype='n'", 1)
		dependencies.member(run, "pg_operator", identity, member)
		dependencies.count("classid='pg_operator'::regclass AND objid="+identity+" AND refclassid='pg_namespace'::regclass AND refobjid='eo_other'::regnamespace AND deptype='n'", 1)
	}
	for _, tc := range []struct {
		name, method         string
		strategies, supports int64
	}{{"ltree_ops", "btree", 5, 1}, {"hash_ltree_ops", "hash", 1, 2}} {
		family := "(SELECT f.oid FROM pg_opfamily f JOIN pg_am a ON a.oid=f.opfmethod WHERE f.opfname='" + tc.name + "' AND f.opfnamespace='eo_new'::regnamespace AND a.amname='" + tc.method + "')"
		class := "(SELECT c.oid FROM pg_opclass c JOIN pg_am a ON a.oid=c.opcmethod WHERE c.opcname='" + tc.name + "' AND c.opcnamespace='eo_new'::regnamespace AND a.amname='" + tc.method + "')"
		run(ownershipCount("SELECT count(*) FROM pg_opclass WHERE oid="+class+" AND opcfamily="+family+" AND opcintype='eo_new.ltree'::regtype AND opcdefault", 1))
		run(ownershipCount("SELECT count(*) FROM pg_amop WHERE amopfamily="+family, tc.strategies))
		run(ownershipCount("SELECT count(*) FROM pg_amproc WHERE amprocfamily="+family, tc.supports))
		run(ownershipCount("SELECT count(*) FROM pg_opclass WHERE oid="+class+" AND opcowner=(SELECT oid FROM pg_roles WHERE rolname='eo_survivor_owner')", 1))
		run(ownershipCount("SELECT count(*) FROM pg_opfamily WHERE oid="+family+" AND opfowner=(SELECT oid FROM pg_roles WHERE rolname='eo_survivor_owner')", 1))
		strategies := []string{"<", "<=", "=", ">=", ">"}
		if tc.method == "hash" {
			strategies = []string{"="}
		}
		for index, op := range strategies {
			identity := "(SELECT oid FROM pg_amop WHERE amopfamily=" + family + " AND amopstrategy=" + fmt.Sprint(index+1) + ")"
			run(ownershipCount("SELECT count(*) FROM pg_amop WHERE oid="+identity+" AND amopopr='eo_other."+op+"(eo_new.ltree,eo_new.ltree)'::regoperator AND amoplefttype='eo_new.ltree'::regtype AND amoprighttype='eo_new.ltree'::regtype AND amoppurpose='s'", 1))
			dependencies.count("classid='pg_amop'::regclass AND objid="+identity+" AND refclassid='pg_opclass'::regclass AND refobjid="+class+" AND deptype='i'", 1)
		}
		for _, entry := range []struct{ catalog, id string }{{"pg_opclass", class}, {"pg_opfamily", family}} {
			dependencies.member(run, entry.catalog, entry.id, member)
			dependencies.count("classid='"+entry.catalog+"'::regclass AND objid="+entry.id+" AND refclassid='pg_namespace'::regclass AND refobjid='eo_new'::regnamespace AND deptype='n'", 1)
		}
		first := 6
		if tc.method == "hash" {
			first = 7
		}
		for number := 1; number <= int(tc.supports); number++ {
			identity := "(SELECT oid FROM pg_amproc WHERE amprocfamily=" + family + " AND amprocnum=" + fmt.Sprint(number) + ")"
			dependency := "n"
			if number == 2 {
				dependency = "a"
			}
			run(ownershipCount("SELECT count(*) FROM pg_amproc WHERE oid="+identity+" AND amproc='"+ownershipLtreeComparisonSignature(first+number-1, true)+"'::regprocedure", 1))
			dependencies.count("classid='pg_amproc'::regclass AND objid="+identity+" AND refclassid='pg_proc'::regclass AND refobjid='"+ownershipLtreeComparisonSignature(first+number-1, true)+"'::regprocedure AND deptype='"+dependency+"'", 1)
			if number == 1 {
				dependencies.count("classid='pg_amproc'::regclass AND objid="+identity+" AND refclassid='pg_opclass'::regclass AND refobjid="+class+" AND deptype='i'", 1)
			} else {
				dependencies.count("classid='pg_amproc'::regclass AND objid="+identity+" AND refclassid='pg_opfamily'::regclass AND refobjid="+family+" AND deptype='a'", 1)
				dependencies.count("classid='pg_amproc'::regclass AND objid="+identity+" AND refclassid='pg_opclass'::regclass", 0)
			}
		}
	}
}
