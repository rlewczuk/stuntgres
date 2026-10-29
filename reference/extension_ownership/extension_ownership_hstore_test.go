package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

// The complete real nominal/helper graph survives retirement. Catalog-derived
// addresses select DDL targets only: expected values are the existing hstore
// constructor/record/rows/update corpora, never values learned from native SQL.
func ownershipHstoreSurvivors(t *testing.T) {
	corpus := ownershipHstoreSurvivorCorpus()
	for _, foreign := range []bool{false, true} {
		name := "detached"
		if foreign {
			name = "foreign"
		}
		t.Run(name, func(t *testing.T) {
			// Each complete nominal mutation has a bounded value inventory
			// shared by both destinations under the original fixture deadline.
			for start := 3; start < len(corpus); start += 14 {
				t.Run("inventory_"+ownershipNumber(int64(start)), func(t *testing.T) {
					fixture.RoleReferenceCleanup(t, []string{"eo_survivor_owner", "eo_survivor_acl"})
					ownershipScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
						run := ownershipRun(t, p, b, c)
						ownershipSurvivorRoles(run)
						run(fixture.Exec(`CREATE EXTENSION hstore SCHEMA eo_old`, 0))
						run(fixture.Exec(`ALTER EXTENSION hstore SET SCHEMA eo_new`, 0))
						run(fixture.Exec(`CREATE TYPE eo_old.hs_record_pair AS (i integer,n text)`, 0))
						// Detach actual public catalog objects, including input/output routines,
						// operators, casts and index support. Arrays detach recursively with type.
						targets := ownershipHstoreTargets(t, p, c)
						signatures := []string{"eo_new.hstore(text,text)", "eo_new.hstore(record)", "eo_new.populate_record(anyelement,eo_new.hstore)", "eo_new.each(eo_new.hstore)", "eo_new.hs_concat(eo_new.hstore,eo_new.hstore)", "eo_new.hstore_eq(eo_new.hstore,eo_new.hstore)"}
						signatures = append(signatures, "eo_new.skeys(eo_new.hstore)", "eo_new.svals(eo_new.hstore)", "eo_new.delete(eo_new.hstore,text)", "eo_new.delete(eo_new.hstore,text[])", "eo_new.delete(eo_new.hstore,eo_new.hstore)", "eo_new.slice(eo_new.hstore,text[])")
						ids := make([]int64, len(signatures))
						before := make([][][]any, len(signatures))
						invariant := func(id int64) string {
							return `SELECT p.oid::bigint,p.proowner::bigint,p.proacl::text,p.proargtypes::text,p.prorettype::bigint,p.proisstrict,p.provolatile,p.proretset,p.proparallel,p.procost,p.prorows,p.prosrc,p.probin FROM pg_proc p WHERE p.oid=` + ownershipNumber(id)
						}
						for i, sig := range signatures {
							ownershipSurvivorACL(run, sig)
							ids[i] = ownershipID(t, p, c, "SELECT '"+sig+"'::regprocedure::bigint")
							before[i] = ownershipSnapshot(t, p, c, invariant(ids[i]))
						}
						for _, sig := range signatures {
							ownershipSurvivorMutation(t, p, c, run, "hstore", sig)
						}
						// Keep the constructor/record dependency witnesses in every
						// inventory, and partition all remaining cases without omission.
						end := min(len(corpus), start+14)
						inventory := append([]fixture.Step(nil), corpus[:3]...)
						steps := append(inventory, corpus[start:end]...)
						for i, s := range steps {
							view := "eo_old.hs_witness_" + ownershipNumber(int64(i))
							run(fixture.Exec("CREATE VIEW "+view+" AS "+s.Query, 0))
							run(fixture.Exec("GRANT SELECT ON "+view+" TO eo_survivor_acl", 0))
						}
						held := make([]fixture.HeldStatement, len(steps))
						heldSteps := append([]fixture.Step(nil), steps...)
						for i := range steps {
							// Keep the SQL name stable while the stored consumer retains
							// its real function/operator identities through helper renames.
							heldSteps[i].Query = "SELECT * FROM eo_old.hs_witness_" + ownershipNumber(int64(i))
							heldSteps[i].Bag = true
							held[i] = fixture.CrosstabHeld(t, p, c, heldSteps[i].Query)
						}
						values := func(conn *sql.Conn, recorder *fixture.Recorder) {
							r := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, recorder, s) }
							for i, s := range steps {
								r(s)
								r(fixture.Prepared(s))
								s.Query = "SELECT * FROM eo_old.hs_witness_" + ownershipNumber(int64(i))
								// SRF row order is not retained by a view; compare its full bag.
								s.Bag = true
								r(s)
								r(fixture.Prepared(s))
							}
						}
						for _, target := range targets {
							run(fixture.Exec("ALTER EXTENSION hstore DROP "+target, 0))
						}
						ownershipSurvivorNonmember(run, "hstore", signatures[1])
						ownershipSurvivorNormalRestriction(run, signatures[1], "eo_old.hs_witness_2")
						run(ownershipCount(`SELECT count(*) FROM pg_depend WHERE refclassid='pg_extension'::regclass AND refobjid=(SELECT oid FROM pg_extension WHERE extname='hstore') AND deptype='e'`, 0))
						values(c, b.Recorder)
						for _, rename := range []struct{ old, next string }{
							{"hs_concat", "eo_concat"}, {"skeys", "eo_skeys"}, {"svals", "eo_svals"}, {"each", "eo_each"}, {"delete", "eo_delete"}, {"slice", "eo_slice"},
						} {
							prefix := "eo_new." + rename.old + "("
							// Rename every selected overload; retained operator links still target
							// the original current function identity.
							for i, sig := range signatures {
								if !strings.HasPrefix(sig, prefix) {
									continue
								}
								run(fixture.Exec("ALTER FUNCTION "+sig+" RENAME TO "+rename.next, 0))
								signatures[i] = strings.Replace(sig, prefix, "eo_new."+rename.next+"(", 1)
							}
							for i := range steps {
								steps[i].Query = strings.ReplaceAll(steps[i].Query, prefix, "eo_new."+rename.next+"(")
							}
							for i, target := range targets {
								targets[i] = strings.Replace(target, prefix, "eo_new."+rename.next+"(", 1)
							}
						}
						for _, target := range targets {
							run(fixture.Exec(`ALTER EXTENSION "uuid-ossp" ADD `+target, 0))
						}
						for _, sig := range signatures {
							ownershipSurvivorMemberRestriction(run, sig, "uuid-ossp")
						}
						for _, sig := range signatures {
							for _, ext := range []string{"hstore", `"uuid-ossp"`} {
								run(ownershipError("ALTER EXTENSION "+ext+" ADD FUNCTION "+sig, "55000", "function "+sig+` is already a member of extension "uuid-ossp"`))
							}
							ownershipSurvivorNonmember(run, "hstore", sig)
						}
						values(c, b.Recorder)
						run(fixture.Exec(`DROP EXTENSION hstore`, 0))
						if !foreign {
							for _, target := range targets {
								run(fixture.Exec(`ALTER EXTENSION "uuid-ossp" DROP `+target, 0))
							}
						}
						for i, h := range held {
							fixture.RunStep(t, p.Ctx, h, b.Recorder, heldSteps[i])
							if err := h.Statement.Close(); err != nil {
								t.Fatal(err)
							}
						}
						for i, id := range ids {
							ownershipUnchanged(t, p, c, invariant(id), before[i])
						}
						check := func(conn *sql.Conn, recorder *fixture.Recorder) {
							r := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, recorder, s) }
							values(conn, recorder)
							if foreign {
								ownershipSurvivorMemberRestriction(r, signatures[1], "uuid-ossp")
							} else {
								ownershipSurvivorNormalRestriction(r, signatures[1], "eo_old.hs_witness_2")
							}
							r(fixture.Exec(`SET ROLE eo_survivor_acl`, 0))
							values(conn, recorder)
							r(fixture.Exec(`RESET ROLE`, 0))
							r(ownershipCount(`SELECT count(*) FROM pg_extension WHERE extname='hstore'`, 0))
							ownershipHstoreLinks(r, signatures, foreign)
							symbols := []string{"hstore_from_text", "hstore_from_record", "hstore_populate_record", "hstore_each", "hstore_concat", "hstore_eq", "hstore_skeys", "hstore_svals", "hstore_delete", "hstore_delete_array", "hstore_delete_hstore", "hstore_slice_to_hstore"}
							for i, sig := range signatures {
								r(fixture.Select(`SELECT p.proisstrict,p.provolatile,p.proretset,p.prosrc,p.probin FROM pg_proc p WHERE p.oid='`+sig+`'::regprocedure`, []string{"proisstrict", "provolatile", "proretset", "prosrc", "probin"}, []string{"BOOL", "CHAR", "BOOL", "TEXT", "TEXT"}, [][]any{{i >= 3, "i", i == 3 || i == 6 || i == 7, symbols[i], "$libdir/hstore"}}))
							}
							member := ""
							if foreign {
								member = "uuid-ossp"
							}
							r(fixture.Select(`SELECT COALESCE(e.extname::text,'') AS member_of FROM pg_type t LEFT JOIN pg_depend d ON d.classid='pg_type'::regclass AND d.objid=t.oid AND d.refclassid='pg_extension'::regclass AND d.deptype='e' LEFT JOIN pg_extension e ON e.oid=d.refobjid WHERE t.oid='eo_new.hstore'::regtype`, []string{"member_of"}, []string{"TEXT"}, [][]any{{member}}))
							r(ownershipCount(`SELECT count(*) FROM pg_type t JOIN pg_type a ON a.oid=t.typarray WHERE t.oid='eo_new.hstore'::regtype AND a.typelem=t.oid`, 1))
						}
						var allDeclarations []string
						for _, target := range targets {
							if strings.HasPrefix(target, "FUNCTION ") {
								allDeclarations = append(allDeclarations, strings.TrimPrefix(target, "FUNCTION "))
							}
						}
						ownershipSurvivorControls(t, p, b, c, "hstore_"+name, allDeclarations, check)
					})
				})
			}
		})
	}
}

