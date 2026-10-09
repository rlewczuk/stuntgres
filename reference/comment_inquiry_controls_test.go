package reference_test

import (
	"database/sql"
	"strings"
	"stuntgres"
	"testing"
)

func TestReferenceCommentInquiryBuiltinRemovalControls(t *testing.T) {
	for _, nonContext := range []bool{false, true} {
		t.Run(map[bool]string{false: "Recorded", true: "Source"}[nonContext], func(t *testing.T) {
			p := newReferenceScenario(t)
			for _, backend := range p.backends() {
				t.Run(backend.name, func(t *testing.T) {
					db := backend.db
					run := func(s referenceStep) { t.Helper(); s.nonContext = nonContext; commentInquiryStep(t, p, backend, db, s) }
					for _, tc := range []struct {
						name string
						oid  int64
						seed any
					}{
						{"pg_catalog", 11, "system catalog schema"}, {"public", 2200, "standard public schema"},
					} {
						if backend.pg && tc.name == "public" {
							tc.seed = nil
							if err := db.QueryRowContext(p.ctx, "SELECT oid FROM pg_namespace WHERE nspname='public'").Scan(&tc.oid); err != nil {
								t.Fatal(err)
							}
						}
						check := func(want any) {
							t.Helper()
							rows, err := db.Query("SELECT obj_description($1,'pg_namespace')", tc.oid)
							commentInquiryRows(t, rows, err, backend.pg, false, "obj_description", []any{want})
						}
						check(tc.seed)
						run(referenceExec("COMMENT ON SCHEMA "+tc.name+" IS NULL", 0))
						check(nil)
						if !backend.pg {
							text := commentInquiryDump(t, p)
							if !strings.Contains(text, "COMMENT ON SCHEMA \""+tc.name+"\" IS NULL;") {
								t.Fatal(text)
							}
							if err := stuntgres.Reset(p.ctx, p.native); err != nil {
								t.Fatal(err)
							}
							check(tc.seed)
							if err := stuntgres.Restore(p.ctx, p.native, strings.NewReader(text)); err != nil {
								t.Fatal(err)
							}
							check(nil)
							if commentInquiryDump(t, p) != text {
								t.Fatal("noncanonical removal replay")
							}
						} else if tc.seed != nil {
							// Restore the real shared builtin comment, not a fictitious host Reset.
							run(referenceExec("COMMENT ON SCHEMA pg_catalog IS 'system catalog schema'", 0))
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
			p := newReferenceScenario(t)
			for _, backend := range p.backends() {
				t.Run(backend.name, func(t *testing.T) {
					db := backend.db
					run := func(s referenceStep) {
						t.Helper()
						s.nonContext = nonContext && !s.prepared && s.state == ""
						commentInquiryStep(t, p, backend, db, s)
					}
					if nonContext && !backend.pg {
						// The original escaped/view replay follows both builtin removals.
						run(referenceExec("COMMENT ON SCHEMA pg_catalog IS NULL", 0))
						run(referenceExec("COMMENT ON SCHEMA public IS NULL", 0))
					}
					for _, q := range []string{"CREATE TABLE t(a INT,b TEXT)", "COMMENT ON TABLE t IS 'whole'", "COMMENT ON COLUMN t.b IS E'quote '' slash \\\\ newline \\n é'", "CREATE VIEW v AS SELECT col_description('t'::regclass,2) FROM t", "INSERT INTO t VALUES(1,'row')"} {
						count := int64(0)
						if q == "INSERT INTO t VALUES(1,'row')" {
							count = 1
						}
						run(referenceExec(q, count))
					}
					if !backend.pg {
						before := commentInquiryDump(t, p)
						err := stuntgres.Restore(p.ctx, p.native, strings.NewReader(before+"COMMENT ON COLUMN t.missing IS 'bad';"))
						referenceState(t, err, "42703")
						if commentInquiryDump(t, p) != before {
							t.Fatal("failed replay changed state")
						}
						if err := stuntgres.Reset(p.ctx, p.native); err != nil {
							t.Fatal(err)
						}
						if err := stuntgres.Restore(p.ctx, p.native, strings.NewReader(before)); err != nil {
							t.Fatal(err)
						}
						if commentInquiryDump(t, p) != before {
							t.Fatal("noncanonical comment replay")
						}
					} else {
						s := referenceImplicitError("COMMENT ON COLUMN t.missing IS 'bad'", referenceDiagnostic{Code: "42703", Message: `column "missing" of relation "t" does not exist`})
						run(s)
					}
					rows, err := db.Query("SELECT col_description('t'::regclass,2)")
					commentInquiryRows(t, rows, err, backend.pg, false, "col_description", []any{"quote ' slash \\ newline \n é"})
					run(referenceExec("COMMENT ON COLUMN t.b IS 'fresh'", 0))
					for _, prepared := range []bool{false, true} {
						s := referenceSelect("SELECT * FROM v", []string{"col_description"}, []string{"TEXT"}, [][]any{{"fresh"}})
						// The stored view exposes a nullable column descriptor on native,
						// unlike the direct inquiry invocation above; pq remains unknown.
						s.checkMetadata = func(t *testing.T, rows *sql.Rows) {
							commentInquiryMetadata(t, rows, backend.pg, true, s.columns)
						}
						if prepared {
							s = referencePrepared(s)
						}
						run(s)
					}
				})
			}
		})
	}
}
