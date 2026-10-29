package proc

import (
	"errors"
	"testing"

	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func TestPseudoSignatureSafetyBoundary(t *testing.T) {
	u := pseudoUsages()
	for _, tc := range []struct {
		name    string
		inputs  []sqltype.Usage
		outputs []sqltype.Usage
		message string
	}{
		{"cstring_result", nil, []sqltype.Usage{u[2275]}, "PL/pgSQL functions cannot return type cstring"},
		{"cstring_argument", []sqltype.Usage{u[2275]}, []sqltype.Usage{u[sqltype.Int4]}, "PL/pgSQL functions cannot accept type cstring"},
		{"internal_with_input", []sqltype.Usage{u[2281]}, []sqltype.Usage{u[2281]}, "PL/pgSQL functions cannot return type internal"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := ValidatePseudoResultSafety(tc.inputs, tc.outputs); err != nil {
				t.Fatal("catalog declaration safety ran a language validator", err)
			}
			formals := make([]Formal, len(tc.inputs))
			for i, usage := range tc.inputs {
				formals[i] = Formal{Mode: "i", Usage: usage}
			}
			err := ValidatePLSignature(tc.inputs, tc.outputs, formals, "signature_boundary", false)
			var diagnostic *sqlstate.Error
			if !errors.As(err, &diagnostic) || diagnostic.Code != "0A000" || diagnostic.Message != tc.message || diagnostic.Detail != "" || diagnostic.Position != "" || diagnostic.Context != "" {
				t.Fatal("explicit PL validator lost signature restriction", err)
			}
		})
	}
	for _, tc := range []struct {
		result          sqltype.ID
		message, detail string
	}{
		{2283, "cannot determine result data type", "A result of type anyelement requires at least one input of type anyelement, anyarray, anynonarray, anyenum, anyrange, or anymultirange."},
		{2281, `unsafe use of pseudo-type "internal"`, "A result of type internal requires at least one input of type internal."},
	} {
		err := ValidatePseudoResultSafety(nil, []sqltype.Usage{u[tc.result]})
		var diagnostic *sqlstate.Error
		if !errors.As(err, &diagnostic) || diagnostic.Code != "42P13" || diagnostic.Message != tc.message || diagnostic.Detail != tc.detail {
			t.Fatal("unconditional catalog safety was weakened", err)
		}
		layout := &Definition{Result: u[tc.result]}
		if err := layout.SetFormals([]Formal{{Mode: "o", Name: "v", Usage: u[tc.result]}}); err != nil {
			t.Fatal(err)
		}
		err = ValidatePseudoResultSafety(layout.Inputs, []sqltype.Usage{layout.Formals[layout.OutputFormals[0]].Usage})
		if !errors.As(err, &diagnostic) || diagnostic.Code != "42P13" || diagnostic.Message != tc.message || diagnostic.Detail != tc.detail {
			t.Fatal("OUT-only formal incorrectly supplied catalog safety input", err)
		}
	}
	for _, tc := range []struct {
		result, argument    sqltype.ID
		code, message, hint string
	}{
		{sqltype.Int4, 4538, "0A000", "PL/pgSQL functions cannot accept type anycompatiblemultirange", ""},
		{2279, sqltype.Int4, "42P13", "trigger functions cannot have declared arguments", "The arguments of the trigger can be accessed through TG_NARGS and TG_ARGV instead."},
		{3838, sqltype.Int4, "42P13", "event trigger functions cannot have declared arguments", ""},
	} {
		err := ValidatePLSignature([]sqltype.Usage{u[tc.argument]}, []sqltype.Usage{u[tc.result]}, []Formal{{Mode: "i", Usage: u[tc.argument]}}, "signature_boundary", false)
		var diagnostic *sqlstate.Error
		if !errors.As(err, &diagnostic) || diagnostic.Code != tc.code || diagnostic.Message != tc.message || diagnostic.Hint != tc.hint || diagnostic.Context != `compilation of PL/pgSQL function "signature_boundary" near line 1` {
			t.Fatal("genuine PL signature checks lost compiler restrictions", err)
		}
	}
	if err := ValidatePseudoSignature([]sqltype.Usage{u[sqltype.Int4]}, []sqltype.Usage{u[sqltype.Int4]}, []Formal{{Mode: "i", Usage: u[sqltype.Int4]}}, PLpgSQLBody, false); err != nil {
		t.Fatal("ordinary checked signature acceptance changed", err)
	}
}
