package reference_test

import (
	"database/sql"
	"fmt"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

// These are the unchanged positive/miss inputs and independent outcomes from
// extension_types/ltree_arrays_helpers_test.go. This package exercises the same
// released corpus through membership mutation and destination-owned controls.
type ownershipLtreeArrayCase struct {
	name, op, leftType, rightType, left, right, missLeft, missRight string
	want                                                            any
}

var ownershipLtreeArrayCases = []ownershipLtreeArrayCase{
	{"_ltree_isparent", "@>", "ltree[]", "ltree", "{Other,Top.Science}", "Top.Science.Astro", "{Other}", "Top.Science.Astro", true},
	{"_ltree_r_isparent", "<@", "ltree", "ltree[]", "Top.Science.Astro", "{Other,Top.Science}", "Top.Science.Astro", "{Other}", true},
	{"_ltree_risparent", "<@", "ltree[]", "ltree", "{Other,Top.Science.Astro}", "Top.Science", "{Other}", "Top.Science", true},
	{"_ltree_r_risparent", "@>", "ltree", "ltree[]", "Top.Science", "{Other,Top.Science.Astro}", "Top.Science", "{Other}", true},
	{"_ltq_regex", "~", "ltree[]", "lquery", "{a.b}", "*.b", "{a.b}", "a", true},
	{"_ltq_rregex", "~", "lquery", "ltree[]", "*.b", "{a.b}", "a", "{a.b}", true},
	{"lt_q_regex", "?", "ltree", "lquery[]", "Top.Astro", "{Other,Top.*}", "Top.Astro", "{Other}", true},
	{"lt_q_rregex", "?", "lquery[]", "ltree", "{Other,Top.*}", "Top.Astro", "{Other}", "Top.Astro", true},
	{"_lt_q_regex", "?", "ltree[]", "lquery[]", "{Other,Top.Astro}", "{Nope,*.Astro}", "{Other}", "{Nope}", true},
	{"_lt_q_rregex", "?", "lquery[]", "ltree[]", "{Nope,*.Astro}", "{Other,Top.Astro}", "{Nope}", "{Other}", true},
	{"_ltxtq_exec", "@", "ltree[]", "ltxtquery", "{Europe.Top}", "Top & Europe", "{Top,Europe}", "Top & Europe", true},
	{"_ltxtq_rexec", "@", "ltxtquery", "ltree[]", "Top & Europe", "{Europe.Top}", "Top & Europe", "{Top,Europe}", true},
	{"_ltree_extract_isparent", "?@>", "ltree[]", "ltree", "[7:8]={Top.Science,Top}", "Top.Science.Astro", "{}", "Top", "Top.Science"},
	{"_ltree_extract_risparent", "?<@", "ltree[]", "ltree", "[0:1]={Top.Z,Top.A}", "Top", "{}", "Top", "Top.Z"},
	{"_ltq_extract_regex", "?~", "ltree[]", "lquery", "[7:9]={Other,Top.Z,Top.A}", "Top.*", "{Other}", "Top.*", "Top.Z"},
	{"_ltxtq_extract_exec", "?@", "ltree[]", "ltxtquery", "[7:9]={Other,Top.Z,Top.A}", "Top", "{Other}", "Top", "Top.Z"},
}

func ownershipLtreeSignature(index int, renamed bool) string {
	tc := ownershipLtreeArrayCases[index]
	name := tc.name
	if renamed {
		name = fmt.Sprintf("eo_array_%d", index)
	}
	return "eo_new." + name + "(eo_new." + tc.leftType + ",eo_new." + tc.rightType + ")"
}
func ownershipLtreeOperator(index int) string {
	tc := ownershipLtreeArrayCases[index]
	return "eo_new." + tc.op + "(eo_new." + tc.leftType + ",eo_new." + tc.rightType + ")"
}
func ownershipLtreeTargets(renamed bool) []string {
	var targets []string
	for _, tc := range []struct{ typ, input, output string }{{"lquery", "lquery_in", "lquery_out"}, {"ltree", "ltree_in", "ltree_out"}, {"ltxtquery", "ltxtq_in", "ltxtq_out"}} {
		targets = append(targets, "TYPE eo_new."+tc.typ, "FUNCTION eo_new."+tc.input+"(cstring)", "FUNCTION eo_new."+tc.output+"(eo_new."+tc.typ+")")
	}
	for i := range ownershipLtreeArrayCases {
		targets = append(targets, "FUNCTION "+ownershipLtreeSignature(i, renamed), "OPERATOR "+ownershipLtreeOperator(i))
	}
	return targets
}
func ownershipLtreeStep(b fixture.Backend, index int, renamed, direct bool, kind int, prepared bool) fixture.Step {
	tc := ownershipLtreeArrayCases[index]
	left, right, want := tc.left, tc.right, tc.want
	if kind == 1 {
		left, right = tc.missLeft, tc.missRight
		want = false
		if index >= 12 {
			want = nil
		}
	}
	args := []any{left, right}
	l, r := "$1::eo_new."+tc.leftType, "$2::eo_new."+tc.rightType
	if !prepared {
		l = "'" + strings.ReplaceAll(left, "'", "''") + "'::eo_new." + tc.leftType
		r = "'" + strings.ReplaceAll(right, "'", "''") + "'::eo_new." + tc.rightType
		args = nil
	}
	if kind == 2 {
		l = "NULL::eo_new." + tc.leftType
		want = nil
		if prepared {
			r = "$1::eo_new." + tc.rightType
			args = []any{right}
		}
	}
	if kind == 3 {
		r = "NULL::eo_new." + tc.rightType
		want = nil
		if prepared {
			l = "$1::eo_new." + tc.leftType
			args = []any{left}
		}
	}
	expression := l + " OPERATOR(eo_new." + tc.op + ") " + r
	if direct {
		name := tc.name
		if renamed {
			name = fmt.Sprintf("eo_array_%d", index)
		}
		expression = "eo_new." + name + "(" + l + "," + r + ")"
	}
	family := "BOOL"
	if index >= 12 {
		family = "LTREE"
		if b.PG {
			family = ""
		}
	}
	s := fixture.Select("SELECT "+expression+" AS v", []string{"v"}, []string{family}, [][]any{{want}}, args...)
	if index >= 12 {
		s.CheckRaw = func(t *testing.T, _, _ int, value any) bool {
			if value == nil {
				return true
			}
			if b.PG {
				if _, ok := value.([]byte); !ok {
					t.Fatalf("pq LTREE raw %T", value)
				}
			} else if _, ok := value.(string); !ok {
				t.Fatalf("native LTREE raw %T", value)
			}
			return true
		}
	}
	if prepared {
		return fixture.Prepared(s)
	}
	return s
}

// Every bounded inventory owns one complete real typed/helper/operator source
// graph shared by both native destinations. Per-operation value, declaration and
// dependency assertions retain the same disjoint corpus partition.
func TestReferenceExtensionOwnershipLtreeArraySurvivors(t *testing.T) {
	for _, foreign := range []bool{false, true} {
		state := "detached"
		if foreign {
			state = "foreign"
		}
		t.Run(state, func(t *testing.T) {
			for start := 0; start < len(ownershipLtreeArrayCases); start += 2 {
				t.Run(fmt.Sprintf("inventory_%02d", start), func(t *testing.T) {
					fixture.RoleReferenceCleanup(t, []string{"eo_survivor_owner", "eo_survivor_acl"})
					ownershipScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
						run := ownershipRun(t, p, b, c)
						ownershipSurvivorRoles(run)
						run(fixture.Exec("CREATE EXTENSION ltree SCHEMA eo_old", 0))
						run(fixture.Exec("ALTER EXTENSION ltree SET SCHEMA eo_new", 0))
						var signatures []string
						invariant := func(id int64) string {
							return `SELECT p.oid::bigint,p.proowner::bigint,p.proacl::text,p.proargtypes::text,p.prorettype::bigint,p.proisstrict,p.provolatile,p.proretset,p.proparallel,p.procost,p.prorows,p.prosrc,p.probin FROM pg_proc p WHERE p.oid=` + ownershipNumber(id)
						}
						ids := make([]int64, len(ownershipLtreeArrayCases))
						before := make([][][]any, len(ids))
						for i := start; i < min(start+2, len(ownershipLtreeArrayCases)); i++ {
							sig := ownershipLtreeSignature(i, false)
							ownershipSurvivorACL(run, sig)
							ids[i] = ownershipID(t, p, c, "SELECT '"+sig+"'::regprocedure::bigint")
							before[i] = ownershipSnapshot(t, p, c, invariant(ids[i]))
						}
						// Mutation diagnostics and rollback retain their exact assertions;
						// every inventory also witnesses normal view-to-routine dependencies.
						ownershipSurvivorMutation(t, p, c, run, "ltree", ownershipLtreeSignature(start, false))
						var held []fixture.HeldStatement
						var heldSteps []fixture.Step
						for i := start; i < min(start+2, len(ownershipLtreeArrayCases)); i++ {
							for _, direct := range []bool{false, true} {
								s := ownershipLtreeStep(b, i, false, direct, 0, false)
								view := fmt.Sprintf("eo_old.la_witness_%d_%t", i, direct)
								run(fixture.Exec("CREATE VIEW "+view+" AS "+s.Query, 0))
								run(fixture.Exec("GRANT SELECT ON "+view+" TO eo_survivor_acl", 0))
								s.Query = "SELECT * FROM " + view
								held = append(held, crosstabHeld(t, p, c, s.Query))
								heldSteps = append(heldSteps, s)
							}
						}
						// A genuine SQL function occurrence beneath the operator must
						// survive traversal without promoting its implementation backer.
						operand := fmt.Sprintf("eo_old.la_operand_%d", start)
						operandType := "eo_new." + ownershipLtreeArrayCases[start].leftType
						run(fixture.Exec("CREATE FUNCTION "+operand+"(v "+operandType+") RETURNS "+operandType+" LANGUAGE SQL IMMUTABLE AS 'SELECT $1'", 0))
						nested := ownershipLtreeStep(b, start, false, false, 0, false)
						nested.Query = strings.Replace(nested.Query, "SELECT ", "SELECT "+operand+"(", 1)
						nested.Query = strings.Replace(nested.Query, " OPERATOR(", ") OPERATOR(", 1)
						nestedView := fmt.Sprintf("eo_old.la_nested_%d", start)
						run(fixture.Exec("CREATE VIEW "+nestedView+" AS "+nested.Query, 0))
						run(fixture.Exec("GRANT SELECT ON "+nestedView+" TO eo_survivor_acl", 0))
						nested.Query = "SELECT * FROM " + nestedView
						held = append(held, crosstabHeld(t, p, c, nested.Query))
						heldSteps = append(heldSteps, nested)
						for _, target := range ownershipLtreeTargets(false) {
							run(fixture.Exec("ALTER EXTENSION ltree DROP "+target, 0))
						}
						ownershipSurvivorNonmember(run, "ltree", ownershipLtreeSignature(start, false))
						values := func(conn *sql.Conn, recorder *fixture.Recorder, renamed bool) {
							r := ownershipRunWithRecorder(t, p, conn, recorder)
							for i := start; i < min(start+2, len(ownershipLtreeArrayCases)); i++ {
								for _, direct := range []bool{false, true} {
									for kind := range 4 {
										for _, prepared := range []bool{false, true} {
											r(ownershipLtreeStep(b, i, renamed, direct, kind, prepared))
										}
									}
								}
								for _, direct := range []bool{false, true} {
									s := ownershipLtreeStep(b, i, renamed, direct, 0, false)
									s.Query = fmt.Sprintf("SELECT * FROM eo_old.la_witness_%d_%t", i, direct)
									r(s)
									r(fixture.Prepared(s))
								}
							}
							r(nested)
							r(fixture.Prepared(nested))
						}
						values(c, b.Recorder, false)
						for i := range ownershipLtreeArrayCases {
							run(fixture.Exec(fmt.Sprintf("ALTER FUNCTION %s RENAME TO eo_array_%d", ownershipLtreeSignature(i, false), i), 0))
							signatures = append(signatures, ownershipLtreeSignature(i, true))
						}
						for _, target := range ownershipLtreeTargets(true) {
							run(fixture.Exec(`ALTER EXTENSION "uuid-ossp" ADD `+target, 0))
						}
						ownershipSurvivorMemberRestriction(run, signatures[start], "uuid-ossp")
						values(c, b.Recorder, true)
						// The released module also gives the retained operators its
						// selectivity routine and the retained types binary codecs.
						// Preserve their actual normal dependencies before retiring
						// the origin; native has no phantom unrepresented helpers.
						var operators []string
						for i := range ownershipLtreeArrayCases {
							operators = append(operators, "'"+ownershipLtreeOperator(i)+"'::regoperator")
						}
						query := `SELECT DISTINCT p.oid::regprocedure::text FROM pg_depend d JOIN pg_proc p ON p.oid=d.refobjid JOIN pg_depend em ON em.classid='pg_proc'::regclass AND em.objid=p.oid AND em.refclassid='pg_extension'::regclass AND em.deptype='e' JOIN pg_extension e ON e.oid=em.refobjid WHERE d.refclassid='pg_proc'::regclass AND d.deptype='n' AND e.extname='ltree' AND ((d.classid='pg_type'::regclass AND d.objid IN ('eo_new.ltree'::regtype,'eo_new.lquery'::regtype,'eo_new.ltxtquery'::regtype)) OR (d.classid='pg_operator'::regclass AND d.objid IN (` + strings.Join(operators, ",") + `)))`
						rows, err := c.QueryContext(p.Ctx, query)
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
							for _, target := range ownershipLtreeTargets(true) {
								run(fixture.Exec(`ALTER EXTENSION "uuid-ossp" DROP `+target, 0))
							}
						}
						for i, h := range held {
							fixture.RunStep(t, p.Ctx, h, b.Recorder, heldSteps[i])
							if err := h.Statement.Close(); err != nil {
								t.Fatal(err)
							}
						}
						for i := start; i < min(start+2, len(ownershipLtreeArrayCases)); i++ {
							ownershipUnchanged(t, p, c, invariant(ids[i]), before[i])
						}
						var allDeclarations []string
						allDeclarations = append(allDeclarations, operand+"("+operandType+")")
						var logicalQueries []string
						// The six scalar codecs stay common; per-operation declarations
						// and dependency assertions follow the disjoint value inventory.
						for _, target := range ownershipLtreeTargets(true)[:9] {
							if strings.HasPrefix(target, "FUNCTION ") {
								allDeclarations = append(allDeclarations, strings.TrimPrefix(target, "FUNCTION "))
							}
						}
						for i := start; i < min(start+2, len(ownershipLtreeArrayCases)); i++ {
							allDeclarations = append(allDeclarations, signatures[i])
							logicalQueries = append(logicalQueries, `SELECT n.nspname,o.oprname,r.rolname,o.oprleft::regtype::text,o.oprright::regtype::text,o.oprresult::regtype::text,o.oprcode::regprocedure::text,o.oprcom::regoperator::text,o.oprnegate::regoperator::text,o.oprrest::regproc::text,o.oprjoin::regproc::text,o.oprcanhash,o.oprcanmerge FROM pg_operator o JOIN pg_namespace n ON n.oid=o.oprnamespace JOIN pg_roles r ON r.oid=o.oprowner WHERE o.oid='`+ownershipLtreeOperator(i)+`'::regoperator`)
						}
						for _, typ := range []string{"ltree", "lquery", "ltxtquery"} {
							logicalQueries = append(logicalQueries, `SELECT n.nspname,t.typname,r.rolname,t.typacl::text,t.typinput::regprocedure::text,t.typoutput::regprocedure::text,a.typname,a.typelem::regtype::text,a.typinput::regprocedure::text,a.typoutput::regprocedure::text,t.typstorage FROM pg_type t JOIN pg_type a ON a.oid=t.typarray JOIN pg_namespace n ON n.oid=t.typnamespace JOIN pg_roles r ON r.oid=t.typowner WHERE t.oid='eo_new.`+typ+`'::regtype`)
						}
						logical := make([][][]any, len(logicalQueries))
						for i, q := range logicalQueries {
							logical[i] = ownershipSnapshot(t, p, c, q)
							if len(logical[i]) != 1 {
								t.Fatal("missing current nominal/operator declaration")
							}
						}
						check := func(conn *sql.Conn, recorder *fixture.Recorder) {
							r := ownershipRunWithRecorder(t, p, conn, recorder)
							values(conn, recorder, true)
							r(fixture.Exec("SET ROLE eo_survivor_acl", 0))
							values(conn, recorder, true)
							r(fixture.Exec("RESET ROLE", 0))
							r(ownershipCount(`SELECT count(*) FROM pg_extension WHERE extname='ltree'`, 0))
							for i, q := range logicalQueries {
								ownershipUnchanged(t, p, conn, q, logical[i])
							}
							member := ""
							if foreign {
								member = "uuid-ossp"
							}
							dependencies := &ownershipLtreeDependencies{}
							for i := start; i < min(start+2, len(ownershipLtreeArrayCases)); i++ {
								sig := signatures[i]
								dependencies.links(r, sig, foreign)
								tc := ownershipLtreeArrayCases[i]
								r(fixture.Select(`SELECT p.proisstrict,p.provolatile,p.proretset,p.prosrc,p.probin FROM pg_proc p WHERE p.oid='`+sig+`'::regprocedure`, []string{"proisstrict", "provolatile", "proretset", "prosrc", "probin"}, []string{"BOOL", "CHAR", "BOOL", "TEXT", "TEXT"}, [][]any{{true, "i", false, tc.name, "$libdir/ltree"}}))
								op := ownershipLtreeOperator(i)
								r(ownershipCount(`SELECT count(*) FROM pg_operator o WHERE o.oid='`+op+`'::regoperator AND o.oprcode='`+sig+`'::regprocedure`, 1))
								dependencies.count(`classid='pg_operator'::regclass AND objid='`+op+`'::regoperator AND refclassid='pg_proc'::regclass AND refobjid='`+sig+`'::regprocedure AND deptype='n'`, 1)
								dependencies.member(r, "pg_operator", "'"+op+"'::regoperator", member)
								dependencies.count(`classid='pg_operator'::regclass AND objid='`+op+`'::regoperator AND refclassid='pg_namespace'::regclass AND refobjid='eo_new'::regnamespace AND deptype='n'`, 1)
								ownershipLtreeCallDependencies(dependencies, fmt.Sprintf("eo_old.la_witness_%d_true", i), fmt.Sprintf("eo_old.la_witness_%d_false", i), sig, op)
							}
							ownershipLtreeNestedDependencies(dependencies, nestedView, operand+"("+operandType+")", signatures[start], ownershipLtreeOperator(start))
							for _, typ := range []string{"ltree", "lquery", "ltxtquery"} {
								dependencies.member(r, "pg_type", "'eo_new."+typ+"'::regtype", member)
								r(ownershipCount(`SELECT count(*) FROM pg_type t JOIN pg_type a ON a.oid=t.typarray WHERE t.oid='eo_new.`+typ+`'::regtype AND a.typelem=t.oid`, 1))
								dependencies.count(`classid='pg_type'::regclass AND objid='eo_new.`+typ+`'::regtype AND refclassid='pg_namespace'::regclass AND refobjid='eo_new'::regnamespace AND deptype='n'`, 1)
								dependencies.member(r, "pg_type", "(SELECT typarray FROM pg_type WHERE oid='eo_new."+typ+"'::regtype)", member)
							}
							dependencies.check(r)
						}
						// Keep the actual result rows open across same/independent reset and
						// assert their original immutable metadata, values and EOF afterwards.
						var retainedRows []*sql.Rows
						if !b.PG {
							// Capture one real source resource per destination before either reset.
							for range 2 {
								oldRows, err := c.QueryContext(p.Ctx, fmt.Sprintf("SELECT * FROM eo_old.la_witness_%d_true", start))
								if err != nil {
									t.Fatal(err)
								}
								defer oldRows.Close()
								retainedRows = append(retainedRows, oldRows)
							}
						}
						ownershipSurvivorControls(t, p, b, c, "ltree_array_"+state, allDeclarations, check)
						for _, oldRows := range retainedRows {
							columns, err := oldRows.ColumnTypes()
							if err != nil || len(columns) != 1 {
								t.Fatal("retained metadata", err)
							}
							family := "BOOL"
							if start >= 12 {
								family = "LTREE"
							}
							if columns[0].DatabaseTypeName() != family {
								t.Fatal("retained nominal result metadata")
							}
							if !oldRows.Next() {
								t.Fatal("missing retained value", oldRows.Err())
							}
							var value any
							if err := oldRows.Scan(&value); err != nil {
								t.Fatal(err)
							}
							if _, ok := fixture.CompareRows([][]any{{fixture.Normalize(value)}}, [][]any{{ownershipLtreeArrayCases[start].want}}, false); !ok {
								t.Fatal("retained value changed", value)
							}
							if oldRows.Next() || oldRows.Err() != nil {
								t.Fatal("retained EOF", oldRows.Err())
							}
							if err := oldRows.Close(); err != nil {
								t.Fatal(err)
							}
						}
					})
				})
			}
		})
	}
}
