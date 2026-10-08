//go:build reference

package reference_test

import (
	"strings"
	"testing"
)

func TestReferenceCommentInquiryArgumentLimit(t *testing.T) {
	for _, backend := range []string{"Oracle", "Native"} {
		t.Run(backend, func(t *testing.T) {
			p := newReferencePair(t)
			db, rec := p.postgres, p.postgresRecord
			if backend == "Native" {
				db, rec = p.memory, p.memoryRecord
			}
			referenceRunStep(t, p.ctx, db, rec, referenceExec("CREATE TABLE inquiry_limits(id INT)", 0))
			const hint = "No function matches the given name and argument types. You might need to add explicit type casts."
			nulls100 := strings.TrimSuffix(strings.Repeat("NULL,", 100), ",")
			unknowns100 := strings.TrimSuffix(strings.Repeat("unknown, ", 100), ", ")
			// Independent PG18 expectations, not records copied from native:
			// transform every child, apply FUNC_MAX_ARGS=100, then resolve
			// the selected fixed signature. Root observes the pinned oracle.
			for _, fn := range []struct {
				name, signature, lastPosition string
			}{
				{"col_description", "function col_description(" + unknowns100 + ") does not exist", "524"},
				{"obj_description", "function obj_description(" + unknowns100 + ") does not exist", "524"},
				{"shobj_description", "function shobj_description(" + unknowns100 + ") does not exist", "526"},
			} {
				for _, tc := range []struct {
					label, query string
					want         referenceDiagnostic
				}{
					{"101-null", "SELECT " + fn.name + "(" + nulls100 + ",NULL)", referenceDiagnostic{Code: "54023", Message: "cannot pass more than 100 arguments to a function", Position: "8"}},
					{"100-null", "SELECT " + fn.name + "(" + nulls100 + ")", referenceDiagnostic{Code: "42883", Message: fn.signature, Position: "8", Hint: hint}},
					{"last-missing-column", "SELECT " + fn.name + "(" + nulls100 + ",missing) FROM inquiry_limits", referenceDiagnostic{Code: "42703", Message: `column "missing" does not exist`, Position: fn.lastPosition}},
				} {
					t.Run(fn.name+"/"+tc.label+"/Direct", func(t *testing.T) {
						s := referenceImplicitError(tc.query, tc.want)
						s.selectRows = true
						s.errorStage = "execute"
						referenceRunStep(t, p.ctx, db, rec, s)
					})
					t.Run(fn.name+"/"+tc.label+"/Prepare", func(t *testing.T) {
						s := referenceImplicitError(tc.query, tc.want)
						s.selectRows = true
						s.errorStage = "prepare"
						referenceRunStep(t, p.ctx, db, rec, referencePrepared(s))
					})
				}
			}
		})
	}
}
