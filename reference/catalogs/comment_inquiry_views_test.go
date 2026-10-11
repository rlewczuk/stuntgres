package reference_test

import (
	"database/sql"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceCommentInquiryConstantNullViews(t *testing.T) {
	for _, nonContext := range []bool{false, true} {
		t.Run(map[bool]string{false: "Recorded", true: "Source"}[nonContext], func(t *testing.T) {
			for _, population := range []struct {
				name, insert string
				count        int64
				want         []any
			}{
				{"original", "INSERT INTO t VALUES(1),(2)", 2, []any{nil, nil}},
				{"duplicate", "INSERT INTO t VALUES(1),(1),(2)", 3, []any{nil, nil, nil}},
			} {
				t.Run(population.name, func(t *testing.T) {
					p := fixture.NewScenario(t)
					for _, backend := range p.Backends() {
						t.Run(backend.Name, func(t *testing.T) {
							db := backend.DB
							run := func(s fixture.Step) {
								t.Helper()
								s.NonContext = nonContext && population.name == "original" && !s.SelectRows
								commentInquiryStep(t, p, backend, db, s)
							}
							run(fixture.Exec("CREATE TABLE t(a INT)", 0))
							run(fixture.Exec(population.insert, population.count))
							run(fixture.Exec("CREATE SEQUENCE null_sibling", 0))
							cases := []struct{ name, expression string }{
								{"null_column", "col_description(NULL,1)"},
								{"null_local", "obj_description(NULL,'pg_class')"},
								{"null_legacy", "obj_description(NULL)"},
								{"null_shared", "shobj_description(NULL,'pg_authid')"},
								{"null_column_number", "col_description(a,NULL)"},
								{"null_local_catalog", "obj_description(a,NULL)"},
								{"null_shared_catalog", "shobj_description(a,NULL)"},
								{"null_sequence", "obj_description(nextval('null_sibling'),NULL)"},
								{"null_case", "CASE WHEN a=1 THEN obj_description(NULL,'pg_class') ELSE col_description(NULL,1) END"},
							}
							for _, tc := range cases {
								run(fixture.Exec("CREATE VIEW "+tc.name+" AS SELECT "+tc.expression+" AS description FROM t", 0))
							}
							check := func() {
								t.Helper()
								for _, tc := range cases {
									q := "SELECT description FROM " + tc.name
									rows, err := db.Query(q)
									commentInquiryRows(t, rows, err, backend.PG, true, "description", population.want)
									stmt, err := db.Prepare(q)
									if err != nil {
										t.Fatal(tc.name, err)
									}
									rows, err = stmt.Query()
									commentInquiryRows(t, rows, err, backend.PG, true, "description", population.want)
									if err := stmt.Close(); err != nil {
										t.Fatal(err)
									}
									want := make([][]any, len(population.want))
									for i := range want {
										want[i] = []any{nil}
									}
									s := fixture.Select(q, []string{"description"}, []string{"TEXT"}, want)
									// Stored strict-NULL view columns retain known nullable
									// descriptors on native; pq reports them as unknown.
									s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
										commentInquiryMetadata(t, rows, backend.PG, true, s.Columns)
									}
									run(s)
									run(fixture.Prepared(s))
								}
							}
							check()
							if !backend.PG {
								before := commentInquiryDump(t, p)
								if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
									t.Fatal(err)
								}
								if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(before)); err != nil {
									t.Fatal(err)
								}
								check()
								if commentInquiryDump(t, p) != before {
									t.Fatal("noncanonical strict-NULL view replay")
								}
							}
							var next int64
							if err := db.QueryRow("SELECT nextval('null_sibling')").Scan(&next); err != nil || next != 1 {
								t.Fatal("strict-NULL demanded sequence sibling", next, err)
							}
						})
					}
				})
			}
		})
	}
}
