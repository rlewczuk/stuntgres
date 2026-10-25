package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceCommentInquiryDiagnostics(t *testing.T) {
	for _, nonContext := range []bool{false, true} {
		t.Run(map[bool]string{false: "Recorded", true: "Source"}[nonContext], func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, backend := range p.Backends() {
				t.Run(backend.Name, func(t *testing.T) {
					db, rec := backend.DB, backend.Recorder
					run := func(s fixture.Step) { t.Helper(); s.NonContext = nonContext; fixture.RunStep(t, p.Ctx, db, rec, s) }
					const hint = "No function matches the given name and argument types. You might need to add explicit type casts."
					for _, tc := range []struct {
						q    string
						want fixture.Diagnostic
					}{
						{"SELECT col_description()", fixture.Diagnostic{Code: "42883", Message: "function col_description() does not exist", Position: "8", Hint: hint}},
						{"SELECT /*é*/ col_description()", fixture.Diagnostic{Code: "42883", Message: "function col_description() does not exist", Position: "14", Hint: hint}},
						{"SELECT col_description(1)", fixture.Diagnostic{Code: "42883", Message: "function col_description(integer) does not exist", Position: "8", Hint: hint}},
						{"SELECT col_description(1::oid,1::bigint)", fixture.Diagnostic{Code: "42883", Message: "function col_description(oid, bigint) does not exist", Position: "8", Hint: hint}},
						{"SELECT col_description('1'::text,1)", fixture.Diagnostic{Code: "42883", Message: "function col_description(text, integer) does not exist", Position: "8", Hint: hint}},
						{"SELECT obj_description(1,TRUE)", fixture.Diagnostic{Code: "42883", Message: "function obj_description(integer, boolean) does not exist", Position: "8", Hint: hint}},
						{"SELECT shobj_description(1)", fixture.Diagnostic{Code: "42883", Message: "function shobj_description(integer) does not exist", Position: "8", Hint: hint}},
						{"SELECT col_description(VARIADIC NULL)", fixture.Diagnostic{Code: "42883", Message: "function col_description(unknown) does not exist", Position: "8", Hint: hint}},
						{"SELECT shobj_description(VARIADIC NULL)", fixture.Diagnostic{Code: "42883", Message: "function shobj_description(unknown) does not exist", Position: "8", Hint: hint}},
						{"SELECT col_description('bad',1)", fixture.Diagnostic{Code: "22P02", Message: `invalid input syntax for type oid: "bad"`, Position: "24"}},
						{"SELECT col_description(1,'bad')", fixture.Diagnostic{Code: "22P02", Message: `invalid input syntax for type integer: "bad"`, Position: "26"}},
						{"SELECT col_description(1,'2147483648')", fixture.Diagnostic{Code: "22003", Message: `value "2147483648" is out of range for type integer`, Position: "26"}},
						{"SELECT col_description(NULL,'bad')", fixture.Diagnostic{Code: "22P02", Message: `invalid input syntax for type integer: "bad"`, Position: "29"}},
						{"SELECT public.col_description(1,1)", fixture.Diagnostic{Code: "42883", Message: "function public.col_description(integer, integer) does not exist", Position: "8", Hint: hint}},
						{"SELECT missing_schema.col_description(1,1)", fixture.Diagnostic{Code: "3F000", Message: `schema "missing_schema" does not exist`, Position: "8"}},
					} {
						s := fixture.ImplicitError(tc.q, tc.want)
						s.SelectRows = true
						s.ErrorStage = "execute"
						run(s)
						s.ErrorStage = "prepare"
						run(fixture.Prepared(s))
					}
					held, err := db.Prepare("SELECT col_description($1,$2)")
					if err != nil {
						t.Fatal(err)
					}
					defer held.Close()
					for _, tc := range []struct {
						q                      string
						args                   []any
						code, message, context string
					}{
						{"SELECT col_description($1,$2)", []any{"bad", 1}, "22P02", `invalid input syntax for type oid: "bad"`, `unnamed portal parameter $1 = '...'`},
						{"SELECT col_description($1,$2)", []any{nil, "bad"}, "22P02", `invalid input syntax for type integer: "bad"`, `unnamed portal parameter $2 = '...'`},
						{"SELECT col_description($1,$2)", []any{nil, "2147483648"}, "22003", `value "2147483648" is out of range for type integer`, `unnamed portal parameter $2 = '...'`},
					} {
						s := fixture.ImplicitError(tc.q, fixture.Diagnostic{Code: tc.code, Message: tc.message}, tc.args...)
						s.SelectRows = true
						s.ErrorStage = "execute"
						pg := *s.Diagnostic
						pg.Context = tc.context
						s.Diagnostic = &pg
						fixture.RunStep(t, p.Ctx, db, rec, fixture.Prepared(s))
						rows, err := held.Query(tc.args...)
						if rows != nil {
							rows.Close()
							t.Fatal("bad input exposed cursor")
						}
						fixture.AssertDiagnostic(t, err, s.Diagnostic)
						executor := fixture.HeldStatement{Executor: db, Statement: held, SQL: tc.q}
						fixture.RunStep(t, p.Ctx, executor, rec, s)
					}
					if err := held.Close(); err != nil {
						t.Fatal(err)
					}
				})
			}
		})
	}
}

