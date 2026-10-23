package reference_test

import (
	"database/sql"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePLpgSQLAssignmentCardinality(t *testing.T) {
	for _, tc := range []struct {
		name, declaration, target, rhs string
		lo, hi                         int
		want                           any
		code, message, domain          string
	}{
		{"empty_element", "integer[]", "[1]", "generate_series(lo,hi)", 1, 0, nil, "", "", ""},
		{"actual_null_element", "integer[]", "[1]", "x", 1, 1, "{NULL,2}", "", "", ""},
		{"not_null_root_empty", "integer[] NOT NULL", "[1]", "generate_series(lo,hi)", 1, 0, nil, "22004", `null value cannot be assigned to variable "a" declared NOT NULL`, ""},
		{"not_null_root_actual_null", "integer[] NOT NULL", "[1]", "x", 1, 1, "{NULL,2}", "", "", ""},
		// The empty projection carries the root domain's identity. It is not
		// fresh base input to a domain or a NULL element conversion.
		{"root_domain_not_null_empty", "pa_card_root_nn", "[1]", "generate_series(lo,hi)", 1, 0, nil, "", "", ""},
		{"root_domain_check_empty", "pa_card_root_check", "[1]", "generate_series(lo,hi)", 1, 0, nil, "", "", ""},
		{"element_domain_empty", "pa_card_element_nn[]", "[1]", "generate_series(lo,hi)", 1, 0, nil, "", "", ""},
		{"element_domain_actual_null", "pa_card_element_nn[]", "[1]", "x", 1, 1, nil, "23502", "domain pa_card_element_nn does not allow null values", "pa_card_element_nn"},
		{"multiple_rows", "integer[]", "[1]", "generate_series(lo,hi)", 1, 2, nil, "21000", "query returned more than one row", ""},
		{"empty_null_bound", "integer[]", "[NULL::integer]", "generate_series(lo,hi)", 1, 0, nil, "", "", ""},
		{"one_row_null_bound", "integer[]", "[NULL::integer]", "generate_series(lo,hi)", 1, 1, nil, "22004", "array subscript in assignment must not be null", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				userOperatorExec(t, p, b, c,
					"CREATE DOMAIN pa_card_root_nn AS integer[] NOT NULL",
					"CREATE DOMAIN pa_card_root_check AS integer[] CONSTRAINT pa_card_nonnull CHECK(VALUE IS NOT NULL)",
					"CREATE DOMAIN pa_card_element_nn AS integer NOT NULL",
					"CREATE TABLE pa_card_input(a integer[])",
					"CREATE FUNCTION pa_cardinality_case(input integer[],lo integer,hi integer,x integer) RETURNS text LANGUAGE plpgsql AS $$DECLARE a "+tc.declaration+" := input; BEGIN a"+tc.target+" := "+tc.rhs+"; RETURN a::text; END$$")
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO pa_card_input VALUES ('{1,2}')", 1))
				wantError := plpgsqlRuntimeDiagnostic(tc.code, tc.message, "pa_cardinality_case(integer[],integer,integer,integer)", "assignment")
				if tc.domain != "" {
					wantError.Schema, wantError.DataType = "public", tc.domain
				}
				switch tc.name {
				case "multiple_rows":
					wantError.Context = "query: a[1] := generate_series(lo,hi)\n" + wantError.Context
				case "one_row_null_bound":
					wantError.Context = "PL/pgSQL assignment \"a[NULL::integer] := generate_series(lo,hi)\"\n" + wantError.Context
				}
				query := fmt.Sprintf("SELECT pa_cardinality_case(a,%d,%d,NULL) AS v FROM pa_card_input", tc.lo, tc.hi)
				if tc.code == "" {
					plpgsqlValue(t, p, b, c, query, "TEXT", tc.want)
				} else {
					plpgsqlError(t, p, c, query, true, wantError)
				}
				plpgsqlValue(t, p, b, c, "SELECT a::text AS v FROM pa_card_input", "TEXT", "{1,2}")

				q := "SELECT pa_cardinality_case($1::integer[],$2::integer,$3::integer,$4::integer) AS v"
				held, err := c.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				defer held.Close()
				executor := fixture.HeldStatement{Executor: c, Statement: held, SQL: q}
				// The NULL-bound function cannot have a successful nonempty
				// invocation; its held recovery is the empty result instead.
				if tc.target != "[NULL::integer]" {
					fixture.RunStep(t, p.Ctx, executor, b.Recorder, fixture.Select(q, []string{"v"}, []string{"TEXT"}, [][]any{{"{7,5}"}}, "{4,5}", 7, 7, 7))
				}
				if tc.code == "" {
					fixture.RunStep(t, p.Ctx, executor, b.Recorder, fixture.Select(q, []string{"v"}, []string{"TEXT"}, [][]any{{tc.want}}, "{1,2}", tc.lo, tc.hi, nil))
				} else {
					s := fixture.ErrorDiagnostic(q, wantError, "{1,2}", tc.lo, tc.hi, nil)
					s.SelectRows, s.ErrorStage = true, "execute"
					fixture.RunStep(t, p.Ctx, executor, b.Recorder, s)
				}
				if tc.target == "[NULL::integer]" {
					fixture.RunStep(t, p.Ctx, executor, b.Recorder, fixture.Select(q, []string{"v"}, []string{"TEXT"}, [][]any{{nil}}, "{8,9}", 1, 0, 4))
				} else {
					fixture.RunStep(t, p.Ctx, executor, b.Recorder, fixture.Select(q, []string{"v"}, []string{"TEXT"}, [][]any{{"{4,9}"}}, "{8,9}", 4, 4, 4))
				}
				plpgsqlValue(t, p, b, c, "SELECT a::text AS v FROM pa_card_input", "TEXT", "{1,2}")
			})
		})
	}
	t.Run("set_projection_bound_order", plpgsqlCardinalityBoundOrder)
	t.Run("field_datums", plpgsqlCardinalityFields)
}

