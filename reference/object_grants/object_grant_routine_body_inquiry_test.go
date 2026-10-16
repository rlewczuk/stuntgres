package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceObjectGrantRoutineBodyInquiry(t *testing.T) {
	// These expected surviving executable predicates are initial source-based
	// oracle questions, not claims inferred from a textual WHERE clause.
	// nodeIndexscan.c initializes indexqualorig even for exact indexquals;
	// nodeIndexonlyscan.c can instead omit those executable comparisons. The
	// root must pin the actual catalog-body access paths before native edits.
	for _, op := range []struct{ name, target, primitive string }{
		{"int4eq", "pg_catalog.int4eq(integer,integer)", "n=n"},
		{"oideq", "pg_catalog.oideq(oid,oid)", "o=o"},
		{"nameeq", "pg_catalog.nameeq(name,name)", "nm=nm"},
	} {
		t.Run(op.name, func(t *testing.T) {
			for _, tc := range []struct {
				name, function, expression, nullExpression, parameterExpression string
				shared, usesInt4, usesName                                      bool
			}{
				{"object_catalog", "obj_description", "obj_description(o,'pg_class')", "obj_description(NULL::OID,'pg_class')", "obj_description($1::OID,'pg_class')", false, true, true},
				{"object_legacy", "obj_description", "obj_description(o)", "obj_description(NULL::OID)", "obj_description($1::OID)", false, true, false},
				{"column", "col_description", "col_description(o,1)", "col_description(NULL::OID,1)", "col_description($1::OID,1)", false, true, false},
				{"shared", "shobj_description", "shobj_description(o,'pg_authid')", "shobj_description(NULL::OID,'pg_authid')", "shobj_description($1::OID,'pg_authid')", true, false, true},
			} {
				t.Run(tc.name, func(t *testing.T) {
					objectGrantScenario(t, nil, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
						defer objectGrantRoutineCleanup(t, p, c, []string{op.target})
						for _, q := range []string{
							"GRANT USAGE ON SCHEMA public TO og_object_reader",
							"CREATE TABLE og_body_comment(x INT)",
							"COMMENT ON TABLE og_body_comment IS 'body table comment'",
							"COMMENT ON COLUMN og_body_comment.x IS 'body column comment'",
							"COMMENT ON ROLE og_object_other IS 'body shared comment'",
							"CREATE TABLE og_inquiry_source(o OID,n INT,nm NAME,null_oid OID)",
							"GRANT SELECT ON og_inquiry_source TO og_object_reader",
						} {
							run(fixture.Exec(q, 0))
						}
						value := "body table comment"
						if tc.shared {
							run(fixture.Exec("INSERT INTO og_inquiry_source SELECT oid,1,'pg_authid',NULL FROM pg_roles WHERE rolname='og_object_other'", 1))
							value = "body shared comment"
						} else {
							run(fixture.Exec("INSERT INTO og_inquiry_source VALUES('og_body_comment'::REGCLASS,1,'pg_class',NULL)", 1))
							if tc.name == "column" {
								value = "body column comment"
							}
						}
						query := "SELECT " + tc.expression + " AS v FROM og_inquiry_source"
						nullColumn := tc.function + "(null_oid"
						switch tc.name {
						case "object_catalog":
							nullColumn += ",'pg_class')"
						case "object_legacy":
							nullColumn += ")"
						case "column":
							nullColumn += ",1)"
						case "shared":
							nullColumn += ",'pg_authid')"
						}
						want := fixture.Select(query, []string{"v"}, []string{"TEXT"}, [][]any{{value}})
						run(fixture.Exec("SET SESSION AUTHORIZATION og_object_reader", 0))
						stmt, err := c.PrepareContext(p.Ctx, query)
						if err != nil {
							t.Fatal(err)
						}
						defer stmt.Close()
						held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: query}
						fixture.RunStep(t, p.Ctx, held, b.Recorder, want)
						run(want)
						miss := "obj_description(0::OID,'pg_class')"
						if tc.name == "object_legacy" {
							miss = "obj_description(0::OID)"
						} else if tc.name == "column" {
							miss = "col_description(0::OID,1)"
						} else if tc.shared {
							miss = "shobj_description(0::OID,'pg_authid')"
						}
						run(fixture.Select("SELECT "+miss+" AS v", []string{"v"}, []string{"TEXT"}, [][]any{{nil}}))
						run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
						denies := op.name == "oideq" || op.name == "int4eq" && tc.usesInt4 || op.name == "nameeq" && tc.usesName
						phase := func(allowed bool) {
							fixture.RoleGrantActor(run, "og_object_reader", func() {
								if allowed || !denies {
									run(want)
									fixture.RunStep(t, p.Ctx, held, b.Recorder, want)
									run(fixture.Select("SELECT "+miss+" AS v", []string{"v"}, []string{"TEXT"}, [][]any{{nil}}))
								} else {
									s := fixture.ImplicitError(query, fixture.Diagnostic{Code: "42501", Message: "permission denied for function " + op.name, Context: `SQL function "` + tc.function + `" statement 1`})
									s.SelectRows, s.ErrorStage = true, "execute"
									run(s)
									fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
									missing := fixture.ImplicitError("SELECT "+miss+" AS v", fixture.Diagnostic{Code: "42501", Message: "permission denied for function " + op.name, Context: `SQL function "` + tc.function + `" statement 1`})
									missing.SelectRows, missing.ErrorStage = true, "execute"
									run(missing)
								}
								// Query bodies cannot scalar-inline. No row or strict NULL
								// means no body entry, even when its operators are denied.
								run(fixture.Select(query+" WHERE FALSE", []string{"v"}, []string{"TEXT"}, nil))
								run(fixture.Select("SELECT "+tc.nullExpression+" AS v", []string{"v"}, []string{"TEXT"}, [][]any{{nil}}))
								run(fixture.Select("SELECT "+tc.parameterExpression+" AS v", []string{"v"}, []string{"TEXT"}, [][]any{{nil}}, nil))
								// Feed an actual nullable column, not only a folded NULL.
								run(fixture.Select("SELECT "+nullColumn+" AS v FROM og_inquiry_source", []string{"v"}, []string{"TEXT"}, [][]any{{nil}}))
								run(fixture.Select("SELECT CASE WHEN FALSE THEN "+tc.expression+" ELSE 'dead body' END AS v FROM og_inquiry_source", []string{"v"}, []string{"TEXT"}, [][]any{{"dead body"}}))
								primitive := "SELECT " + op.primitive + " AS v FROM og_inquiry_source"
								if allowed {
									run(fixture.Select(primitive, []string{"v"}, []string{"BOOL"}, [][]any{{true}}))
								} else {
									fixture.RoleGrantPermissionDenied(run, primitive, "function", op.name)
								}
							})
						}
						run(fixture.Exec("REVOKE EXECUTE ON FUNCTION "+op.target+" FROM PUBLIC", 0))
						phase(false)
						run(fixture.Exec("GRANT EXECUTE ON FUNCTION "+op.target+" TO og_object_reader", 0))
						phase(true)
						run(fixture.Exec("REVOKE EXECUTE ON FUNCTION "+op.target+" FROM og_object_reader", 0))
						phase(false)
					})
				})
			}
		})
	}
}
