package reference_test

import (
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

// Expectations come from PostgreSQL REL_18_6: objectaddress.c's
// get_object_address_opcf resolves the method strictly before the family;
// OpFamilyCacheLookup passes missing_ok to LookupExplicitNamespace, which still
// checks USAGE on existing schemas. dropcmds.c's OBJECT_OPFAMILY branch chooses
// the schema-specific skip notice. See also:
// https://www.postgresql.org/docs/18/sql-dropopfamily.html
// https://github.com/postgres/postgres/blob/REL_18_6/src/backend/commands/dropcmds.c
// https://github.com/postgres/postgres/blob/REL_18_6/src/backend/catalog/namespace.c
func TestReferenceOperatorFamilyReviewMissingSchema(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"family_review_schema_reader"})
	fixture.SchemaReferenceCleanup(t, "family_review_schema_denied")
	familyBackends(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		var notices []fixture.Diagnostic
		_, conn, rec := fixture.NoticeReferencePool(t, p, b.PG, &notices)
		defer conn.ExecContext(p.Ctx, `RESET ROLE`)
		run := func(s fixture.Step) {
			fixture.RunStep(t, p.Ctx, conn, rec, s)
		}
		run(fixture.Exec(familyCreate, 0))
		run(fixture.Exec(familyPopulate, 0))
		run(fixture.Exec(`CREATE ROLE family_review_schema_reader`, 0))
		run(fixture.Exec(`GRANT USAGE ON SCHEMA public TO family_review_schema_reader`, 0))
		run(fixture.Exec(`CREATE SCHEMA family_review_schema_denied`, 0))
		run(fixture.Exec(`REVOKE ALL ON SCHEMA family_review_schema_denied FROM PUBLIC`, 0))
		run(fixture.Exec(`CREATE OPERATOR FAMILY family_review_schema_denied.family_guard USING btree`, 0))
		run(fixture.Select(`SELECT count(*) FROM pg_namespace WHERE nspname='family_review_missing_schema'`, []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
		run(fixture.Select(`SELECT count(*) FROM pg_opfamily WHERE opfname='family_probe' AND opfnamespace='public'::regnamespace`, []string{"count"}, []string{"INT8"}, [][]any{{int64(1)}}))
		run(fixture.Select(`SELECT count(*) FROM pg_amop a JOIN pg_opfamily f ON f.oid=a.amopfamily WHERE f.opfname='family_probe' AND f.opfnamespace='public'::regnamespace`, []string{"count"}, []string{"INT8"}, [][]any{{int64(5)}}))
		run(fixture.Select(`SELECT count(*) FROM pg_amproc a JOIN pg_opfamily f ON f.oid=a.amprocfamily WHERE f.opfname='family_probe' AND f.opfnamespace='public'::regnamespace`, []string{"count"}, []string{"INT8"}, [][]any{{int64(1)}}))

		// Preserve complete family/member catalog values and identities, not just
		// row counts. Runtime user OIDs are relational; the invariant is that no
		// attempted DROP below changes any row on its own backend.
		snapshot := func(t *testing.T) [][][]string {
			t.Helper()
			queries := []string{
				`SELECT oid,opfmethod,opfname,opfnamespace,opfowner FROM pg_opfamily ORDER BY oid`,
				`SELECT oid,amopfamily,amoplefttype,amoprighttype,amopstrategy,amoppurpose,amopopr,amopmethod,amopsortfamily FROM pg_amop ORDER BY oid`,
				`SELECT oid,amprocfamily,amproclefttype,amprocrighttype,amprocnum,amproc::oid FROM pg_amproc ORDER BY oid`,
			}
			var result [][][]string
			for _, query := range queries {
				rows, err := conn.QueryContext(p.Ctx, query)
				if err != nil {
					t.Fatal(err)
				}
				columns, err := rows.Columns()
				if err != nil {
					rows.Close()
					t.Fatal(err)
				}
				var table [][]string
				for rows.Next() {
					values := make([]string, len(columns))
					targets := make([]any, len(columns))
					for i := range values {
						targets[i] = &values[i]
					}
					if err := rows.Scan(targets...); err != nil {
						rows.Close()
						t.Fatal(err)
					}
					table = append(table, values)
				}
				err = rows.Err()
				closeErr := rows.Close()
				if err != nil {
					t.Fatal(err)
				}
				if closeErr != nil {
					t.Fatal(closeErr)
				}
				result = append(result, table)
			}
			return result
		}
		before := snapshot(t)
		cases := []struct {
			name   string
			query  string
			denied bool
			error  *fixture.Diagnostic
			notice *fixture.Diagnostic
		}{
			{
				name:   "missing-schema-if-exists",
				query:  `DROP OPERATOR FAMILY IF EXISTS family_review_missing_schema.f USING btree`,
				notice: &fixture.Diagnostic{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: `schema "family_review_missing_schema" does not exist, skipping`},
			},
			{
				name:  "missing-schema-strict",
				query: `DROP OPERATOR FAMILY family_review_missing_schema.f USING btree`,
				error: &fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "3F000", Message: `schema "family_review_missing_schema" does not exist`},
			},
			{
				name:   "existing-schema-missing-family",
				query:  `DROP OPERATOR FAMILY IF EXISTS public.family_review_schema_absent USING btree`,
				notice: &fixture.Diagnostic{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: `operator family "public.family_review_schema_absent" does not exist for access method "btree", skipping`},
			},
			{
				name:   "inaccessible-existing-schema",
				query:  `DROP OPERATOR FAMILY IF EXISTS family_review_schema_denied.family_guard USING btree`,
				denied: true,
				error:  &fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42501", Message: `permission denied for schema family_review_schema_denied`},
			},
			{
				name:  "alter-missing-schema",
				query: `ALTER OPERATOR FAMILY family_review_missing_schema.f USING btree RENAME TO family_unused`,
				error: &fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "3F000", Message: `schema "family_review_missing_schema" does not exist`},
			},
			{
				name:   "alter-inaccessible-schema",
				query:  `ALTER OPERATOR FAMILY family_review_schema_denied.family_guard USING btree RENAME TO family_unused`,
				denied: true,
				error:  &fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42501", Message: `permission denied for schema family_review_schema_denied`},
			},
			{
				name:  "member-function-missing-schema",
				query: `ALTER OPERATOR FAMILY public.family_probe USING btree ADD FUNCTION 1 family_review_missing_schema.f(integer,integer)`,
				error: &fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "3F000", Message: `schema "family_review_missing_schema" does not exist`},
			},
			{
				name:  "member-operator-missing-schema",
				query: `ALTER OPERATOR FAMILY public.family_probe USING btree ADD OPERATOR 2 family_review_missing_schema.< (integer,integer)`,
				error: &fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42883", Message: `operator does not exist: integer family_review_missing_schema.< integer`},
			},
			{
				name:  "unknown-method-before-missing-schema",
				query: `DROP OPERATOR FAMILY IF EXISTS family_review_missing_schema.f USING family_review_missing_method`,
				error: &fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42704", Message: `access method "family_review_missing_method" does not exist`},
			},
		}
		for _, prepared := range []bool{false, true} {
			mode := "direct"
			if prepared {
				mode = "prepared"
			}
			for _, tc := range cases {
				t.Run(mode+"/"+tc.name, func(t *testing.T) {
					run := func(s fixture.Step) {
						fixture.RunStep(t, p.Ctx, conn, rec, s)
					}
					if tc.denied {
						run(fixture.Exec(`SET ROLE family_review_schema_reader`, 0))
						defer conn.ExecContext(p.Ctx, `RESET ROLE`)
					}
					notices = nil
					s := fixture.Exec(tc.query, 0)
					if tc.error != nil {
						s = fixture.ErrorDiagnostic(tc.query, *tc.error)
					}
					s.Prepared = prepared
					run(s)
					run(fixture.Select(`SELECT 42 AS sentinel`, []string{"sentinel"}, []string{"INT4"}, [][]any{{int64(42)}}))
					if tc.notice == nil {
						if len(notices) != 0 {
							t.Fatalf("unexpected notices: %+v", notices)
						}
					} else if len(notices) != 1 || notices[0] != *tc.notice {
						t.Fatalf("notices: %+v; want exactly %+v", notices, *tc.notice)
					}
					if tc.denied {
						run(fixture.Exec(`RESET ROLE`, 0))
					}
					if after := snapshot(t); !reflect.DeepEqual(after, before) {
						t.Fatalf("DROP changed family/member catalogs: before=%v after=%v", before, after)
					}
				})
			}
		}
	})
}
