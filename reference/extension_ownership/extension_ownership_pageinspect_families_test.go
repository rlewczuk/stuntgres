package reference_test

import (
	"database/sql"
	"encoding/hex"
	"testing"

	"stuntgres/reference/fixture"
)

// These are the same independently assembled release-layout images used by the
// decoder suites. Membership mutation must not alter their released OUT ABI.
func ownershipPageinspectFamiliesSurvivors(t *testing.T) {
	cases := []struct {
		name         string
		names, types []string
		pages        [][]byte
		rows         [][][]any
		nullRows     [][]any
		order        string
	}{
		{"bt_page_items", fixture.BTNames, fixture.BTTypes, [][]byte{fixture.BTOrdinary(), fixture.BTPosting()}, [][][]any{fixture.BTOrdinaryRows, fixture.BTPostingRows}, nil, " ORDER BY itemoffset"},
		{"fsm_page_contents", []string{"contents"}, []string{"TEXT"}, [][]byte{fixture.FSMSparse()}, [][][]any{{{fixture.FSMSparseText}}}, [][]any{{nil}}, ""},
		{fixture.GINMembers[0], fixture.GINNames[0], fixture.GINTypes[0], [][]byte{fixture.GINMeta()}, [][][]any{fixture.GINMetaRows}, fixture.GINNullRows(0), ""},
		{fixture.GINMembers[1], fixture.GINNames[1], fixture.GINTypes[1], [][]byte{fixture.GINPage(0xffff)}, [][][]any{fixture.GINOpaqueRows}, fixture.GINNullRows(1), ""},
		{fixture.GINMembers[2], fixture.GINNames[2], fixture.GINTypes[2], [][]byte{fixture.GINPosting()}, [][][]any{fixture.GINLeafRows}, nil, " ORDER BY first_tid"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, foreign := range []bool{false, true} {
				state := "detached"
				if foreign {
					state = "foreign"
				}
				t.Run(state, func(t *testing.T) {
					fixture.RoleReferenceCleanup(t, []string{"eo_survivor_owner", "eo_survivor_acl"})
					fixture.OwnershipScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
						run := fixture.OwnershipRun(t, p, b, c)
						fixture.OwnershipSurvivorRoles(run)
						run(fixture.Exec(`CREATE EXTENSION pageinspect SCHEMA eo_old`, 0))
						original := "eo_old." + tc.name + "(bytea)"
						fixture.OwnershipSurvivorACL(run, original)
						switch tc.name {
						case "bt_page_items":
							run(fixture.BTDeclaration(tc.name, "eo_old", "eo_survivor_owner"))
						case "fsm_page_contents":
							run(fixture.FSMDeclaration(tc.name, "eo_old", "eo_survivor_owner"))
						default:
							for i, name := range fixture.GINMembers {
								if name == tc.name {
									run(fixture.GINDeclaration(i, tc.name, "eo_old", "eo_survivor_owner"))
								}
							}
						}
						id := fixture.OwnershipID(t, p, c, `SELECT '`+original+`'::regprocedure::bigint`)
						declaration := fixture.OwnershipSnapshot(t, p, c, fixture.OwnershipSurvivorDeclaration(original))
						if len(declaration) != 1 {
							t.Fatal("missing released declaration", original)
						}
						run(fixture.Exec(`ALTER EXTENSION pageinspect DROP FUNCTION `+original, 0))
						fixture.OwnershipUnchanged(t, p, c, fixture.OwnershipSurvivorDeclaration(original), declaration)
						run(fixture.OwnershipCount(`SELECT count(*) FROM pg_depend WHERE classid='pg_proc'::regclass AND objid=`+fixture.OwnershipNumber(id)+` AND refclassid='pg_extension'::regclass AND deptype='e'`, 0))
						run(fixture.Exec(`ALTER FUNCTION `+original+` RENAME TO eo_inspect`, 0))
						run(fixture.Exec(`ALTER FUNCTION eo_old.eo_inspect(bytea) SET SCHEMA eo_new`, 0))
						signature := "eo_new.eo_inspect(bytea)"
						if got := fixture.OwnershipID(t, p, c, `SELECT '`+signature+`'::regprocedure::bigint`); got != id {
							t.Fatal("survivor identity changed", got, id)
						}
						moved := fixture.OwnershipSnapshot(t, p, c, fixture.OwnershipSurvivorDeclaration(signature))
						query := func(argument string) string {
							if tc.name == "fsm_page_contents" {
								return `SELECT eo_new.eo_inspect(` + argument + `) AS contents`
							}
							return `SELECT * FROM eo_new.eo_inspect(` + argument + `)` + tc.order
						}
						metadata := func(s fixture.Step, view bool) fixture.Step {
							switch tc.name {
							case "bt_page_items":
								return fixture.BTMetadata(s, b.PG, view)
							case "fsm_page_contents":
								return fixture.FSMMetadata(s, b.PG, view)
							default:
								return fixture.GINMetadata(s, b.PG, view)
							}
						}
						values := func(conn *sql.Conn, recorder *fixture.Recorder) {
							r := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, recorder, s) }
							for i, page := range tc.pages {
								s := metadata(fixture.Select(query(`$1::bytea`), tc.names, tc.types, tc.rows[i], page), false)
								r(s)
								r(fixture.Prepared(s))
							}
							s := metadata(fixture.Select(query(`NULL::bytea`), tc.names, tc.types, tc.nullRows), false)
							r(s)
							r(fixture.Prepared(s))
							zeroRows := tc.nullRows
							if tc.name == "bt_page_items" {
								zeroRows = fixture.BTNullRows
							}
							if tc.name == "gin_leafpage_items" {
								zeroRows = fixture.GINNullRows(2)
							}
							zero := metadata(fixture.Select(query(`$1::bytea`), tc.names, tc.types, zeroRows, make([]byte, 8192)), false)
							r(zero)
							r(fixture.Prepared(zero))
						}
						values(c, b.Recorder)
						run(fixture.Exec(`ALTER EXTENSION "uuid-ossp" ADD FUNCTION `+signature, 0))
						values(c, b.Recorder)
						for _, suffix := range []string{"", " CASCADE"} {
							s := fixture.OwnershipError(`DROP FUNCTION `+signature+suffix, "2BP01", `cannot drop function eo_new.eo_inspect(bytea) because extension uuid-ossp requires it`)
							s.Diagnostic.Hint = "You can drop extension uuid-ossp instead."
							run(s)
						}
						run(fixture.Exec(`CREATE VIEW eo_new.eo_inspected AS `+query(`'\x`+hex.EncodeToString(tc.pages[0])+`'::bytea`), 0))
						run(fixture.Exec(`CREATE VIEW eo_new.eo_inspected_null AS `+query(`NULL::bytea`), 0))
						run(fixture.Exec(`DROP EXTENSION pageinspect`, 0))
						if !foreign {
							run(fixture.Exec(`ALTER EXTENSION "uuid-ossp" DROP FUNCTION `+signature, 0))
						}
						if got := fixture.OwnershipID(t, p, c, `SELECT '`+signature+`'::regprocedure::bigint`); got != id {
							t.Fatal("origin retirement changed survivor identity", got, id)
						}
						check := func(conn *sql.Conn, recorder *fixture.Recorder) {
							r := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, recorder, s) }
							values(conn, recorder)
							fixture.OwnershipUnchanged(t, p, conn, fixture.OwnershipSurvivorDeclaration(signature), moved)
							ownershipSurvivorLinks(r, signature, foreign)
							r(fixture.OwnershipCount(`SELECT count(*) FROM pg_extension WHERE extname='pageinspect'`, 0))
							r(fixture.OwnershipCount(`SELECT count(*) FROM pg_depend WHERE classid='pg_rewrite'::regclass AND refclassid='pg_proc'::regclass AND refobjid='`+signature+`'::regprocedure AND deptype='n'`, 2))
							s := metadata(fixture.Select(`SELECT * FROM eo_new.eo_inspected`+tc.order, tc.names, tc.types, tc.rows[0]), true)
							r(s)
							r(fixture.Prepared(s))
							storedNull := metadata(fixture.Select(`SELECT * FROM eo_new.eo_inspected_null`+tc.order, tc.names, tc.types, tc.nullRows), true)
							r(storedNull)
							r(fixture.Prepared(storedNull))
							// An explicit EXECUTE grant does not bypass pageinspect's superuser gate.
							r(fixture.Exec(`SET ROLE eo_survivor_acl`, 0))
							denied := fixture.ImplicitError(query(`$1::bytea`), fixture.Diagnostic{Code: "42501", Message: "must be superuser to use raw page functions"}, tc.pages[0])
							denied.SelectRows = true
							denied.ErrorStage = "execute"
							r(denied)
							r(fixture.Prepared(denied))
							null := metadata(fixture.Select(query(`NULL::bytea`), tc.names, tc.types, tc.nullRows), false)
							r(null)
							r(fixture.Prepared(null))
							r(fixture.Exec(`RESET ROLE`, 0))
						}
						// Ensure the granted caller reaches the real execution permission check.
						run(fixture.Exec(`GRANT USAGE ON SCHEMA eo_new TO eo_survivor_acl`, 0))
						fixture.OwnershipSurvivorControls(t, p, b, c, "pageinspect_"+tc.name+"_"+state, []string{signature}, check)
					})
				})
			}
		})
	}
}