// Each unchanged check stage reads the real membership bag and one finite
// normal-edge projection. Separate assertions must not rebuild the entire stored
// consumer dependency graph once per expected procedure edge.
func ownershipHstoreLinks(run func(fixture.Step), signatures []string, foreign bool) {
	member := ""
	if foreign {
		member = "uuid-ossp"
	}
	var identities, counts, columns, types []string
	var members [][]any
	var expected []any
	for i, signature := range signatures {
		identity := "'" + signature + "'::regprocedure"
		identities = append(identities, identity)
		members = append(members, []any{signature, member})
		column := "namespace_" + ownershipNumber(int64(i))
		counts = append(counts, `count(*) FILTER (WHERE classid='pg_proc'::regclass AND objid=`+identity+` AND refclassid='pg_namespace'::regclass AND refobjid='eo_new'::regnamespace AND deptype='n') AS `+column)
		columns = append(columns, column)
		types = append(types, "INT8")
		expected = append(expected, int64(1))
	}
	membership := fixture.Select(`SELECT p.oid::regprocedure::text AS signature,COALESCE(e.extname::text,'') AS member_of FROM pg_proc p LEFT JOIN pg_depend d ON d.classid='pg_proc'::regclass AND d.objid=p.oid AND d.refclassid='pg_extension'::regclass AND d.deptype='e' LEFT JOIN pg_extension e ON e.oid=d.refobjid WHERE p.oid IN (`+strings.Join(identities, ",")+`)`, []string{"signature", "member_of"}, []string{"TEXT", "TEXT"}, members)
	membership.Bag = true
	run(membership)
	for _, signature := range signatures {
		run(fixture.Select(`SELECT r.rolname,has_function_privilege('eo_survivor_acl',p.oid,'EXECUTE') AS allowed,p.proacl IS NOT NULL AS explicit_acl,has_function_privilege('public',p.oid,'EXECUTE') AS public_allowed FROM pg_proc p JOIN pg_roles r ON r.oid=p.proowner WHERE p.oid='`+signature+`'::regprocedure`, []string{"rolname", "allowed", "explicit_acl", "public_allowed"}, []string{"NAME", "BOOL", "BOOL", "BOOL"}, [][]any{{"eo_survivor_owner", true, true, false}}))
	}
	counts = append(counts,
		`count(*) FILTER (WHERE classid='pg_operator'::regclass AND objid='eo_new.||(eo_new.hstore,eo_new.hstore)'::regoperator AND refclassid='pg_proc'::regclass AND refobjid='eo_new.eo_concat(eo_new.hstore,eo_new.hstore)'::regprocedure AND deptype='n') AS concat_function`,
		`count(*) FILTER (WHERE classid='pg_type'::regclass AND objid='eo_new.hstore'::regtype AND refclassid='pg_namespace'::regclass AND refobjid='eo_new'::regnamespace AND deptype='n') AS type_namespace`)
	columns = append(columns, "concat_function", "type_namespace")
	types = append(types, "INT8", "INT8")
	expected = append(expected, int64(1), int64(1))
	run(fixture.Select("SELECT "+strings.Join(counts, ",")+" FROM pg_depend", columns, types, [][]any{expected}))
}

