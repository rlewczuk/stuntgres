package reference_test

import (
	"bytes"
	"database/sql"
	"fmt"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

// Expectations come from the released UPC conversion corpus, not native output.
// The upstream script has additional cast/function members; discovery below is
// setup-only so both backends retain their actual conversion implementation.
func TestReferenceExtensionOwnershipISNConversions(t *testing.T) {
	for _, state := range []string{"one_detached", "both_detached", "foreign", "origin_absent"} {
		t.Run(state, func(t *testing.T) {
			fixture.SchemaReferenceCleanup(t, "eo_isn")
			fixture.SchemaReferenceCleanup(t, "eo_receiver")
			upcScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
				for _, q := range []string{"CREATE SCHEMA eo_isn", "CREATE SCHEMA eo_receiver", "CREATE EXTENSION isn SCHEMA eo_isn", "SET search_path=eo_isn,pg_catalog"} {
					run(fixture.Exec(q, 0))
				}
				for i, tc := range upcConversionValues {
					run(fixture.Exec(fmt.Sprintf("CREATE VIEW eo_isn.conversion_%d AS SELECT %s AS v", i, tc.expression), 0))
				}
				heldQuery := "SELECT '123456789010!'::upc::ean13::text AS v"
				held := ean13Held(t, p, c, heldQuery)
				heldStep := fixture.Select(heldQuery, []string{"v"}, []string{"TEXT"}, [][]any{{"012-345678901-2!"}})
				fixture.RunStep(t, p.Ctx, held, b.Recorder, heldStep)
				run(fixture.Exec("ALTER EXTENSION isn DROP TYPE eo_isn.upc", 0))
				if state != "one_detached" {
					run(fixture.Exec("ALTER EXTENSION isn DROP TYPE eo_isn.ean13", 0))
				}
				if state == "foreign" {
					run(fixture.Exec(`CREATE EXTENSION "uuid-ossp" SCHEMA eo_receiver`, 0))
					run(fixture.Exec(`ALTER EXTENSION "uuid-ossp" ADD TYPE eo_isn.upc`, 0))
					run(fixture.Exec(`ALTER EXTENSION "uuid-ossp" ADD TYPE eo_isn.ean13`, 0))
				}
				if state == "origin_absent" {
					for _, signature := range []string{"upc_in(cstring)", "isn_out(eo_isn.upc)", "ean13_in(cstring)", "ean13_out(eo_isn.ean13)"} {
						run(fixture.Exec("ALTER EXTENSION isn DROP FUNCTION eo_isn."+signature, 0))
					}
					// Native uses intrinsic conversion metadata; PostgreSQL owns real
					// pg_cast declarations and the reverse conversion function.
					for _, query := range []string{
						`SELECT 'FUNCTION ' || p.oid::regprocedure::text FROM pg_proc p JOIN pg_depend d ON d.classid='pg_proc'::regclass AND d.objid=p.oid AND d.deptype='e' JOIN pg_extension e ON e.oid=d.refobjid WHERE e.extname='isn' AND p.proname='upc'`,
						`SELECT 'CAST (' || c.castsource::regtype::text || ' AS ' || c.casttarget::regtype::text || ')' FROM pg_cast c JOIN pg_depend d ON d.classid='pg_cast'::regclass AND d.objid=c.oid AND d.deptype='e' JOIN pg_extension e ON e.oid=d.refobjid WHERE e.extname='isn' AND c.castsource IN ('eo_isn.upc'::regtype,'eo_isn.ean13'::regtype) AND c.casttarget IN ('eo_isn.upc'::regtype,'eo_isn.ean13'::regtype)`,
					} {
						rows, err := c.QueryContext(p.Ctx, query)
						if err != nil {
							t.Fatal(err)
						}
						var objects []string
						for rows.Next() {
							var object string
							if err := rows.Scan(&object); err != nil {
								rows.Close()
								t.Fatal(err)
							}
							objects = append(objects, object)
						}
						if err := rows.Err(); err != nil {
							rows.Close()
							t.Fatal(err)
						}
						if err := rows.Close(); err != nil {
							t.Fatal(err)
						}
						for _, object := range objects {
							run(fixture.Exec("ALTER EXTENSION isn DROP "+object, 0))
						}
					}
					run(fixture.Exec("DROP EXTENSION isn", 0))
				}
				fixture.RunStep(t, p.Ctx, held, b.Recorder, heldStep)
				check := func(conn *sql.Conn, recorder *fixture.Recorder) {
					r := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, recorder, s) }
					r(fixture.Exec("SET search_path=eo_isn,pg_catalog", 0))
					for i, tc := range upcConversionValues {
						for _, q := range []string{"SELECT " + tc.expression + " AS v", fmt.Sprintf("SELECT v FROM eo_isn.conversion_%d", i)} {
							s := upcSelect(b, q, []string{"v"}, []string{tc.typ}, [][]any{{tc.output}}, nil)
							r(s)
							r(fixture.Prepared(s))
						}
					}
					for _, tc := range []struct {
						query, typ    string
						input, output any
					}{
						{"SELECT $1::upc::ean13 AS v", "EAN13", "123456789010!", "012-345678901-2!"},
						{"SELECT $1::ean13::upc AS v", "UPC", "0123456789010!", "123456789012!"},
						{"SELECT $1::upc::ean13 AS v", "EAN13", nil, nil},
						{"SELECT $1::ean13::upc AS v", "UPC", nil, nil},
					} {
						s := upcSelect(b, tc.query, []string{"v"}, []string{tc.typ}, [][]any{{tc.output}}, nil, tc.input)
						r(s)
						r(fixture.Prepared(s))
					}
					for _, prepared := range []bool{false, true} {
						s := fixture.ErrorDiagnostic("SELECT '9780123456786'::ean13::upc", ean13Diagnostic("22P02", `cannot cast EAN13(ISBN) to UPC for number: "9780123456786"`))
						s.ErrorStage = "execute"
						if prepared {
							s = fixture.Prepared(s)
						}
						r(s)
					}
					memberCount := int64(0)
					if state == "one_detached" {
						memberCount = 1
					}
					if state == "foreign" {
						memberCount = 2
					}
					r(fixture.Select(`SELECT count(*) FROM pg_depend WHERE classid='pg_type'::regclass AND objid IN ('eo_isn.upc'::regtype,'eo_isn.ean13'::regtype) AND refclassid='pg_extension'::regclass AND deptype='e'`, []string{"count"}, []string{"INT8"}, [][]any{{memberCount}}))
					originCount := int64(1)
					if state == "origin_absent" {
						originCount = 0
					}
					r(fixture.Select(`SELECT count(*) FROM pg_extension WHERE extname='isn'`, []string{"count"}, []string{"INT8"}, [][]any{{originCount}}))
					if state == "foreign" {
						r(fixture.Select(`SELECT count(*) FROM pg_depend d JOIN pg_extension e ON e.oid=d.refobjid WHERE d.classid='pg_type'::regclass AND d.objid IN ('eo_isn.upc'::regtype,'eo_isn.ean13'::regtype) AND d.refclassid='pg_extension'::regclass AND d.deptype='e' AND e.extname='uuid-ossp'`, []string{"count"}, []string{"INT8"}, [][]any{{int64(2)}}))
					}
				}
				check(c, b.Recorder)
				if b.PG {
					return
				}
				var output bytes.Buffer
				if err := stuntgres.Dump(p.Ctx, p.Native, &output); err != nil {
					t.Fatal(err)
				}
				connector, err := stuntgres.NewDriver().OpenConnector("eo_isn_conversions_" + state)
				if err != nil {
					t.Fatal(err)
				}
				recorded, recorder := fixture.RecordedNativePool(t, connector)
				other := sql.OpenDB(connector)
				defer other.Close()
				for _, destination := range []struct {
					control, recorded *sql.DB
					recorder          *fixture.Recorder
				}{
					{p.Native, b.DB, b.Recorder}, {other, recorded, recorder},
				} {
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
