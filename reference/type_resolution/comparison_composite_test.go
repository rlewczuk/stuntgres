package reference_test

import (
	"errors"
	"testing"

	"stuntgres/reference/fixture"
)

// Released rowtypes.c:record_eq/record_cmp, not row-constructor semantics.
// A reached field's exact identity/provider is checked before its NULL flags;
// an earlier decisive field can avoid later type/provider/arity demand.
func TestReferenceComparisonCompositeDemand(t *testing.T) {
	comparisonCheckpoint(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		for _, q := range []string{
			"CREATE TYPE comparison_pair AS (x integer,y integer)",
			"CREATE TYPE comparison_other AS (x integer,y integer)",
			"CREATE TYPE comparison_wide AS (x integer,y bigint)",
			"CREATE TYPE comparison_short AS (x integer)",
			"CREATE TYPE comparison_poison AS (x integer,j json)",
			"CREATE TYPE comparison_nested AS (p comparison_pair,z integer)",
		} {
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec(q, 0))
		}
		for _, tc := range []struct {
			name, expr string
			want       any
		}{
			{"same_identity", "ROW(1,NULL)::comparison_pair = ROW(1,NULL)::comparison_pair", true},
			{"different_identity", "ROW(1,NULL)::comparison_pair = ROW(1,NULL)::comparison_other", true},
			{"null_fields_order_last", "ROW(1,NULL)::comparison_pair > ROW(1,0)::comparison_pair", true},
			{"whole_null", "NULL::comparison_pair = ROW(1,NULL)::comparison_pair", nil},
			{"both_whole_null", "NULL::comparison_pair < NULL::comparison_other", nil},
			{"nested", "ROW(ROW(1,NULL)::comparison_pair,2)::comparison_nested = ROW(ROW(1,NULL)::comparison_pair,2)::comparison_nested", true},
			{"row_constructor_is_3vl", "ROW(1,NULL::integer) = ROW(1,NULL::integer)", nil},
			{"row_early_decision", "ROW(1,NULL::integer) < ROW(2,0)", true},
			{"record_skips_type", "ROW(1,0)::comparison_pair < ROW(2,0)::comparison_wide", true},
			{"record_skips_count", "ROW(1,0)::comparison_pair < ROW(2)::comparison_short", true},
			{"record_skips_json_eq", "ROW(1,NULL)::comparison_poison = ROW(2,NULL)::comparison_poison", false},
			{"record_skips_json_cmp", "ROW(1,NULL)::comparison_poison < ROW(2,NULL)::comparison_poison", true},
			{"whole_null_skips_json", "NULL::comparison_poison = ROW(1,NULL)::comparison_poison", nil},
			{"nonnull_all_null_is_null", "ROW(NULL,NULL)::comparison_pair IS NULL", true},
			{"nonnull_all_null_not_distinct_raw_null", "ROW(NULL,NULL)::comparison_pair IS NOT DISTINCT FROM NULL", false},
			{"nonnull_all_null_distinct_raw_null", "ROW(NULL,NULL)::comparison_pair IS DISTINCT FROM NULL", true},
			{"mixed_is_null", "ROW(1,NULL)::comparison_pair IS NULL", false},
			{"mixed_is_not_null", "ROW(1,NULL)::comparison_pair IS NOT NULL", false},
			{"typed_null_distinct", "ROW(NULL,NULL)::comparison_pair IS DISTINCT FROM NULL::comparison_pair", true},
			{"composite_array", "ARRAY[ROW(1,NULL)::comparison_pair] = ARRAY[ROW(1,NULL)::comparison_pair]", true},
			{"composite_array_order", "ARRAY[ROW(1,NULL)::comparison_pair] > ARRAY[ROW(1,0)::comparison_pair]", true},
		} {
			t.Run(tc.name, func(t *testing.T) {
				comparisonCheckpointBool(t, p, b, tc.expr, tc.want)
			})
		}
		for _, tc := range []struct{ name, expr, code, message string }{
			{"reached_type", "ROW(1,0)::comparison_pair = ROW(1,0)::comparison_wide", "42804", "cannot compare dissimilar column types integer and bigint at record column 2"},
			{"reached_type_null", "ROW(1,NULL)::comparison_pair < ROW(1,NULL)::comparison_wide", "42804", "cannot compare dissimilar column types integer and bigint at record column 2"},
			{"reached_count", "ROW(1,0)::comparison_pair = ROW(1)::comparison_short", "42804", "cannot compare record types with different numbers of columns"},
			{"reached_json_eq", "ROW(1,NULL)::comparison_poison = ROW(1,NULL)::comparison_poison", "42883", "could not identify an equality operator for type json"},
			{"reached_json_cmp", "ROW(1,NULL)::comparison_poison < ROW(1,NULL)::comparison_poison", "42883", "could not identify a comparison function for type json"},
		} {
			t.Run(tc.name, func(t *testing.T) {
				s := fixture.ImplicitError("SELECT "+tc.expr+" AS v", fixture.Diagnostic{Code: tc.code, Message: tc.message})
				s.SelectRows, s.ErrorStage = true, "execute"
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Prepared(s))
			})
		}
	})
}