func ownershipHstoreTargets(t *testing.T, p *fixture.Pair, c *sql.Conn) []string {
	t.Helper()
	// Use reg* output under a non-visible namespace for fully qualified addresses.
	if _, err := c.ExecContext(p.Ctx, `SET search_path TO public,pg_catalog`); err != nil {
		t.Fatal(err)
	}
	member := ` d JOIN pg_extension e ON e.oid=d.refobjid WHERE d.refclassid='pg_extension'::regclass AND e.extname='hstore' AND d.deptype='e'`
	queries := []string{
		`SELECT 'FUNCTION '||p.oid::regprocedure::text FROM pg_proc p JOIN pg_depend` + strings.Replace(member, " d JOIN", " d ON d.classid='pg_proc'::regclass AND d.objid=p.oid JOIN", 1),
		`SELECT 'OPERATOR '||o.oid::regoperator::text FROM pg_operator o JOIN pg_depend` + strings.Replace(member, " d JOIN", " d ON d.classid='pg_operator'::regclass AND d.objid=o.oid JOIN", 1),
		`SELECT 'CAST ('||c.castsource::regtype::text||' AS '||c.casttarget::regtype::text||')' FROM pg_cast c JOIN pg_depend` + strings.Replace(member, " d JOIN", " d ON d.classid='pg_cast'::regclass AND d.objid=c.oid JOIN", 1),
		`SELECT 'OPERATOR CLASS eo_new.'||o.opcname||' USING '||a.amname FROM pg_opclass o JOIN pg_am a ON a.oid=o.opcmethod JOIN pg_depend` + strings.Replace(member, " d JOIN", " d ON d.classid='pg_opclass'::regclass AND d.objid=o.oid JOIN", 1),
		`SELECT 'OPERATOR FAMILY eo_new.'||o.opfname||' USING '||a.amname FROM pg_opfamily o JOIN pg_am a ON a.oid=o.opfmethod JOIN pg_depend` + strings.Replace(member, " d JOIN", " d ON d.classid='pg_opfamily'::regclass AND d.objid=o.oid JOIN", 1),
		// The upstream script also creates the private GiST storage root
		// ghstore. Select actual non-array extension roots, not a native-only
		// type list; ALTER EXTENSION detaches each associated array itself.
		`SELECT 'TYPE '||t.oid::regtype::text FROM pg_type t JOIN pg_depend` + strings.Replace(member, " d JOIN", " d ON d.classid='pg_type'::regclass AND d.objid=t.oid JOIN", 1) + ` AND t.typelem=0`,
	}
	var out []string
	for _, q := range queries {
		func() {
			rows, err := c.QueryContext(p.Ctx, q)
			if err != nil {
				t.Fatal(q, err)
			}
			defer rows.Close()
			for rows.Next() {
				var target string
				if err := rows.Scan(&target); err != nil {
					t.Fatal(err)
				}
				out = append(out, target)
			}
			if err := rows.Err(); err != nil {
				t.Fatal(err)
			}
		}()
	}
	if len(out) == 0 {
		t.Fatal("missing genuine hstore helper graph")
	}
	return out
}

