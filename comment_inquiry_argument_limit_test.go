package stuntgres_test

import (
	"strings"
	"stuntgres"
	"testing"
)

func TestCommentInquiryArgumentLimit(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	execSQL(t, db, "CREATE TABLE inquiry_limits(id INT)")
	const hint = "No function matches the given name and argument types. You might need to add explicit type casts."
	nulls100 := strings.TrimSuffix(strings.Repeat("NULL,", 100), ",")
	unknowns100 := strings.TrimSuffix(strings.Repeat("unknown, ", 100), ", ")
	for _, fn := range []struct {
		name, signature, lastPosition string
	}{
		{"col_description", "function col_description(" + unknowns100 + ") does not exist", "524"},
		{"obj_description", "function obj_description(" + unknowns100 + ") does not exist", "524"},
		{"shobj_description", "function shobj_description(" + unknowns100 + ") does not exist", "526"},
	} {
		for _, tc := range []struct {
			label, query string
			want         stuntgres.Error
		}{
			{"101-null", "SELECT " + fn.name + "(" + nulls100 + ",NULL)", stuntgres.Error{Code: "54023", Message: "cannot pass more than 100 arguments to a function", Position: "8"}},
			{"100-null", "SELECT " + fn.name + "(" + nulls100 + ")", stuntgres.Error{Code: "42883", Message: fn.signature, Position: "8", Hint: hint}},
			// The last child must be analyzed before the outer limit, even
			// though the preceding 100 NULL children transform successfully.
			{"last-missing-column", "SELECT " + fn.name + "(" + nulls100 + ",missing) FROM inquiry_limits", stuntgres.Error{Code: "42703", Message: `column "missing" does not exist`, Position: fn.lastPosition}},
		} {
			t.Run(fn.name+"/"+tc.label+"/Direct", func(t *testing.T) {
				rows, err := db.Query(tc.query)
				if rows != nil {
					rows.Close()
					t.Fatal("failed analysis exposed a cursor")
				}
				typeCoreDiagnostic(t, err, tc.want)
			})
			t.Run(fn.name+"/"+tc.label+"/Prepare", func(t *testing.T) {
				stmt, err := db.Prepare(tc.query)
				if stmt != nil {
					stmt.Close()
					t.Fatal("failed analysis exposed a handle")
				}
				typeCoreDiagnostic(t, err, tc.want)
			})
		}
	}
}
