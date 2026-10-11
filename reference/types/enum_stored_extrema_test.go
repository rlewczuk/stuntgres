package reference_test

import (
	"bytes"
	"database/sql"
	"reflect"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

// Independent expectations from root tests-17, pinned PostgreSQL 18.0:
// declaration order z,a (not lexical order), ignored NULL operands, domain
// identity wrapped in defaults and views, and exact bad-label diagnostics.
func TestReferenceEnumStoredExtrema(t *testing.T) {
	fixture.EnumReferenceScenario(t, func(t *testing.T, p *fixture.Pair, backend fixture.Backend, conn *sql.Conn) {
		for _, query := range []string{
			"CREATE TYPE mood AS ENUM ('z','a')",
			"CREATE DOMAIN wrapped AS mood",
		} {
			fixture.RunStep(t, p.Ctx, conn, backend.Recorder, fixture.Exec(query, 0))
		}
		var restored []fixture.Step
		for _, prepared := range []bool{false, true} {
			route := "direct"
			if prepared {
				route = "prepared"
			}
			for _, sample := range []struct {
				name, expression string
				value, hi, lo    any
				directPosition   string
				preparedPosition string
			}{
				{"greatest", "GREATEST('z'::wrapped,'a'::wrapped)", "a", "a", "a", "27", "45"},
				{"least", "LEAST('z'::wrapped,'a'::wrapped)", "z", "z", "z", "40", "42"},
				{"null_operand", "GREATEST(NULL::wrapped,'z'::wrapped,'a'::wrapped)", "a", "a", "a", "47", "49"},
				{"all_null", "LEAST(NULL::wrapped,NULL::wrapped)", nil, "z", "a", "43", "45"},
			} {
				t.Run(route+"/"+sample.name, func(t *testing.T) {
					name := "saved_" + route + "_" + sample.name
					if route == "direct" && sample.name == "greatest" {
						name = "saved"
					}
					run := func(step fixture.Step) {
						step.Prepared = prepared
						fixture.EnumReferenceRunStep(t, p.Ctx, conn, backend.Recorder, step)
					}
					run(fixture.Exec("CREATE TABLE "+name+"(v wrapped DEFAULT "+sample.expression+")", 0))
					run(fixture.Exec("INSERT INTO "+name+" DEFAULT VALUES", 1))
					run(fixture.Exec("CREATE TABLE "+name+"_checked(v wrapped DEFAULT "+sample.expression+", CHECK (v::mood >= LEAST('z'::wrapped,'a'::wrapped)::mood))", 0))
					run(fixture.Exec("INSERT INTO "+name+"_checked DEFAULT VALUES", 1))
					for _, table := range []string{name, name + "_checked"} {
						step := fixture.Select("SELECT v FROM "+table, []string{"v"}, []string{""}, [][]any{{sample.value}})
						step.CheckMetadata = fixture.EnumReferenceMetadata(backend.PG, true)
						run(step)
						restored = append(restored,
							fixture.Exec("DELETE FROM "+table, 1),
							fixture.Exec("INSERT INTO "+table+" DEFAULT VALUES", 1),
							step,
						)
					}
					run(fixture.Exec("CREATE VIEW "+name+"_view AS SELECT GREATEST(v,'z'::wrapped) AS hi, LEAST(v,'a'::wrapped) AS lo FROM "+name, 0))
					view := fixture.Select("SELECT hi,lo FROM "+name+"_view", []string{"hi", "lo"}, []string{"", ""}, [][]any{{sample.hi, sample.lo}})
					view.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
						types, err := rows.ColumnTypes()
						if err != nil {
							t.Fatal(err)
						}
						for _, typ := range types {
							length, lengthKnown := typ.Length()
							precision, scale, decimalKnown := typ.DecimalSize()
							scan := reflect.TypeFor[string]()
							if backend.PG {
								scan = reflect.TypeFor[any]()
								nullable, known := typ.Nullable()
								if nullable || known {
									t.Fatalf("pinned enum view nullable=%t/%t, want false/false", nullable, known)
								}
							}
							if typ.DatabaseTypeName() != "" || typ.ScanType() != scan || length != 0 || lengthKnown || precision != 0 || scale != 0 || decimalKnown {
								t.Fatalf("enum view metadata type=%q scan=%v length=%d/%t decimal=%d,%d/%t", typ.DatabaseTypeName(), typ.ScanType(), length, lengthKnown, precision, scale, decimalKnown)
							}
						}
					}
					run(view)
					restored = append(restored, view)
					position, stage := sample.directPosition, "execute"
					if prepared {
						position, stage = sample.preparedPosition, "prepare"
					}
					bad := fixture.ErrorDiagnostic("INSERT INTO "+name+" VALUES ('nope')", fixture.Diagnostic{
						Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22P02",
						Message: `invalid input value for enum mood: "nope"`, Position: position,
					})
					bad.ErrorStage = stage
					run(bad)
					// Failed input leaves the independently expected stored row intact.
					run(fixture.Select("SELECT v FROM "+name, []string{"v"}, []string{""}, [][]any{{sample.value}}))
				})
			}
		}
		if backend.PG {
			return // Native controls are not PostgreSQL SQL capabilities.
		}
		for range 2 {
			var dump bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
			for _, step := range restored {
				// Each round reexecutes restored defaults and CHECKs, then the
				// restored views; saved row retrieval alone is insufficient.
				step.Prepared = false
				fixture.EnumReferenceRunStep(t, p.Ctx, conn, backend.Recorder, step)
				if step.SelectRows {
					step.Prepared = true
					fixture.EnumReferenceRunStep(t, p.Ctx, conn, backend.Recorder, step)
				}
			}
		}
	})
}