func ownershipHstoreSurvivorCorpus() []fixture.Step {
	steps := []fixture.Step{
		fixture.Select(`SELECT eo_new.hstore('a',NULL::text)::text AS v`, []string{"v"}, []string{"TEXT"}, [][]any{{`"a"=>NULL`}}),
		fixture.Select(`SELECT eo_new.hstore(NULL::text,'x')::text AS v`, []string{"v"}, []string{"TEXT"}, [][]any{{nil}}),
		fixture.Select(`SELECT eo_new.hstore(NULL::eo_old.hs_record_pair)::text AS v`, []string{"v"}, []string{"TEXT"}, [][]any{{`"i"=>NULL, "n"=>NULL`}}),
		fixture.Select(`SELECT eo_new.populate_record('(7,keep)'::eo_old.hs_record_pair,'i=>0042'::eo_new.hstore)::text AS v`, []string{"v"}, []string{"TEXT"}, [][]any{{"(42,keep)"}}),
		fixture.Select(`SELECT eo_new.populate_record(NULL::eo_old.hs_record_pair,NULL::eo_new.hstore)::text AS v`, []string{"v"}, []string{"TEXT"}, [][]any{{nil}}),
		fixture.Select(`SELECT ('(7,keep)'::eo_old.hs_record_pair OPERATOR(eo_new.#=) 'i=>0042'::eo_new.hstore)::text AS v`, []string{"v"}, []string{"TEXT"}, [][]any{{"(42,keep)"}}),
		fixture.Select(`SELECT (NULL::eo_old.hs_record_pair OPERATOR(eo_new.#=) NULL::eo_new.hstore)::text AS v`, []string{"v"}, []string{"TEXT"}, [][]any{{nil}}),
		fixture.Select(`SELECT 'a=>1'::eo_new.hstore OPERATOR(eo_new.=) 'a=>1'::eo_new.hstore AS v`, []string{"v"}, []string{"BOOL"}, [][]any{{true}}),
		fixture.Select(`SELECT ('a=>1,b=>2'::eo_new.hstore OPERATOR(eo_new.||) 'b=>3'::eo_new.hstore)::text AS v`, []string{"v"}, []string{"TEXT"}, [][]any{{`"a"=>"1", "b"=>"3"`}}),
		fixture.Select(`SELECT delete('a=>1,b=>2'::eo_new.hstore,'b'::text)::text AS v`, []string{"v"}, []string{"TEXT"}, [][]any{{`"a"=>"1"`}}),
		fixture.Select(`SELECT key,value FROM eo_new.each('a=>x,b=>NULL,c=>"NULL",""=>"",d=>x'::eo_new.hstore) ORDER BY key`, []string{"key", "value"}, []string{"TEXT", "TEXT"}, [][]any{{"", ""}, {"a", "x"}, {"b", nil}, {"c", "NULL"}, {"d", "x"}}),
		fixture.Select(`SELECT key,value FROM eo_new.each(NULL::eo_new.hstore)`, []string{"key", "value"}, []string{"TEXT", "TEXT"}, nil),
		fixture.Select(`SELECT eo_new.hs_concat('a=>1'::eo_new.hstore,'a=>NULL'::eo_new.hstore)::text AS v`, []string{"v"}, []string{"TEXT"}, [][]any{{`"a"=>NULL`}}),
		fixture.Select(`SELECT eo_new.skeys('a=>NULL'::eo_new.hstore) AS v`, []string{"v"}, []string{"TEXT"}, [][]any{{"a"}}),
		fixture.Select(`SELECT eo_new.svals('a=>NULL'::eo_new.hstore) AS v`, []string{"v"}, []string{"TEXT"}, [][]any{{nil}}),
		fixture.Select(`SELECT eo_new.delete('a=>NULL,b=>2'::eo_new.hstore,'{a,missing,NULL,a}'::text[])::text AS v`, []string{"v"}, []string{"TEXT"}, [][]any{{`"b"=>"2"`}}),
		fixture.Select(`SELECT eo_new.delete('a=>1,b=>2,c=>3'::eo_new.hstore,'a=>4,b=>2'::eo_new.hstore)::text AS v`, []string{"v"}, []string{"TEXT"}, [][]any{{`"a"=>"1", "c"=>"3"`}}),
		fixture.Select(`SELECT eo_new.slice('a=>NULL,b=>2'::eo_new.hstore,'{a,missing,NULL,a}'::text[])::text AS v`, []string{"v"}, []string{"TEXT"}, [][]any{{`"a"=>NULL`}}),
		fixture.Select(`SELECT ('a=>1'::eo_new.hstore OPERATOR(eo_new.||) NULL::eo_new.hstore)::text AS v`, []string{"v"}, []string{"TEXT"}, [][]any{{nil}}),
	}
	// Qualify the overloaded update helper; no search_path-dependent authority.
	steps[9].Query = strings.Replace(steps[9].Query, "delete(", "eo_new.delete(", 1)
	steps = append(steps, ownershipHstoreMutationCorpus()...)
	return steps
}

