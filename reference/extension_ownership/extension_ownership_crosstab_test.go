package reference_test

import (
	"database/sql"
	"fmt"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

// Exercise every released crosstab signature, including the nominal result
// graph. The source and independent expected rows are the baseline corpus.
func ownershipCrosstabSurvivors(t *testing.T) {
	for _, foreign := range []bool{false, true} {
		name := "detached"
		if foreign {
			name = "foreign"
		}
		t.Run(name, func(t *testing.T) {
			fixture.RoleReferenceCleanup(t, []string{"eo_survivor_owner", "eo_survivor_acl"})
			fixture.OwnershipScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				run := fixture.OwnershipRun(t, p, b, c)
				fixture.OwnershipSurvivorRoles(run)
				fixture.CrosstabSeedInSchema(t, p, b, c, "eo_old")
				run(fixture.Exec(`GRANT USAGE ON SCHEMA public TO eo_survivor_acl`, 0))
				run(fixture.Exec(`GRANT SELECT ON public.ct_source TO eo_survivor_acl`, 0))
				sourceSQL := strings.Replace(fixture.CrosstabSource, "FROM ct_source", "FROM public.ct_source", 1)
				old := []struct {
					signature, result, symbol string
					width                     int
				}{
					{"crosstab(text)", "record", "crosstab", 0},
					{"crosstab(text,integer)", "record", "crosstab", 0},
					{"crosstab2(text)", "eo_new.tablefunc_crosstab_2", "crosstab", 2},
					{"crosstab3(text)", "eo_new.tablefunc_crosstab_3", "crosstab", 3},
					{"crosstab4(text)", "eo_new.tablefunc_crosstab_4", "crosstab", 4},
					{"crosstab(text,text)", "record", "crosstab_hash", 0},
				}
				signatures := make([]string, len(old))
				ids := make([]int64, len(old))
				declarations := make([][][]any, len(old))
				held := make([]fixture.HeldStatement, len(old))
				heldSteps := make([]fixture.Step, len(old))
				invariant := func(id int64) string {
					return `SELECT p.oid::bigint,p.proowner::bigint,p.proacl::text,p.proargtypes::text,p.prorettype::bigint,p.proisstrict,p.provolatile,p.proretset,p.proparallel,p.procost,p.prorows,p.prosrc,p.probin FROM pg_proc p WHERE p.oid=` + fixture.OwnershipNumber(id)
				}
				query := func(i int, routine, arg string) string {
					call := routine + "(" + arg
					if old[i].signature == "crosstab(text,integer)" {
						call += ",3::integer"
					}
					if old[i].signature == "crosstab(text,text)" {
						call += "," + fixture.CrosstabQuote("SELECT category FROM (VALUES(1,'z'::text),(2,'a'),(3,'x')) AS cats(seq,category) ORDER BY seq")
					}
					call += ")"
					if old[i].width == 0 {
						return "SELECT * FROM " + call + " AS t(k text,c1 text,c2 text,c3 text) ORDER BY k"
					}
					return "SELECT * FROM " + call + " ORDER BY row_name"
				}
				want := func(i int, q string, args ...any) fixture.Step {
					if old[i].signature == "crosstab(text,text)" {
						return fixture.Select(q, []string{"k", "c1", "c2", "c3"}, []string{"TEXT", "TEXT", "TEXT", "TEXT"}, [][]any{{"g1", "A", "C", nil}, {"g2", nil, nil, "B2"}, {"g3", nil, nil, nil}}, args...)
					}
					if old[i].width == 0 {
						return fixture.CrosstabWant(q, args...)
					}
					n := old[i].width
					columns := []string{"row_name"}
					types := []string{"TEXT"}
					rows := [][]any{{"g1", "A", "C"}, {"g2", "B1", "B2"}, {"g3", nil, nil}}
					for j := 1; j <= n; j++ {
						columns = append(columns, fmt.Sprintf("category_%d", j))
						types = append(types, "TEXT")
					}
					if n >= 3 {
						rows[0] = append(rows[0], nil)
						rows[1] = append(rows[1], "B3")
						rows[2] = append(rows[2], nil)
					}
					if n == 4 {
						rows[0] = append(rows[0], nil)
						rows[1] = append(rows[1], "B4")
						rows[2] = append(rows[2], nil)
					}
					return fixture.Select(q, columns, types, rows, args...)
				}
				for i, declaration := range old {
					sig := declaration.signature
					original := "eo_old." + sig
					fixture.OwnershipSurvivorACL(run, original)
					ids[i] = fixture.OwnershipID(t, p, c, "SELECT '"+original+"'::regprocedure::bigint")
					declarations[i] = fixture.OwnershipSnapshot(t, p, c, invariant(ids[i]))
					routine := strings.Split(original, "(")[0]
					run(fixture.Exec(fmt.Sprintf("CREATE VIEW eo_old.eo_pivot_%d AS %s", i, query(i, routine, fixture.CrosstabQuote(sourceSQL))), 0))
					run(fixture.Exec(fmt.Sprintf("GRANT SELECT ON eo_old.eo_pivot_%d TO eo_survivor_acl", i), 0))
					// Hold a stable SQL spelling through the genuinely bound view.
					// PostgreSQL reparses invalidated ordinary prepared SQL, so a
					// prepared call using the retired routine name is not retained
					// catalog identity. Fresh calls below use the new spelling.
					heldQuery := fmt.Sprintf("SELECT * FROM eo_old.eo_pivot_%d ORDER BY 1", i)
					held[i] = fixture.CrosstabHeld(t, p, c, heldQuery)
					heldSteps[i] = want(i, heldQuery)
					fixture.OwnershipSurvivorMutation(t, p, c, run, "tablefunc", original)
					run(fixture.Exec("ALTER EXTENSION tablefunc DROP FUNCTION "+original, 0))
					fixture.OwnershipSurvivorNonmember(run, "tablefunc", original)
					ownershipSurvivorNormalRestriction(run, original, fmt.Sprintf("eo_old.eo_pivot_%d", i))
					renamed := fmt.Sprintf("eo_pivot_fn_%d", i)
					run(fixture.Exec("ALTER FUNCTION "+original+" RENAME TO "+renamed, 0))
					args := sig[strings.Index(sig, "("):]
					run(fixture.Exec("ALTER FUNCTION eo_old."+renamed+args+" SET SCHEMA eo_new", 0))
					signatures[i] = "eo_new." + renamed + args
				}
				for n := 2; n <= 4; n++ {
					typ := fmt.Sprintf("tablefunc_crosstab_%d", n)
					run(fixture.Exec("ALTER EXTENSION tablefunc DROP TYPE eo_old."+typ, 0))
					run(fixture.Exec("ALTER TYPE eo_old."+typ+" SET SCHEMA eo_new", 0))
					run(fixture.Exec("ALTER TYPE eo_new."+typ+" OWNER TO eo_survivor_owner", 0))
				}
				values := func(conn *sql.Conn, recorder *fixture.Recorder) {
					r := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, recorder, s) }
					for i, sig := range signatures {
						routine := strings.Split(sig, "(")[0]
						s := want(i, query(i, routine, "$1::text"), sourceSQL)
						r(s)
						r(fixture.Prepared(s))
						s = want(i, query(i, routine, "NULL::text"))
						s.Rows = nil
						r(s)
						r(fixture.Prepared(s))
						if i == 1 {
							s = want(i, query(i, routine, "'SELECT missing_survivor'::text"))
							s.Query = strings.Replace(s.Query, ",3::integer", ",NULL::integer", 1)
							s.Rows = nil
							r(s)
							r(fixture.Prepared(s))
						}
						s = want(i, fmt.Sprintf("SELECT * FROM eo_old.eo_pivot_%d ORDER BY 1", i))
						r(s)
						r(fixture.Prepared(s))
					}
				}
				values(c, b.Recorder)
				for _, sig := range signatures {
					run(fixture.Exec(`ALTER EXTENSION "uuid-ossp" ADD FUNCTION `+sig, 0))
				}
				for _, sig := range signatures {
					fixture.OwnershipSurvivorMemberRestriction(run, sig, "uuid-ossp")
				}
				for _, sig := range signatures {
					for _, ext := range []string{"tablefunc", `"uuid-ossp"`} {
						run(fixture.OwnershipError("ALTER EXTENSION "+ext+" ADD FUNCTION "+sig, "55000", "function "+sig+` is already a member of extension "uuid-ossp"`))
					}
					fixture.OwnershipSurvivorNonmember(run, "tablefunc", sig)
				}
				values(c, b.Recorder)
				run(fixture.Exec("DROP EXTENSION tablefunc", 0))
				if !foreign {
					for _, sig := range signatures {
						run(fixture.Exec(`ALTER EXTENSION "uuid-ossp" DROP FUNCTION `+sig, 0))
					}
				}
				for i, id := range ids {
					fixture.OwnershipUnchanged(t, p, c, invariant(id), declarations[i])
				}
				for i, h := range held {
					fixture.RunStep(t, p.Ctx, h, b.Recorder, heldSteps[i])
					if err := h.Statement.Close(); err != nil {
						t.Fatal(err)
					}
				}
				check := func(conn *sql.Conn, recorder *fixture.Recorder) {
					r := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, recorder, s) }
					values(conn, recorder)
					for i, sig := range signatures {
						if foreign {
							fixture.OwnershipSurvivorMemberRestriction(r, sig, "uuid-ossp")
						} else {
							ownershipSurvivorNormalRestriction(r, sig, fmt.Sprintf("eo_old.eo_pivot_%d", i))
						}
					}
					r(fixture.Exec(`SET ROLE eo_survivor_acl`, 0))
					values(conn, recorder)
					r(fixture.Exec(`RESET ROLE`, 0))
					r(fixture.OwnershipCount(`SELECT count(*) FROM pg_extension WHERE extname='tablefunc'`, 0))
					for i, sig := range signatures {
						ownershipSurvivorLinks(r, sig, foreign)
						symbol := old[i].symbol
						result := old[i].result
						r(fixture.Select(`SELECT p.prorettype::regtype::text,p.proisstrict,p.provolatile,p.proretset,p.proparallel,p.procost,p.prorows,p.prosrc,p.probin FROM pg_proc p WHERE p.oid='`+sig+`'::regprocedure`, []string{"prorettype", "proisstrict", "provolatile", "proretset", "proparallel", "procost", "prorows", "prosrc", "probin"}, []string{"TEXT", "BOOL", "CHAR", "BOOL", "CHAR", "FLOAT4", "FLOAT4", "TEXT", "TEXT"}, [][]any{{result, true, "s", true, "u", float64(1), float64(1000), symbol, "$libdir/tablefunc"}}))
						if old[i].width != 0 {
							r(fixture.OwnershipCount(`SELECT count(*) FROM pg_depend WHERE classid='pg_proc'::regclass AND objid='`+sig+`'::regprocedure AND refclassid='pg_type'::regclass AND refobjid='`+result+`'::regtype AND deptype='n'`, 1))
							r(fixture.OwnershipCount(`SELECT count(*) FROM pg_type t JOIN pg_class c ON c.oid=t.typrelid JOIN pg_type a ON a.oid=t.typarray WHERE t.oid='`+result+`'::regtype AND c.reltype=t.oid AND a.typelem=t.oid`, 1))
							r(fixture.OwnershipCount(`SELECT count(*) FROM pg_depend WHERE classid='pg_type'::regclass AND objid IN (SELECT oid FROM pg_type WHERE oid='`+result+`'::regtype OR typelem='`+result+`'::regtype) AND refclassid='pg_extension'::regclass AND deptype='e'`, 0))
						}
					}
				}
				fixture.OwnershipSurvivorControls(t, p, b, c, "crosstab_"+name, signatures, check)
			})
		})
	}
}
