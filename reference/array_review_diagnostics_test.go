package reference_test

import (
	"database/sql"
	"testing"
)

// Expectations come from REL_18_0 arrayfuncs.c ReadArrayStr: opening
// braces check frozen rank before input; closing braces check that level's
// extent; scalar input runs before checking the element's nesting depth.
// https://raw.githubusercontent.com/postgres/postgres/REL_18_0/src/backend/utils/adt/arrayfuncs.c
func TestReferenceArrayStructuralDiagnostics(t *testing.T) {
	for _, tc := range []struct {
		name, input, detail string
		elementError        bool
	}{
		{"explicit_ragged", "[1:2][1:1]={{1},{2,3}}", "Specified array dimensions do not match array contents.", false},
		{"explicit_rank_before_input", "[1:1]={{oops}}", "Specified array dimensions do not match array contents.", false},
		{"inferred_rank_before_input", "{{1},{{oops}}}", "Multidimensional arrays must have sub-arrays with matching dimensions.", false},
		{"explicit_extent_before_input", "[1:2][1:1]={{1,2},{oops}}", "Specified array dimensions do not match array contents.", false},
		{"inferred_extent_before_input", "{{1},{2,3},{oops}}", "Multidimensional arrays must have sub-arrays with matching dimensions.", false},
		{"null_freezes_inferred_rank", "{{NULL},{{oops}}}", "Multidimensional arrays must have sub-arrays with matching dimensions.", false},
		{"explicit_short_extent_before_input", "[1:2][1:2]={{1},{oops,2}}", "Specified array dimensions do not match array contents.", false},
		{"explicit_empty_extent_before_input", "[1:2][1:1]={{},{oops}}", "Specified array dimensions do not match array contents.", false},
		{"explicit_shallow_element", "[1:1][1:1]={1}", "Specified array dimensions do not match array contents.", false},
		{"inferred_shallow_element", "{{1},2}", "Multidimensional arrays must have sub-arrays with matching dimensions.", false},
		{"explicit_input_before_extent", "[1:2][1:1]={{1,oops},{2}}", "", true},
		{"inferred_input_before_extent", "{{1},{oops,2}}", "", true},
		{"explicit_input_before_shallow_rank", "[1:1][1:1]={oops}", "", true},
		{"inferred_input_before_shallow_rank", "{{1},oops}", "", true},
		{"explicit_input_before_outer_extent", "[1:1][1:1]={{1},{oops}}", "", true},
		{"empty_does_not_freeze_rank_before_input", "{{},{{oops}}}", "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			arrayReferenceScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
				want := controlsReferenceDiagnostic("22P02", `malformed array literal: "`+tc.input+`"`)
				want.Detail = tc.detail
				if tc.elementError {
					want.Message = `invalid input syntax for type integer: "oops"`
				}
				// Query literal and direct bind, Prepare literal and prepared bind.
				// The shared helper independently requires the entire diagnostic,
				// phase, no returned cursor/handle, and portal Context only at bind.
				arrayReferenceInputError(t, p, b, c, tc.input, "int4[]", want)
				for _, prepared := range []bool{false, true} {
					literal := want
					literal.Position = "14"
					s := referenceErrorDiagnostic("/*é*/ SELECT "+arrayReferenceQuote(tc.input)+"::int4[] AS a FROM public.one", literal)
					s.selectRows, s.prepared, s.errorStage = true, prepared, "execute"
					if prepared {
						s.errorStage = "prepare"
					}
					referenceRunStep(t, p.ctx, c, b.recorder, s)
				}
				referenceRunStep(t, p.ctx, c, b.recorder, referenceExec("DELETE FROM public.one", 1))
				literal := want
				literal.Position = "8"
				s := referenceErrorDiagnostic("SELECT "+arrayReferenceQuote(tc.input)+"::int4[] AS a FROM public.one", literal)
				s.selectRows, s.prepared, s.errorStage = true, true, "prepare"
				referenceRunStep(t, p.ctx, c, b.recorder, s)
			})
		})
	}
}

// Both array_in (750) and array_out (751) are STABLE, independently of
// their scalar element's volatility. Physical domain wrappers do not turn
// those IO calls into immutable casts.
// https://raw.githubusercontent.com/postgres/postgres/REL_18_0/src/include/catalog/pg_proc.dat
// heap.c cookDefault rejects mutable generation expressions with 42P17,
// no position/detail/context. CREATE TABLE defers this validation to execute
// even when the statement has first been prepared.
func TestReferenceArrayGeneratedIOVolatility(t *testing.T) {
	for _, family := range []string{
		"bool", "bytea", "name", "int2", "int4", "int8", "text", "oid",
		"bpchar", "varchar", "float4", "float8", "numeric", "date", "time",
		"timetz", "timestamp", "timestamptz", "interval", "uuid", "regclass",
	} {
		t.Run(family, func(t *testing.T) {
			arrayReferenceScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
				run := func(s referenceStep) { referenceRunStep(t, p.ctx, c, b.recorder, s) }
				run(referenceExec("CREATE DOMAIN array_io_element AS "+family, 0))
				baseArray := family + "[]"
				if family == "regclass" {
					// Keep the existing supported REGCLASS-domain boundary;
					// this witness does not introduce bare REGCLASS storage.
					baseArray = "array_io_element[]"
				}
				run(referenceExec("CREATE DOMAIN array_io_whole AS "+baseArray, 0))
				run(referenceExec("CREATE DOMAIN array_io_chain AS array_io_whole", 0))
				run(referenceExec("CREATE DOMAIN array_io_nested AS array_io_element[]", 0))
				for _, typ := range []string{baseArray, "array_io_element[]", "array_io_whole", "array_io_chain", "array_io_nested", "array_io_whole[]"} {
					for _, input := range []bool{false, true} {
						for _, prepared := range []bool{false, true} {
							q := "CREATE TABLE array_io_rejected(a " + typ + ",b TEXT GENERATED ALWAYS AS(a::TEXT) STORED)"
							if input {
								q = "CREATE TABLE array_io_rejected(a TEXT,b " + typ + " GENERATED ALWAYS AS(a::" + typ + ") STORED)"
							}
							s := referenceErrorDiagnostic(q, controlsReferenceDiagnostic("42P17", "generation expression is not immutable"))
							s.prepared, s.errorStage = prepared, "execute"
							run(s)
							missing := controlsReferenceDiagnostic("42P01", `relation "array_io_rejected" does not exist`)
							missing.Position = "15"
							s = referenceErrorDiagnostic("SELECT * FROM array_io_rejected", missing)
							s.selectRows, s.errorStage = true, "execute"
							run(s)
							// Failed DDL must not publish even an empty relation. Reusing
							// its name also catches publication without catalog projections.
							run(referenceExec("CREATE TABLE array_io_rejected(a INT)", 0))
							run(referenceExec("INSERT INTO array_io_rejected VALUES(7)", 1))
							run(referenceSelect("SELECT a FROM array_io_rejected", []string{"a"}, []string{"INT4"}, [][]any{{int64(7)}}))
							run(referenceExec("DROP TABLE array_io_rejected", 0))
						}
					}
				}
			})
		})
	}
}
