package reference_test

import (
	"database/sql"
	"fmt"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

func jsonRecordScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			for _, q := range []string{
				"CREATE TYPE jr_pair AS (a integer, b text)",
				"CREATE TYPE jr_sub AS (d integer, e text)",
				"CREATE TYPE jr_outer AS (a integer, b text[], c jr_sub)",
				"CREATE TYPE jr_empty AS ()",
			} {
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(q, 0))
			}
			body(t, p, b, c)
		})
	}
}

func jsonRecordBoth(t *testing.T, p *fixture.Pair, b fixture.Backend, c fixture.Executor, s fixture.Step) {
	t.Helper()
	s.RawTypes = append([]string(nil), s.Types...)
	for i, typ := range s.RawTypes {
		if typ == "" {
			s.RawTypes[i] = "RECORD"
		}
	}
	fixture.JSONEachBoth(t, p, b, c, s)
}

func jsonRecordPair(q string, rows [][]any, args ...any) fixture.Step {
	return fixture.Select(q, []string{"a", "b"}, []string{"INT4", "TEXT"}, rows, args...)
}

// Complete records and preparation stages were pinned by the root's PostgreSQL
// 18.0 discovery runs. Direct Query failures use the fixture's execute stage.
func jsonRecordErrorBoth(t *testing.T, p *fixture.Pair, b fixture.Backend, c fixture.Executor, q string, want fixture.Diagnostic, analysis bool, args ...any) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprintf("prepared=%t", prepared), func(t *testing.T) {
			stage := "execute"
			if prepared && analysis {
				stage = "prepare"
			}
			fixture.JSONKeysError(t, p, b, c, q, prepared, want, stage, args...)
		})
	}
}

func jsonRecordDiagnostic(code, message string, position int) fixture.Diagnostic {
	d := fixture.ControlsReferenceDiagnostic(code, message)
	d.Position = fixture.Position(position)
	return d
}

func jsonRecordMissingShape(function string) fixture.Diagnostic {
	d := jsonRecordDiagnostic("0A000", "could not determine row type for result of "+function, 0)
	d.Hint = "Provide a non-null record argument, or call the function in the FROM clause using a column definition list."
	return d
}

func jsonRecordSignature(signature string, position int) fixture.Diagnostic {
	d := jsonRecordDiagnostic("42883", "function "+signature+" does not exist", position)
	d.Hint = "No function matches the given name and argument types. You might need to add explicit type casts."
	return d
}

func jsonRecordDomain(code, domain, constraint string) fixture.Diagnostic {
	message := "domain " + domain + " does not allow null values"
	if constraint != "" {
		message = fmt.Sprintf("value for domain %s violates check constraint %q", domain, constraint)
	}
	d := jsonRecordDiagnostic(code, message, 0)
	d.Schema, d.DataType, d.Constraint = "public", domain, constraint
	return d
}

// Every public metadata field is checked before Next, including empty streams.
// Most declarations use VARCHAR(3) and NUMERIC(5,2). The optional length pins
// other declared VARCHAR widths; negative means the unconstrained adapter case.
func jsonRecordMetadata(s fixture.Step, pg bool, varcharLength ...int64) fixture.Step {
	s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
		meta, err := rows.ColumnTypes()
		if err != nil || len(meta) != len(s.Types) {
			t.Fatalf("metadata=%v error=%v want width=%d", meta, err, len(s.Types))
		}
		for i, ct := range meta {
			typ := s.Types[i]
			scan := reflect.TypeFor[string]()
			switch typ {
			case "INT4":
				scan = reflect.TypeFor[int64]()
				if pg {
					scan = reflect.TypeFor[int32]()
				}
			case "INT8":
				scan = reflect.TypeFor[int64]()
			case "", "RECORD", "JSON", "JSONB", "_INT4", "NUMERIC":
				if pg {
					scan = reflect.TypeFor[any]()
				}
			}
			length, lengthKnown := int64(0), false
			switch typ {
			case "TEXT":
				length, lengthKnown = 9223372036854775807, true
			case "VARCHAR":
				length, lengthKnown = 3, true
				if len(varcharLength) != 0 {
					length = varcharLength[0]
					if length < 0 {
						length = 9223372036854775807
						if pg {
							length = -5
						}
					}
				}
			}
			precision, scale, decimalKnown := int64(0), int64(0), false
			if typ == "NUMERIC" {
				precision, scale, decimalKnown = 5, 2, true
			}
			if ct.Name() != s.Columns[i] || ct.DatabaseTypeName() != typ || ct.ScanType() != scan {
				t.Errorf("column%d=%q/%q/%v want=%q/%q/%v", i, ct.Name(), ct.DatabaseTypeName(), ct.ScanType(), s.Columns[i], typ, scan)
			}
			if n, k := ct.Length(); n != length || k != lengthKnown {
				t.Errorf("column%d length=%d/%t want=%d/%t", i, n, k, length, lengthKnown)
			}
			nullableKnown := !pg && typ == "INT8" && s.Columns[i] == "count"
			if n, k := ct.Nullable(); n || k != nullableKnown {
				t.Errorf("column%d nullable=%t/%t want=false/%t", i, n, k, nullableKnown)
			}
			if p, s, k := ct.DecimalSize(); p != precision || s != scale || k != decimalKnown {
				t.Errorf("column%d decimal=%d/%d/%t want=%d/%d/%t", i, p, s, k, precision, scale, decimalKnown)
			}
		}
	}
	return s
}