func ownershipHstoreMutationCorpus() []fixture.Step {
	var out []fixture.Step
	text := func(q string, rows [][]any) fixture.Step {
		return fixture.Select(q, []string{"v"}, []string{"TEXT"}, rows)
	}
	corpus := `'a=>x,b=>NULL,c=>"NULL",""=>"",d=>x'::eo_new.hstore`
	for _, name := range []string{"skeys", "svals", "each"} {
		for _, input := range []string{corpus, `''::eo_new.hstore`, `NULL::eo_new.hstore`} {
			var rows [][]any
			if input == corpus {
				switch name {
				case "skeys":
					rows = [][]any{{"a"}, {"b"}, {"c"}, {""}, {"d"}}
				case "svals":
					rows = [][]any{{"x"}, {nil}, {"NULL"}, {""}, {"x"}}
				default:
					rows = [][]any{{"a", "x"}, {"b", nil}, {"c", "NULL"}, {"", ""}, {"d", "x"}}
				}
			}
			s := text("SELECT eo_new."+name+"("+input+") AS v", rows)
			if name == "each" {
				s = fixture.Select("SELECT key,value FROM eo_new.each("+input+")", []string{"key", "value"}, []string{"TEXT", "TEXT"}, rows)
			}
			s.Bag = true
			out = append(out, s)
		}
	}
	for _, tc := range []struct{ expression, left, rightType, right, want string }{
		{`eo_new.hs_concat($left,$right)`, "a=>1,b=>2", "eo_new.hstore", "b=>3", `"a"=>"1", "b"=>"3"`},
		{`$left OPERATOR(eo_new.||) $right`, "a=>1,b=>2", "eo_new.hstore", "b=>3", `"a"=>"1", "b"=>"3"`},
		{`eo_new.delete($left,$right)`, "a=>NULL,b=>2", "text", "a", `"b"=>"2"`},
		{`$left OPERATOR(eo_new.-) $right`, "a=>NULL,b=>2", "text", "a", `"b"=>"2"`},
		{`eo_new.delete($left,$right)`, "a=>NULL,b=>2", "text[]", "{a,missing,NULL,a}", `"b"=>"2"`},
		{`$left OPERATOR(eo_new.-) $right`, "a=>NULL,b=>2", "text[]", "{a,missing,NULL,a}", `"b"=>"2"`},
		{`eo_new.delete($left,$right)`, "a=>1,b=>2,c=>3", "eo_new.hstore", "a=>4,b=>2", `"a"=>"1", "c"=>"3"`},
		{`$left OPERATOR(eo_new.-) $right`, "a=>1,b=>2,c=>3", "eo_new.hstore", "a=>4,b=>2", `"a"=>"1", "c"=>"3"`},
		{`eo_new.slice($left,$right)`, "a=>NULL,b=>2", "text[]", "{a,missing,NULL,a}", `"a"=>NULL`},
	} {
		left := fixture.CrosstabQuote(tc.left) + "::eo_new.hstore"
		right := fixture.CrosstabQuote(tc.right) + "::" + tc.rightType
		for side := range 3 {
			l, r := left, right
			var want any = tc.want
			if side == 1 {
				l = "NULL::eo_new.hstore"
				want = nil
			}
			if side == 2 {
				r = "NULL::" + tc.rightType
				want = nil
			}
			expression := strings.NewReplacer("$left", l, "$right", r).Replace(tc.expression)
			out = append(out, text("SELECT ("+expression+")::text AS v", [][]any{{want}}))
		}
	}
	return out
}