// Released parse_expr.c:make_row_comparison_op permits scalar operators on
// one-field rows but requires B-tree interpretations for two or more fields.
// make_row_distinct_op instead needs '=' alone (CID/ACLITEM equality suffices).
func TestReferenceComparisonRowEligibility(t *testing.T) {
	comparisonCheckpoint(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		for _, tc := range []struct {
			name, expr string
			want       any
		}{
			{"cid_one_field", "ROW('0'::cid) = ROW('0'::cid)", true},
			{"xid_one_field", "ROW('0'::xid) <> ROW('1'::xid)", true},
			{"cid_distinct", "ROW('0'::cid,1) IS DISTINCT FROM ROW('0'::cid,1)", false},
			{"cid_not_distinct", "ROW('0'::cid,NULL::int) IS NOT DISTINCT FROM ROW('0'::cid,NULL::int)", true},
			{"numeric_field_coercion", "ROW(1::int2,2::int4) = ROW(1::int8,2::numeric)", true},
			{"ordering_stops_at_first", "ROW(1,NULL::int) < ROW(2,0)", true},
			{"ordering_stops_at_null", "ROW(NULL::int,1) < ROW(NULL::int,2)", nil},
			{"equality_false_dominates_null", "ROW(NULL::int,1) = ROW(NULL::int,2)", false},
		} {
			t.Run(tc.name, func(t *testing.T) { comparisonCheckpointBool(t, p, b, tc.expr, tc.want) })
		}
		for _, tc := range []struct{ name, expr, op string }{
			{"cid_equality", "ROW('0'::cid,1) = ROW('0'::cid,1)", "="},
			{"xid_equality", "ROW('0'::xid,1) = ROW('0'::xid,1)", "="},
			{"xid_inequality", "ROW('0'::xid,1) <> ROW('1'::xid,1)", "<>"},
		} {
			t.Run(tc.name, func(t *testing.T) {
				q := "/*é*/ SELECT " + tc.expr
				comparisonCheckpointAnalysis(t, p, b, q, fixture.Diagnostic{
					Code: "0A000", Message: "could not determine interpretation of row comparison operator " + tc.op,
					Hint:     "Row comparison operators must be associated with btree operator families.",
					Position: comparisonCheckpointPosition(q, tc.op+" ROW"),
				})
			})
		}
	})
}

// PostgreSQL's physical record image operators are not logical composite
// comparison. Identical int4 field images give these exact PostgreSQL results;
// no Go-memory/text fallback can supply the missing native physical mechanism.
// Root's public native probe observed an ordinary Go error, with no SQLSTATE.
// Assert that boundary honestly rather than manufacturing a SQL diagnostic.
func TestReferenceComparisonRecordImageBoundary(t *testing.T) {
	comparisonCheckpoint(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE TYPE comparison_image AS (x integer)", 0))
		for i, op := range []string{"*=", "*<>", "*<", "*<=", "*>", "*>="} {
			t.Run(op, func(t *testing.T) {
				want := []bool{true, false, false, true, false, true}
				expr := "ROW(1)::comparison_image " + op + " ROW(1)::comparison_image"
				if !b.PG {
					_, err := b.DB.QueryContext(p.Ctx, "SELECT "+expr+" AS v")
					if err == nil || err.Error() != "stuntgres: invalid comparison operator" {
						t.Fatalf("physical image boundary: got %v", err)
					}
					var state interface{ SQLState() string }
					if errors.As(err, &state) {
						t.Fatalf("physical image error unexpectedly has SQLSTATE %s", state.SQLState())
					}
					return
				}
				comparisonCheckpointBool(t, p, b, expr, want[i])
			})
		}
	})
}
