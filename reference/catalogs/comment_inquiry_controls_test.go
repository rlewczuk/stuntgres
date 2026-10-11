package reference_test

import (
	"database/sql"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceCommentInquiryBuiltinRemovalControls(t *testing.T) {
	for _, nonContext := range []bool{false, true} {
		t.Run(map[bool]string{false: "Recorded", true: "Source"}[nonContext], func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, backend := range p.Backends() {
				t.Run(backend.Name, func(t *testing.T) {
					db := backend.DB
					run := func(s fixture.Step) { t.Helper(); s.NonContext = nonContext; commentInquiryStep(t, p, backend, db, s) }
					for _, tc := range []struct {
						name string
						oid  int64
						seed any
					}{
						{"pg_catalog", 11, "system catalog schema"}, {"public", 2200, "standard public schema"},
					} {
						if backend.PG && tc.name == "public" {
							tc.seed = nil
							if err := db.QueryRowContext(p.Ctx, "SELECT oid FROM pg_namespace WHERE nspname='public'").Scan(&tc.oid); err != nil {
								t.Fatal(err)
							}
						}
						check := func(want any) {
							t.Helper()
							rows, err := db.Query("SELECT obj_description($1,'pg_namespace')", tc.oid)
							commentInquiryRows(t, rows, err, backend.PG, false, "obj_description", []any{want})
						}
						check(tc.seed)
						run(fixture.Exec("COMMENT ON SCHEMA "+tc.name+" IS NULL", 0))
						check(nil)
						run(fixture.Exec("COMMENT ON ROLE postgres IS 'shared'", 0))
						if !backend.PG {
							text := commentInquiryDump(t, p)
							if !strings.Contains(text, "COMMENT ON SCHEMA \""+tc.name+"\" IS NULL;") {
								t.Fatal(text)
							}
							if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
								t.Fatal(err)
							}
							check(tc.seed)
							if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(text)); err != nil {
								t.Fatal(err)
							}
							check(nil)
							rows, err := db.QueryContext(p.Ctx, "SELECT shobj_description(10,'pg_authid')")
							commentInquiryRows(t, rows, err, false, false, "shobj_description", []any{"shared"})
							if commentInquiryDump(t, p) != text {
								t.Fatal("noncanonical removal replay")
							}
						} else if tc.seed != nil {
							// Restore the real shared builtin comment, not a fictitious host Reset.
							run(fixture.Exec("COMMENT ON SCHEMA pg_catalog IS 'system catalog schema'", 0))
							check(tc.seed)
						}
					}
				})
			}
		})
	}
}

func TestReferenceCommentInquiryControls(t *testing.T) {
	for _, nonContext := range []bool{false, true} {
		t.Run(map[bool]string{false: "Recorded", true: "Source"}[nonContext], func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, backend := range p.Backends() {
				t.Run(backend.Name, func(t *testing.T) {
					db := backend.DB
					run := func(s fixture.Step) {
						t.Helper()
						s.NonContext = nonContext && !s.Prepared && s.State == ""
						commentInquiryStep(t, p, backend, db, s)
					}
					if nonContext && !backend.PG {
						// The original escaped/view replay follows both builtin removals.
						run(fixture.Exec("COMMENT ON SCHEMA pg_catalog IS NULL", 0))
						run(fixture.Exec("COMMENT ON SCHEMA public IS NULL", 0))
					}
					for _, q := range []string{"CREATE TABLE t(a INT,b TEXT)", "COMMENT ON TABLE t IS 'whole'", "COMMENT ON COLUMN t.b IS E'quote '' slash \\\\ newline \\n é'", "CREATE VIEW v AS SELECT col_description('t'::regclass,2) FROM t", "INSERT INTO t VALUES(1,'row')"} {
						count := int64(0)
						if q == "INSERT INTO t VALUES(1,'row')" {
							count = 1
						}
						run(fixture.Exec(q, count))
					}
					if !backend.PG {
						before := commentInquiryDump(t, p)
						err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(before+"COMMENT ON COLUMN t.missing IS 'bad';"))
						fixture.State(t, err, "42703")
						if commentInquiryDump(t, p) != before {
							t.Fatal("failed replay changed state")
						}
						if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
							t.Fatal(err)
						}
						if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(before)); err != nil {
							t.Fatal(err)
						}
						if commentInquiryDump(t, p) != before {
							t.Fatal("noncanonical comment replay")
						}
					} else {
						s := fixture.ImplicitError("COMMENT ON COLUMN t.missing IS 'bad'", fixture.Diagnostic{Code: "42703", Message: `column "missing" of relation "t" does not exist`})
						run(s)
					}
					rows, err := db.Query("SELECT col_description('t'::regclass,2)")
					commentInquiryRows(t, rows, err, backend.PG, false, "col_description", []any{"quote ' slash \\ newline \n é"})
					run(fixture.Exec("COMMENT ON COLUMN t.b IS 'fresh'", 0))
					for _, prepared := range []bool{false, true} {
						s := fixture.Select("SELECT * FROM v", []string{"col_description"}, []string{"TEXT"}, [][]any{{"fresh"}})
						// The stored view exposes a nullable column descriptor on native,
						// unlike the direct inquiry invocation above; pq remains unknown.
						s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
							commentInquiryMetadata(t, rows, backend.PG, true, s.Columns)
						}
						if prepared {
							s = fixture.Prepared(s)
						}
						run(s)
					}
				})
			}
		})
	}
}