func TestReferenceCommentInquiryVariadicMatching(t *testing.T) {
	// REL_18_0 parse_func.c deliberately discards VARIADIC decoration when
	// the matched routine has no variadic parameter. Arity mismatches above
	// are not evidence that all VARIADIC calls to these routines fail.
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			db := backend.DB
			for _, tc := range []struct{ expression, label string }{
				{"col_description(NULL,VARIADIC NULL)", "col_description"},
				{"obj_description(VARIADIC NULL)", "obj_description"},
				{"obj_description(NULL,VARIADIC NULL)", "obj_description"},
				{"shobj_description(NULL,VARIADIC NULL)", "shobj_description"},
			} {
				s := fixture.Select("SELECT "+tc.expression, []string{tc.label}, []string{"TEXT"}, [][]any{{nil}})
				commentInquiryStep(t, p, backend, db, s)
				commentInquiryStep(t, p, backend, db, fixture.Prepared(s))
				rows, err := db.Query(s.Query)
				commentInquiryRows(t, rows, err, backend.PG, false, tc.label, []any{nil})
				stmt, err := db.Prepare(s.Query)
				if err != nil {
					t.Fatal(err)
				}
				rows, err = stmt.Query()
				commentInquiryRows(t, rows, err, backend.PG, false, tc.label, []any{nil})
				if err := stmt.Close(); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

func TestReferenceCommentInquiryDeniedFunctionSchema(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"inquiry_denied"})
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			db, rec := backend.DB, backend.Recorder
			conn, err := db.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			defer func() {
				_, resetErr := conn.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
				if resetErr != nil {
					t.Error(resetErr)
				}
			}()
			run := func(s fixture.Step) { t.Helper(); fixture.RunStep(t, p.Ctx, conn, rec, s) }
			run(fixture.Exec("CREATE USER inquiry_denied", 0))
			run(fixture.Exec("CREATE SCHEMA inquiry_private", 0))
			run(fixture.Exec("SET SESSION AUTHORIZATION inquiry_denied", 0))
			// namespace.c checks explicit function namespace USAGE during
			// lookup; parser callback supplies the function token Position8.
			s := fixture.ImplicitError("SELECT inquiry_private.col_description(1,1)", fixture.Diagnostic{Code: "42501", Message: "permission denied for schema inquiry_private", Position: "8"})
			s.SelectRows = true
			s.ErrorStage = "execute"
			run(s)
			s.ErrorStage = "prepare"
			run(fixture.Prepared(s))
			run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
			run(fixture.Exec("DROP SCHEMA inquiry_private", 0))
			run(fixture.Exec("DROP ROLE inquiry_denied", 0))
		})
	}
}
