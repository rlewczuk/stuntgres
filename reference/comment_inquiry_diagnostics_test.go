//go:build reference

package reference_test

import "testing"

func TestReferenceCommentInquiryDiagnostics(t *testing.T) {
	for _, backend := range []string{"Oracle", "Native"} {
		t.Run(backend, func(t *testing.T) {
			p := newReferencePair(t)
			db, rec := p.postgres, p.postgresRecord
			if backend == "Native" {
				db, rec = p.memory, p.memoryRecord
			}
			const hint = "No function matches the given name and argument types. You might need to add explicit type casts."
			for _, tc := range []struct {
				q    string
				want referenceDiagnostic
			}{
				{"SELECT col_description()", referenceDiagnostic{Code: "42883", Message: "function col_description() does not exist", Position: "8", Hint: hint}},
				{"SELECT /*é*/ col_description()", referenceDiagnostic{Code: "42883", Message: "function col_description() does not exist", Position: "14", Hint: hint}},
				{"SELECT col_description(1)", referenceDiagnostic{Code: "42883", Message: "function col_description(integer) does not exist", Position: "8", Hint: hint}},
				{"SELECT col_description(1::oid,1::bigint)", referenceDiagnostic{Code: "42883", Message: "function col_description(oid, bigint) does not exist", Position: "8", Hint: hint}},
				{"SELECT col_description('1'::text,1)", referenceDiagnostic{Code: "42883", Message: "function col_description(text, integer) does not exist", Position: "8", Hint: hint}},
				{"SELECT obj_description(1,TRUE)", referenceDiagnostic{Code: "42883", Message: "function obj_description(integer, boolean) does not exist", Position: "8", Hint: hint}},
				{"SELECT shobj_description(1)", referenceDiagnostic{Code: "42883", Message: "function shobj_description(integer) does not exist", Position: "8", Hint: hint}},
				{"SELECT col_description('bad',1)", referenceDiagnostic{Code: "22P02", Message: `invalid input syntax for type oid: "bad"`, Position: "24"}},
				{"SELECT col_description(1,'bad')", referenceDiagnostic{Code: "22P02", Message: `invalid input syntax for type integer: "bad"`, Position: "26"}},
				{"SELECT col_description(1,'2147483648')", referenceDiagnostic{Code: "22003", Message: `value "2147483648" is out of range for type integer`, Position: "26"}},
				{"SELECT col_description(NULL,'bad')", referenceDiagnostic{Code: "22P02", Message: `invalid input syntax for type integer: "bad"`, Position: "29"}},
				{"SELECT public.col_description(1,1)", referenceDiagnostic{Code: "42883", Message: "function public.col_description(integer, integer) does not exist", Position: "8", Hint: hint}},
				{"SELECT missing_schema.col_description(1,1)", referenceDiagnostic{Code: "3F000", Message: `schema "missing_schema" does not exist`, Position: "8"}},
			} {
				s := referenceImplicitError(tc.q, tc.want)
				s.selectRows = true
				s.errorStage = "execute"
				referenceRunStep(t, p.ctx, db, rec, s)
				s.errorStage = "prepare"
				referenceRunStep(t, p.ctx, db, rec, referencePrepared(s))
			}
			for _, tc := range []struct {
				q                      string
				args                   []any
				code, message, context string
			}{
				{"SELECT col_description($1,$2)", []any{"bad", 1}, "22P02", `invalid input syntax for type oid: "bad"`, `unnamed portal parameter $1 = '...'`},
				{"SELECT col_description($1,$2)", []any{nil, "bad"}, "22P02", `invalid input syntax for type integer: "bad"`, `unnamed portal parameter $2 = '...'`},
				{"SELECT col_description($1,$2)", []any{nil, "2147483648"}, "22003", `value "2147483648" is out of range for type integer`, `unnamed portal parameter $2 = '...'`},
			} {
				s := referenceImplicitError(tc.q, referenceDiagnostic{Code: tc.code, Message: tc.message}, tc.args...)
				s.selectRows = true
				s.errorStage = "execute"
				pg := *s.diagnostic
				pg.Context = tc.context
				s.postgresDiagnostic = &pg
				referenceRunStep(t, p.ctx, db, rec, referencePrepared(s))
			}
		})
	}
}
