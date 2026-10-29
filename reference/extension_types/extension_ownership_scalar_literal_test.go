package reference_test

import (
	"bytes"
	"database/sql"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceExtensionOwnershipScalarLiteralCaptures(t *testing.T) {
	// These independent samples retain the existing scalar-family formatting
	// expectations; the private proof additionally compares packed datum payloads.
	for _, tc := range []struct{ provider, family, input, output, in, out string }{
		{"hstore", "hstore", `a=>NULL,b=>"NULL"`, `"a"=>NULL, "b"=>"NULL"`, "hstore_in", "hstore_out"},
		{"ltree", "lquery", "Top.*{0,2}", "Top.*{,2}", "lquery_in", "lquery_out"},
		{"ltree", "ltree", "Top.Countries.Europe", "Top.Countries.Europe", "ltree_in", "ltree_out"},
		{"ltree", "ltxtquery", "Europe&Russia*@&!Transportation", "Europe & Russia@* & !Transportation", "ltxtq_in", "ltxtq_out"},
		{"isn", "ean13", "1234567890120!", "123-456789012-8!", "ean13_in", "ean13_out"},
		{"isn", "isbn", "9780123456780!", "0-12-345678-9!", "isbn_in", "isn_out"},
		{"isn", "isbn13", "9780123456780!", "978-0-12-345678-6!", "isbn13_in", "ean13_out"},
		{"isn", "ismn", "M123456780!", "M-1234-5678-5!", "ismn_in", "isn_out"},
		{"isn", "ismn13", "M123456780!", "979-0-1234-5678-5!", "ismn13_in", "ean13_out"},
		{"isn", "issn", "9771234567898!", "977-1234-567-89-8!", "issn_in", "isn_out"},
		{"isn", "issn13", "9771234567898!", "977-1234-567-89-8!", "issn13_in", "ean13_out"},
		{"isn", "upc", "123456789012", "123456789012", "upc_in", "isn_out"},
	} {
		t.Run(tc.family, func(t *testing.T) {
			fixture.SchemaReferenceCleanup(t, "eo_scalar", "eo_receiver")
			upcScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
				display := tc.output
				if tc.family == "issn" {
					// Reuse the established REL_18_6 ean2ISSN truncation
					// expectation without discarding native issue digits or '!'.
					display = issnOutput(b, display)
				}
				for _, q := range []string{"CREATE SCHEMA eo_scalar", "CREATE SCHEMA eo_receiver", "CREATE EXTENSION " + tc.provider + " SCHEMA eo_scalar", `CREATE EXTENSION "uuid-ossp" SCHEMA eo_receiver`} {
					run(fixture.Exec(q, 0))
				}
				typ := "eo_scalar." + tc.family
				q := "SELECT " + ean13Literal(tc.input) + "::" + typ + "::text AS v"
				step := fixture.Select(q, []string{"v"}, []string{"TEXT"}, [][]any{{display}})
				held := ean13Held(t, p, c, q)
				parameterQuery := "SELECT $1::" + typ + "::text AS v"
				parameter := ean13Held(t, p, c, parameterQuery)
				run(fixture.Exec("CREATE VIEW eo_scalar.literal_value AS "+q, 0))
				checkHeld := func() {
					fixture.RunStep(t, p.Ctx, held, b.Recorder, step)
					for _, input := range []any{tc.input, nil} {
						var output any = display
						if input == nil {
							output = nil
						}
						fixture.RunStep(t, p.Ctx, parameter, b.Recorder, fixture.Select(parameterQuery, []string{"v"}, []string{"TEXT"}, [][]any{{output}}, input))
					}
				}
				checkHeld()
				for _, phase := range []string{"detached", "foreign", "origin_absent"} {
					switch phase {
					case "detached":
						run(fixture.Exec("ALTER EXTENSION "+tc.provider+" DROP TYPE "+typ, 0))
					case "foreign":
						run(fixture.Exec(`ALTER EXTENSION "uuid-ossp" ADD TYPE `+typ, 0))
					case "origin_absent":
						run(fixture.Exec(`ALTER EXTENSION "uuid-ossp" DROP TYPE `+typ, 0))
						run(fixture.Exec("ALTER EXTENSION "+tc.provider+" DROP FUNCTION eo_scalar."+tc.in+"(cstring)", 0))
						run(fixture.Exec("ALTER EXTENSION "+tc.provider+" DROP FUNCTION eo_scalar."+tc.out+"("+typ+")", 0))
						// Preserve the real type's other normal codec/analyzer
						// dependencies where the released upstream recipe has them.
						// Discovery affects setup only, never value expectations.
						rows, err := c.QueryContext(p.Ctx, `SELECT p.oid::regprocedure::text FROM pg_depend td JOIN pg_proc p ON p.oid=td.refobjid JOIN pg_depend em ON em.classid='pg_proc'::regclass AND em.objid=p.oid AND em.refclassid='pg_extension'::regclass AND em.deptype='e' JOIN pg_extension e ON e.oid=em.refobjid WHERE td.classid='pg_type'::regclass AND td.objid=$1::regtype AND td.refclassid='pg_proc'::regclass AND td.deptype='n' AND e.extname=$2`, typ, tc.provider)
						if err != nil {
							t.Fatal(err)
						}
						var dependencies []string
						for rows.Next() {
							var signature string
							if err := rows.Scan(&signature); err != nil {
								rows.Close()
								t.Fatal(err)
							}
							dependencies = append(dependencies, signature)
						}
						if err := rows.Err(); err != nil {
							rows.Close()
							t.Fatal(err)
						}
						if err := rows.Close(); err != nil {
							t.Fatal(err)
						}
						for _, signature := range dependencies {
							run(fixture.Exec("ALTER EXTENSION "+tc.provider+" DROP FUNCTION "+signature, 0))
						}
						run(fixture.Exec("DROP EXTENSION "+tc.provider, 0))
					}
					// Membership changes alone may leave SQLType pointers unchanged.
					// Rename twice forces genuine new type generations while retaining
					// the held literal's original nominal identity and final spelling.
					run(fixture.Exec("ALTER TYPE "+typ+" RENAME TO held_scalar", 0))
					run(fixture.Exec("ALTER TYPE eo_scalar.held_scalar RENAME TO "+tc.family, 0))
					checkHeld()
					run(step)
					run(fixture.Prepared(step))
				}
				check := func(conn *sql.Conn, recorder *fixture.Recorder) {
					r := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, recorder, s) }
					r(step)
					r(fixture.Prepared(step))
					s := fixture.Select("SELECT v FROM eo_scalar.literal_value", []string{"v"}, []string{"TEXT"}, [][]any{{display}})
					r(s)
					r(fixture.Prepared(s))
					r(fixture.Prepared(fixture.Select(parameterQuery, []string{"v"}, []string{"TEXT"}, [][]any{{nil}}, nil)))
					r(fixture.Select("SELECT count(*) FROM pg_extension WHERE extname="+ean13Literal(tc.provider), []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
					r(fixture.Select("SELECT count(*) FROM pg_depend WHERE classid='pg_type'::regclass AND objid='"+typ+"'::regtype AND refclassid='pg_extension'::regclass AND deptype='e'", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
				}
				check(c, b.Recorder)
				if b.PG {
					return
				}
				var output bytes.Buffer
				if err := stuntgres.Dump(p.Ctx, p.Native, &output); err != nil {
					t.Fatal(err)
				}
				connector, err := stuntgres.NewDriver().OpenConnector("eo_scalar_" + tc.family)
				if err != nil {
					t.Fatal(err)
				}
				recorded, recorder := fixture.RecordedNativePool(t, connector)
				other := sql.OpenDB(connector)
				defer other.Close()
				for _, destination := range []struct {
					control, recorded *sql.DB
					recorder          *fixture.Recorder
				}{{p.Native, b.DB, b.Recorder}, {other, recorded, recorder}} {
					if err := stuntgres.Reset(p.Ctx, destination.control); err != nil {
						t.Fatal(err)
					}
					if err := stuntgres.Restore(p.Ctx, destination.control, strings.NewReader(output.String())); err != nil {
						t.Fatal(err)
					}
					conn, err := destination.recorded.Conn(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					check(conn, destination.recorder)
					if err := conn.Close(); err != nil {
						t.Fatal(err)
					}
				}
			})
		})
	}
}
