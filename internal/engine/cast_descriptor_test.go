package engine

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"stuntgres/internal/sqltype"
)

func TestCastPublishedDescriptors(t *testing.T) {
	e := New()
	run(t, e, "CREATE TABLE cd(i INT4 NOT NULL,n INT8 NOT NULL,s TEXT NOT NULL,b BOOL NOT NULL,ni INT4,nn INT8,ns TEXT,nb BOOL)")
	op, done, err := testOperation(e, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer done()
	base, err := e.Describe(op, statement(t, "SELECT * FROM cd"))
	if err != nil {
		t.Fatal(err)
	}
	origin := base.Results[0].OriginTable
	if origin == 0 {
		t.Fatal("stored column has no physical origin")
	}
	types := []struct {
		name      string
		id        sqltype.ID
		oid       uint32
		converted string
	}{
		{"int4", sqltype.Int4, 23, "n"},
		{"int8", sqltype.Int8, 20, "i"},
		{"text", sqltype.Text, 25, "i"},
		{"bool", sqltype.Bool, 16, "i"},
	}
	targets := make([]string, 0, 32)
	want := make([]ColumnDescriptor, 0, 32)
	for index, typ := range types {
		descriptor, ok := e.CatalogRead().LookupType(typ.id)
		if !ok {
			t.Fatal(typ.id)
		}
		collation := sqltype.CollationRef{}
		if typ.id == sqltype.Text {
			collation = sqltype.CollationRef{ID: 100, Derivation: sqltype.Default}
		}
		usage := sqltype.Usage{Type: descriptor, Typmod: -1, Collation: collation}
		for _, slot := range []int{index, index + 4} {
			column := base.Results[slot].Name
			for syntax, expression := range []string{"CAST(" + column + " AS " + typ.name + ")", column + "::" + typ.name, typ.name + "(" + column + ")"} {
				name := column
				if syntax == 2 {
					name = typ.name
				}
				targets = append(targets, expression)
				want = append(want, ColumnDescriptor{Name: name, Usage: usage, Nullable: slot >= 4, NullabilityKnown: true, OriginTable: origin, OriginAttribute: int16(slot + 1)})
			}
		}
		for _, column := range []string{typ.converted, "n" + typ.converted} {
			for syntax, expression := range []string{"CAST(" + column + " AS " + typ.name + ")", column + "::" + typ.name, typ.name + "(" + column + ")"} {
				name := column
				if syntax == 2 {
					name = typ.name
				}
				targets = append(targets, expression)
				// Genuine conversions, including nullable sources, have unknown
				// expression nullability and no physical origin.
				want = append(want, ColumnDescriptor{Name: name, Usage: usage})
			}
		}
		sourceType, constant := "int4", "2"
		if typ.id == sqltype.Int4 {
			sourceType, constant = "int8", "2::int8"
		}
		targets = append(targets, "(NULL::"+sourceType+")::"+typ.name, "("+constant+")::"+typ.name)
		want = append(want, ColumnDescriptor{Name: typ.name, Usage: usage}, ColumnDescriptor{Name: typ.name, Usage: usage})
		targets = append(targets, "NULL::"+typ.name)
		want = append(want, ColumnDescriptor{Name: typ.name, Usage: usage})
		if usage.Type.ID() != typ.id || uint32(usage.Type.ID()) != typ.oid {
			t.Fatal("fixed baseline type identity", typ, usage)
		}
	}
	ast := statement(t, "SELECT "+strings.Join(targets, ",")+" FROM cd")
	assertSchema := func(t *testing.T, got []ColumnDescriptor) {
		t.Helper()
		if len(got) != len(want) {
			t.Fatalf("descriptor count %d want %d", len(got), len(want))
		}
		for i, expected := range want {
			if got[i] != expected {
				t.Errorf("%s descriptor %#v want %#v", targets[i], got[i], expected)
			}
			if got[i].Usage.Type.ID() != expected.Usage.Type.ID() || uint32(got[i].Usage.Type.ID()) != uint32(expected.Usage.Type.ID()) {
				t.Errorf("%s type ID/OID %d want %d", targets[i], got[i].Usage.Type.ID(), expected.Usage.Type.ID())
			}
		}
	}
	// Hold the original description across both the empty and populated
	// executions, exercising the prepared result-signature contract too.
	held, err := e.Describe(op, ast)
	if err != nil {
		t.Fatal(err)
	}
	assertSchema(t, held.Results)
	for _, populated := range []bool{false, true} {
		if populated {
			run(t, e, "INSERT INTO cd VALUES(2,2,'yes',TRUE,2,2,'yes',TRUE),(0,0,'no',FALSE,NULL,NULL,NULL,NULL)")
		}
		for _, prepared := range []bool{false, true} {
			t.Run(fmt.Sprintf("populated=%t/prepared=%t", populated, prepared), func(t *testing.T) {
				description, err := e.Describe(op, ast)
				if err != nil {
					t.Fatal(err)
				}
				assertSchema(t, description.Results)
				scope, err := e.StartStatement(op)
				if err != nil {
					t.Fatal(err)
				}
				if prepared {
					scope.Expected = &held
				}
				result, executeErr := scope.Execute(ast, 0, nil)
				if finishErr := scope.Finish(executeErr); finishErr != nil {
					t.Fatal(finishErr)
				}
				defer result.Rows.Close()
				assertSchema(t, result.Rows.Schema())
				count := 0
				if populated {
					count = 2
				}
				if got := len(tuples(result)); got != count {
					t.Fatalf("rows %d want %d", got, count)
				}
			})
		}
	}
}

// Execute the same held prepared description with distinct private imported
// frames. Every target is a genuine conversion; the nested TEXT output also
// retains an execution-owned conversion failure rather than failing import.
func TestCastPublishedDescriptorsPreflightReuse(t *testing.T) {
	for _, tc := range []struct {
		target, expression, valid, next string
		id                              sqltype.ID
		oid                             uint32
		firstValue, nextValue           any
	}{
		{"int4", "int4($1::text)", "42", "17", sqltype.Int4, 23, int64(42), int64(17)},
		{"int8", "CAST($1::text AS int8)", "42", "17", sqltype.Int8, 20, int64(42), int64(17)},
		{"text", "$1::text::int4::text", "42", "17", sqltype.Text, 25, "42", "17"},
		{"bool", "bool($1::text)", "yes", "no", sqltype.Bool, 16, true, false},
	} {
		t.Run(tc.target, func(t *testing.T) {
			e := New()
			run(t, e, "CREATE TABLE cd(i INT4)")
			op, done, err := testOperation(e, context.Background())
			if err != nil {
				t.Fatal(err)
			}
			defer done()
			ast := statement(t, "SELECT "+tc.expression+" FROM cd")
			usage := testUsage(e, tc.id)
			usage.Typmod = -1
			usage.Collation = sqltype.CollationRef{}
			if tc.id == sqltype.Text {
				usage.Collation = sqltype.CollationRef{ID: 100, Derivation: sqltype.Default}
			}
			want := []ColumnDescriptor{{Name: tc.target, Usage: usage, Nullable: false, NullabilityKnown: false, OriginTable: 0, OriginAttribute: 0}}
			assertSchema := func(got []ColumnDescriptor) {
				t.Helper()
				if !reflect.DeepEqual(got, want) {
					t.Fatalf("published schema %#v want %#v", got, want)
				}
				if len(got) != 1 || uint32(got[0].Usage.Type.ID()) != tc.oid {
					t.Fatalf("target OID %#v want %d", got, tc.oid)
				}
			}
			held, err := e.Describe(op, ast)
			if err != nil {
				t.Fatal("Prepare/Describe must not evaluate conversions:", err)
			}
			assertSchema(held.Results)
			if len(held.Parameters.Types) != 1 || held.Parameters.Types[0] != testUsage(e, sqltype.Text) {
				t.Fatalf("parameter source %#v want TEXT", held.Parameters)
			}
			snapshot := held
			snapshot.Results = append([]ColumnDescriptor(nil), held.Results...)
			snapshot.Parameters.Types = append([]sqltype.Usage(nil), held.Parameters.Types...)
			for _, populated := range []bool{false, true} {
				if populated {
					run(t, e, "INSERT INTO cd VALUES(1),(2)")
				}
				for _, prepared := range []bool{false, true} {
					t.Run(fmt.Sprintf("populated=%t/prepared=%t", populated, prepared), func(t *testing.T) {
						for _, execution := range []struct {
							name, input string
							null, fails bool
							value       any
						}{
							{"valid", tc.valid, false, false, tc.firstValue},
							{"nil", "", true, false, nil},
							{"failing", "oops", false, true, nil},
							{"valid-again", tc.next, false, false, tc.nextValue},
						} {
							t.Run(execution.name, func(t *testing.T) {
								description, err := e.Describe(op, ast)
								if err != nil {
									t.Fatal(err)
								}
								assertSchema(description.Results)
								scope, err := e.StartStatement(op)
								if err != nil {
									t.Fatal(err)
								}
								if prepared {
									scope.Expected = &held
								}
								result, executeErr := scope.Execute(ast, 1, func(ctx context.Context, parameters ParameterInfo, frame []sqltype.Value, environment sqltype.InputEnvironment) error {
									frame[0] = sqltype.Null(parameters.Types[0].Type)
									if execution.null {
										return nil
									}
									value, err := parameters.Types[0].Type.InputWithEnvironment(ctx, execution.input, -1, environment)
									frame[0] = value
									return err
								})
								finishErr := scope.Finish(executeErr)
								if execution.fails {
									if result.Rows != nil {
										result.Rows.Close()
										t.Fatal("conversion failure exposed a cursor")
									}
									state(t, executeErr, "22P02")
									state(t, finishErr, "22P02")
								} else {
									if executeErr != nil || finishErr != nil || result.Rows == nil {
										t.Fatal(result, executeErr, finishErr)
									}
									defer result.Rows.Close()
									assertSchema(result.Rows.Schema())
									rows := tuples(result)
									count := 0
									if populated {
										count = 2
									}
									if len(rows) != count {
										t.Fatalf("rows %v want count %d", rows, count)
									}
									for _, row := range rows {
										if len(row) != 1 || row[0] != execution.value {
											t.Fatalf("row %v want [%v]", row, execution.value)
										}
									}
								}
								if !reflect.DeepEqual(held, snapshot) {
									t.Fatal("execution mutated held prepared description")
								}
								description, err = e.Describe(op, ast)
								if err != nil {
									t.Fatal(err)
								}
								assertSchema(description.Results)
							})
						}
					})
				}
			}
		})
	}
}