func plpgsqlCardinalityBoundOrder(t *testing.T) {
	for _, tc := range []struct {
		name string
		hi   int
		want any
		last int64
		code string
	}{
		{"empty", 4, nil, 1, ""},
		{"one_row", 5, "{1,7}", 2, ""},
		{"multiple_rows", 6, nil, 3, "21000"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				userOperatorExec(t, p, b, c,
					"CREATE SEQUENCE pa_card_bound_seq",
					"CREATE SEQUENCE pa_card_rhs_seq",
					"CREATE FUNCTION pa_cardinality_order(lo integer,hi integer) RETURNS text LANGUAGE plpgsql AS $$DECLARE a integer[] := '{1,2}'; BEGIN a[nextval('pa_card_bound_seq')::integer] := generate_series(lo,hi) + nextval('pa_card_rhs_seq')::integer; RETURN a::text; END$$")
				for _, prepared := range []bool{false, true} {
					plpgsqlValue(t, p, b, c, "SELECT setval('pa_card_bound_seq',1,true) AS v", "INT8", int64(1))
					plpgsqlValue(t, p, b, c, "SELECT setval('pa_card_rhs_seq',1,true) AS v", "INT8", int64(1))
					q := fmt.Sprintf("SELECT pa_cardinality_order(5,%d) AS v", tc.hi)
					s := fixture.Select(q, []string{"v"}, []string{"TEXT"}, [][]any{{tc.want}})
					if tc.code != "" {
						want := plpgsqlRuntimeDiagnostic(tc.code, "query returned more than one row", "pa_cardinality_order(integer,integer)", "assignment")
						want.Context = "query: a[nextval('pa_card_bound_seq')::integer] := generate_series(lo,hi) + nextval('pa_card_rhs_seq')::integer\n" + want.Context
						s = fixture.ErrorDiagnostic(q, want)
						s.SelectRows, s.ErrorStage = true, "execute"
					}
					if prepared {
						s = fixture.Prepared(s)
					}
					fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
					plpgsqlValue(t, p, b, c, "SELECT currval('pa_card_bound_seq') AS v", "INT8", tc.last)
					plpgsqlValue(t, p, b, c, "SELECT currval('pa_card_rhs_seq') AS v", "INT8", tc.last)
				}
			})
		})
	}
}

func plpgsqlCardinalityFields(t *testing.T) {
	for _, target := range []struct {
		name, declaration, initial, path string
		array                            bool
	}{
		{"record_scalar", "record", "ROW(7,9)", "r.f1", false},
		{"record_array", "record", "ROW('{1,2}'::integer[],9)", "r.f1[1]", true},
		{"named_scalar", "pa_card_scalar_row", "ROW(7,9)", "r.f", false},
		{"named_array", "pa_card_array_row", "ROW('{1,2}'::integer[],9)", "r.a[1]", true},
	} {
		for _, empty := range []bool{true, false} {
			name, rhs, hi, want := "empty", "generate_series(lo,hi)", 0, "(,9)"
			if !empty {
				name, rhs, hi = "actual_null", "x", 1
				if target.array {
					want = "(\"{NULL,2}\",9)"
				}
			}
			t.Run(target.name+"_"+name, func(t *testing.T) {
				plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
					userOperatorExec(t, p, b, c,
						"CREATE TYPE pa_card_scalar_row AS (f integer,sibling integer)",
						"CREATE TYPE pa_card_array_row AS (a integer[],sibling integer)",
						"CREATE FUNCTION pa_cardinality_field(lo integer,hi integer,x integer) RETURNS text LANGUAGE plpgsql AS $$DECLARE r "+target.declaration+"; BEGIN r := "+target.initial+"; "+target.path+" := "+rhs+"; RETURN r::text; END$$")
					query := fmt.Sprintf("SELECT pa_cardinality_field(1,%d,NULL) AS v", hi)
					plpgsqlValue(t, p, b, c, query, "TEXT", want)
					q := "SELECT pa_cardinality_field($1::integer,$2::integer,$3::integer) AS v"
					held, err := c.PrepareContext(p.Ctx, q)
					if err != nil {
						t.Fatal(err)
					}
					defer held.Close()
					executor := fixture.HeldStatement{Executor: c, Statement: held, SQL: q}
					before, after := "(7,9)", "(4,9)"
					if target.array {
						before, after = "(\"{7,2}\",9)", "(\"{4,2}\",9)"
					}
					fixture.RunStep(t, p.Ctx, executor, b.Recorder, fixture.Select(q, []string{"v"}, []string{"TEXT"}, [][]any{{before}}, 7, 7, 7))
					fixture.RunStep(t, p.Ctx, executor, b.Recorder, fixture.Select(q, []string{"v"}, []string{"TEXT"}, [][]any{{want}}, 1, hi, nil))
					fixture.RunStep(t, p.Ctx, executor, b.Recorder, fixture.Select(q, []string{"v"}, []string{"TEXT"}, [][]any{{after}}, 4, 4, 4))
				})
			})
		}
	}
}
