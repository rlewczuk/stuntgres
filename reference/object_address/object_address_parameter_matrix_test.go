package reference_test

import (
	"database/sql"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

type objectAddressParameterCase struct {
	name string
	args []any
	want fixture.Diagnostic
}

func TestReferenceObjectAddressDiagnosticParameterMatrix(t *testing.T) {
	objectAddressScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, fn := range []string{"pg_identify_object", "pg_identify_object_as_address", "pg_describe_object", "pg_get_object_address"} {
			t.Run(fn, func(t *testing.T) {
				q := "SELECT " + fn + "($1,$2,$3) AS v"
				valid := []any{int64(1247), int64(23), int64(0)}
				typ, value := "RECORD", any("(type,pg_catalog,int4,integer)")
				switch fn {
				case "pg_identify_object_as_address":
					value = "(type,{integer},{})"
				case "pg_describe_object":
					typ, value = "TEXT", "type integer"
				case "pg_get_object_address":
					valid = []any{"type", "{integer}", "{}"}
					value = "(1247,23,0)"
				}
				var cases []objectAddressParameterCase
				var suppressed []objectAddressParameterCase
				addSemantic := func(name string, args []any, want fixture.Diagnostic) {
					// Body diagnostics have no portal import Context; only the
					// type parser supplies its own Position and Context.
					cases = append(cases, objectAddressParameterCase{name, args, want})
					for slot := range 3 {
						withNull := append([]any(nil), args...)
						withNull[slot] = nil
						suppressed = append(suppressed, objectAddressParameterCase{name: fmt.Sprintf("%s_null%d", name, slot+1), args: withNull})
					}
				}
				addImport := func(name string, args []any, slot int, code, message, detail string) {
					cases = append(cases, objectAddressParameterCase{name, args, fixture.Diagnostic{Code: code, Message: message, Detail: detail, Context: fmt.Sprintf("unnamed portal parameter $%d = '...'", slot+1)}})
				}
				if fn != "pg_get_object_address" {
					for slot := range 3 {
						typeName, upper, lower := "oid", "4294967296", "-4294967296"
						if slot == 2 {
							typeName, upper, lower = "integer", "2147483648", "-2147483649"
						}
						for _, input := range []string{"bad", upper, lower} {
							code, message := "22003", fmt.Sprintf("value %q is out of range for type %s", input, typeName)
							if input == "bad" {
								code, message = "22P02", fmt.Sprintf("invalid input syntax for type %s: %q", typeName, input)
							}
							args := append([]any(nil), valid...)
							args[slot] = input
							addImport(fmt.Sprintf("slot%d_%s", slot+1, input), args, slot, code, message, "")
							for nullSlot := range 3 {
								if nullSlot == slot {
									continue
								}
								withNull := append([]any(nil), args...)
								withNull[nullSlot] = nil
								addImport(fmt.Sprintf("slot%d_%s_null%d", slot+1, input, nullSlot+1), withNull, slot, code, message, "")
							}
						}
					}
					addImport("parameter_order", []any{"bad", "bad", "bad"}, 0, "22P02", `invalid input syntax for type oid: "bad"`, "")
				} else {
					addSemantic("type_slot", []any{"stone", "{integer}", "{}"}, fixture.Diagnostic{Code: "22023", Message: `unrecognized object type "stone"`})
					semantic := func(name, kind, names, args, code, message string) {
						addSemantic(name, []any{kind, names, args}, fixture.Diagnostic{Code: code, Message: message})
					}
					for _, tc := range []struct{ kind, names string }{
						{"type", "{integer}"},
						{"table", "{public,addr_absent_matrix}"},
						{"schema", "{public}"},
						{"role", "{addr_absent_matrix}"},
						{"database", "{addr_absent_matrix}"},
						{"tablespace", "{pg_default}"},
						{"extension", "{addr_absent_matrix}"},
						{"function", "{pg_catalog,pg_identify_object}"},
						{"operator", "{pg_catalog,+}"},
						{"cast", "{integer}"},
						{"domain constraint", "{integer}"},
						{"operator class", "{btree,pg_catalog,int4_ops}"},
						{"operator family", "{btree,pg_catalog,integer_ops}"},
					} {
						semantic(tc.kind+"_null_name", tc.kind, "{NULL}", "{}", "22023", "name or argument lists may not contain nulls")
						semantic(tc.kind+"_null_arg", tc.kind, tc.names, "{NULL}", "22023", "name or argument lists may not contain nulls")
						message := "name list length must be at least 1"
						if tc.kind == "type" || tc.kind == "cast" || tc.kind == "domain constraint" {
							message = "name list length must be exactly 1"
						}
						semantic(tc.kind+"_empty_names", tc.kind, "{}", "{}", "22023", message)
					}
					for _, kind := range []string{"type", "schema", "role", "database", "tablespace", "extension", "cast", "domain constraint"} {
						semantic(kind+"_long_names", kind, "{integer,text}", "{}", "22023", "name list length must be exactly 1")
					}
					for _, kind := range []string{"operator class", "operator family"} {
						semantic(kind+"_short_names", kind, "{btree}", "{}", "22023", "name list length must be at least 2")
					}
					for _, kind := range []string{"cast", "domain constraint", "operator"} {
						names, message := "{integer}", "argument list length must be exactly 1"
						inputs := []string{"{}", "{integer,text}"}
						if kind == "operator" {
							names, message = "{pg_catalog,+}", "argument list length must be exactly 2"
							inputs = []string{"{}", "{integer}", "{integer,integer,integer}"}
						}
						for _, args := range inputs {
							semantic(kind+"_args_"+args, kind, names, args, "22023", message)
						}
					}
					semantic("missing_type", "type", "{public.addr_absent_matrix}", "{}", "42704", `type "public.addr_absent_matrix" does not exist`)
					semantic("missing_name", "table", "{public,addr_absent_matrix}", "{}", "42P01", `relation "public.addr_absent_matrix" does not exist`)
					for _, tc := range []struct{ kind, names string }{
						{"function", "{pg_catalog,pg_identify_object}"},
						{"aggregate", "{pg_catalog,count}"},
						{"procedure", "{pg_catalog,pg_identify_object}"},
						{"operator", "{pg_catalog,+}"},
						{"cast", "{integer}"},
					} {
						for _, args := range []string{"{NONE}", "{NONE,NULL}"} {
							addSemantic(tc.kind+"_syntax_"+args, []any{tc.kind, tc.names, args}, fixture.Diagnostic{Code: "42601", Message: `syntax error at or near "NONE"`, Position: "1", Context: `invalid type name "NONE"`})
						}
						semantic(tc.kind+"_null_before_syntax", tc.kind, tc.names, "{NULL,NONE}", "22023", "name or argument lists may not contain nulls")
						semantic(tc.kind+"_missing_type_before_null", tc.kind, tc.names, "{addr_absent_matrix,NULL}", "22023", "name or argument lists may not contain nulls")
					}
					addSemantic("cast_name_syntax", []any{"cast", "{NONE}", "{NULL}"}, fixture.Diagnostic{Code: "42601", Message: `syntax error at or near "NONE"`, Position: "1", Context: `invalid type name "NONE"`})
					semantic("cast_name_null", "cast", "{NULL}", "{NONE}", "22023", "name or argument lists may not contain nulls")
					for slot := 1; slot < 3; slot++ {
						for _, input := range []string{"bad", "{a,,b}"} {
							detail := `Array value must start with "{" or dimension information.`
							if input != "bad" {
								detail = `Unexpected "," character.`
							}
							args := append([]any(nil), valid...)
							args[slot] = input
							message := fmt.Sprintf("malformed array literal: %q", input)
							addImport(fmt.Sprintf("slot%d_%s", slot+1, input), args, slot, "22P02", message, detail)
							for nullSlot := range 3 {
								if nullSlot == slot {
									continue
								}
								withNull := append([]any(nil), args...)
								withNull[nullSlot] = nil
								addImport(fmt.Sprintf("slot%d_%s_null%d", slot+1, input, nullSlot+1), withNull, slot, "22P02", message, detail)
							}
						}
					}
					addImport("array_parameter_order", []any{"stone", "bad", "bad"}, 1, "22P02", `malformed array literal: "bad"`, `Array value must start with "{" or dimension information.`)
				}
				for _, route := range []string{"direct", "fresh", "held"} {
					t.Run(route, func(t *testing.T) {
						var executor fixture.Executor = c
						if route == "held" {
							stmt, err := c.PrepareContext(p.Ctx, q)
							if err != nil {
								t.Fatal(err)
							}
							defer stmt.Close()
							executor = fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
						}
						run := func(s fixture.Step) {
							if route == "fresh" {
								s = fixture.Prepared(s)
							}
							fixture.RunStep(t, p.Ctx, executor, b.Recorder, s)
						}
						recover := fixture.Select(q, []string{"v"}, []string{typ}, [][]any{{value}}, valid...)
						for _, tc := range cases {
							t.Run(tc.name, func(t *testing.T) {
								s := fixture.ImplicitError(q, tc.want, tc.args...)
								s.SelectRows, s.ErrorStage = true, "execute"
								run(s)
								run(recover)
							})
						}
						for slot := range 3 {
							args := append([]any(nil), valid...)
							args[slot] = nil
							run(fixture.Select(q, []string{"v"}, []string{typ}, [][]any{{nil}}, args...))
							run(recover)
						}
						for _, tc := range suppressed {
							t.Run(tc.name, func(t *testing.T) {
								run(fixture.Select(q, []string{"v"}, []string{typ}, [][]any{{nil}}, tc.args...))
								run(recover)
							})
						}
					})
				}
			})
		}
	})
}
