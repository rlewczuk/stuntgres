package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceRoutineHostPrimitiveFailures(t *testing.T) {
	hostPrimitiveScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		defer routineLanguagesCleanup(t, c, "DROP OWNED BY hp_failure_owner CASCADE", "DROP ROLE hp_failure_owner")()
		hostExec(t, p, b, c, "CREATE ROLE hp_failure_owner")
		hostExec(t, p, b, c, "GRANT USAGE,CREATE ON SCHEMA public TO hp_failure_owner")
		hostExec(t, p, b, c, "CREATE TABLE hp_effect(v integer)")
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO hp_effect VALUES (1)", 1))
		hostExec(t, p, b, c, "ALTER TABLE hp_effect OWNER TO hp_failure_owner")
		for _, tc := range []struct {
			name, sig, result, program, python, code, message, native string
			bad, good, want                                           any
		}{
			{"hp_over4", "v integer", "integer", "int4-offset-v1", "return v+1", "22003", "value \"2147483648\" is out of range for type integer", "integer out of range", int64(2147483647), int64(2147483646), int64(2147483647)},
			{"hp_under4", "v integer", "integer", "int4-decrement-v1", "return v-1", "22003", "value \"-2147483649\" is out of range for type integer", "integer out of range", int64(-2147483648), int64(-2147483647), int64(-2147483648)},
			{"hp_over8", "v bigint", "bigint", "int8-offset-v1", "return v+5", "22003", "value \"9223372036854775812\" is out of range for type bigint", "primitive: int8 overflow", int64(9223372036854775807), int64(-9223372036854775808), int64(-9223372036854775803)},
			{"hp_nul", "v text", "text", "text-invalid-v1", "return 'a'+chr(0)+'b' if v == 'nul' else v+'!'", "22021", "invalid byte sequence for encoding \"UTF8\": 0x00", "invalid byte sequence", "nul", "ok", "ok!"},
			{"hp_utf8", "v text", "text", "text-invalid-v1", "return chr(0xd800) if v == 'utf8' else v+'!'", "38000", "could not convert Python Unicode object to bytes", "invalid byte sequence for encoding \"UTF8\": 0xff", "utf8", "λ", "λ!"},
			{"hp_program_error", "v text", "text", "text-error-v1", "if v == 'bad':\n    raise ValueError('primitive program rejected text')\nreturn v+'!'", "38000", "ValueError: primitive program rejected text", "primitive: program rejected text", "bad", "ok", "ok!"},
			{"hp_object", "v integer", "integer", "int4-object-v1", "return 'not-an-integer' if v == 7 else v+1", "22P02", "invalid input syntax for type integer: \"not-an-integer\"", "primitive: expected int64 object", int64(7), int64(2), int64(3)},
		} {
			t.Run(tc.name, func(t *testing.T) {
				hostPrimitiveFunction(t, p, b, c, tc.name, tc.sig, tc.result, tc.program, tc.python, "SET application_name='hp-inner'")
				d := hostPythonError(tc.code, tc.message, tc.name)
				d.Context = "while creating return value\n" + d.Context
				if tc.name == "hp_program_error" {
					d.Context = "Traceback (most recent call last):\n  PL/Python function \"" + tc.name + "\", line 2, in <module>\n    raise ValueError('primitive program rejected text')\nPL/Python function \"" + tc.name + "\""
				}
				if tc.name == "hp_utf8" {
					d.Detail = "UnicodeEncodeError: 'utf-8' codec can't encode character '\\ud800' in position 0: surrogates not allowed"
				}
				q := "SELECT " + tc.name + "($1::" + tc.result + ") AS v"
				ordinary := tc.program == "int8-offset-v1" || tc.program == "text-error-v1" || tc.program == "int4-object-v1"
				nativeCode, nativeMessage := tc.code, tc.native
				if tc.name == "hp_nul" {
					nativeMessage = tc.message
				}
				if tc.name == "hp_utf8" {
					nativeCode = "22021"
				}
				pgNUL := b.PG && tc.name == "hp_nul"
				if pgNUL {
					// PL/Python's cstring conversion truncates this result at NUL.
					// This is not the native tagged-carrier validation contract.
					s := fixture.Select(q, []string{"v"}, []string{"TEXT"}, [][]any{{"a"}}, tc.bad)
					fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
				} else if b.PG {
					hostError(t, p, c, q, d, tc.bad)
				} else if ordinary {
					hostNativeError(t, p.Ctx, c, q, tc.native, tc.bad)
				} else {
					hostError(t, p, c, q, fixture.ControlsReferenceDiagnostic(nativeCode, nativeMessage), tc.bad)
				}
				// Keep one held wrapper through failure and recovery. The actual
				// PL/Python NUL conversion is successful and commits its prefix;
				// native NUL/carrier errors must instead roll that prefix back.
				wrapper := "hp_prefix_" + tc.name
				hostExec(t, p, b, c, "CREATE FUNCTION "+wrapper+"("+tc.sig+") RETURNS "+tc.result+" LANGUAGE plpgsql SECURITY DEFINER SET application_name='hp-wrapper' AS $$ BEGIN INSERT INTO hp_effect VALUES (2); RETURN "+tc.name+"(v); END $$")
				hostExec(t, p, b, c, "ALTER FUNCTION "+wrapper+"("+tc.result+") OWNER TO hp_failure_owner")
				wq := "SELECT " + wrapper + "($1::" + tc.result + ") AS v"
				if pgNUL {
					hostRows(t, p, b, c, wq, []string{"v"}, []string{"TEXT"}, [][]any{{"a"}}, tc.bad)
					hostRows(t, p, b, c, "SELECT count(*) AS v FROM hp_effect WHERE v=2", []string{"v"}, []string{"INT8"}, [][]any{{int64(1)}})
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("DELETE FROM hp_effect WHERE v=2", 1))
				} else if b.PG {
					wd := d
					wd.Context += "\nPL/pgSQL function " + wrapper + "(" + tc.result + ") line 1 at RETURN"
					hostError(t, p, c, wq, wd, tc.bad)
				} else if ordinary {
					hostNativeError(t, p.Ctx, c, wq, tc.native, tc.bad)
				} else {
					wd := fixture.ControlsReferenceDiagnostic(nativeCode, nativeMessage)
					wd.Context = "PL/pgSQL function " + wrapper + "(" + tc.result + ") line 1 at RETURN"
					hostError(t, p, c, wq, wd, tc.bad)
				}
				stmt, err := c.PrepareContext(p.Ctx, wq)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				var value any
				err = stmt.QueryRowContext(p.Ctx, tc.bad).Scan(&value)
				if pgNUL {
					if err != nil || value != "a" {
						t.Fatalf("same held PL/Python NUL result=%#v/%v want a", value, err)
					}
				} else if b.PG {
					wd := d
					wd.Context += "\nPL/pgSQL function " + wrapper + "(" + tc.result + ") line 1 at RETURN"
					fixture.AssertDiagnostic(t, err, &wd)
				} else if ordinary {
					if err == nil || !strings.Contains(err.Error(), tc.native) {
						t.Errorf("held error=%v want %q", err, tc.native)
					}
					var state interface{ SQLState() string }
					if errors.As(err, &state) {
						t.Errorf("ordinary host error acquired SQLSTATE: %v", err)
					}
				} else {
					wd := fixture.ControlsReferenceDiagnostic(nativeCode, nativeMessage)
					wd.Context = "PL/pgSQL function " + wrapper + "(" + tc.result + ") line 1 at RETURN"
					fixture.AssertDiagnostic(t, err, &wd)
				}
				if pgNUL {
					hostRows(t, p, b, c, "SELECT v FROM hp_effect ORDER BY v", []string{"v"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}})
					hostRows(t, p, b, c, "SELECT count(*) AS v FROM hp_effect WHERE v=2", []string{"v"}, []string{"INT8"}, [][]any{{int64(1)}})
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("DELETE FROM hp_effect WHERE v=2", 1))
				} else {
					hostRows(t, p, b, c, "SELECT v FROM hp_effect ORDER BY v", []string{"v"}, []string{"INT4"}, [][]any{{int64(1)}})
					hostRows(t, p, b, c, "SELECT count(*) AS v FROM hp_effect WHERE v=2", []string{"v"}, []string{"INT8"}, [][]any{{int64(0)}})
				}
				owner := "postgres"
				if b.PG {
					owner = "stuntgres"
				}
				hostRows(t, p, b, c, "SELECT current_user::text AS u,current_setting('application_name') AS a", []string{"u", "a"}, []string{"TEXT", "TEXT"}, [][]any{{owner, ""}})
				held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: wq}
				hostRows(t, p, b, held, wq, []string{"v"}, []string{map[string]string{"integer": "INT4", "bigint": "INT8", "text": "TEXT"}[tc.result]}, [][]any{{tc.want}}, tc.good)
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("DELETE FROM hp_effect WHERE v=2", 1))
			})
		}
		hostPrimitiveFunction(t, p, b, c, "hp_checked_add", "a bigint,b bigint", "bigint", "int8-add-v1", "return a+b", "")
		addQuery := "SELECT hp_checked_add($1::bigint,$2::bigint) AS v"
		for _, tc := range []struct {
			a, b     int64
			returned string
		}{{9223372036854775807, 1, "9223372036854775808"}, {-9223372036854775808, -1, "-9223372036854775809"}} {
			if b.PG {
				d := hostPythonError("22003", "value \""+tc.returned+"\" is out of range for type bigint", "hp_checked_add")
				d.Context = "while creating return value\n" + d.Context
				hostError(t, p, c, addQuery, d, tc.a, tc.b)
			} else {
				hostNativeError(t, p.Ctx, c, addQuery, "primitive: int8 overflow", tc.a, tc.b)
			}
		}
		if !b.PG {
			hostPrimitiveCarrierFailures(t, p)
			hostPrimitiveConverterRollback(t, p)
		}
	})
}

// Each adversarial direction is installed before opening its own genuine Driver.
// These controls are Go ABI proof, not PL/Python representation claims.
func hostPrimitiveCarrierFailures(t *testing.T, p *fixture.Pair) {
	t.Helper()
	for _, typ := range []stuntgres.HostType{stuntgres.HostBool, stuntgres.HostInt4, stuntgres.HostInt8, stuntgres.HostText} {
		for _, fault := range []string{"tag", "extra-bool", "extra-int", "extra-text", "extra-hstore", "null-sql", "null-object", "null-result", "wrong-object", "wrong-result", "from-error", "to-error"} {
			if typ == stuntgres.HostBool && fault == "extra-bool" || (typ == stuntgres.HostInt4 || typ == stuntgres.HostInt8) && fault == "extra-int" || typ == stuntgres.HostText && fault == "extra-text" {
				continue
			}
			t.Run(string(typ)+"-"+fault, func(t *testing.T) {
				d := hostPrimitiveDefinition()
				for i := range d.Conversions {
					if d.Conversions[i].Type != typ {
						continue
					}
					originalFrom, originalTo := d.Conversions[i].FromSQL, d.Conversions[i].ToSQL
					d.Conversions[i].FromSQL = func(ctx context.Context, v stuntgres.HostSQLValue) (stuntgres.HostObject, error) {
						if fault == "from-error" {
							return stuntgres.HostObject{}, errors.New("primitive: FROM rejected")
						}
						if fault == "null-object" {
							return stuntgres.HostObject{Null: true, Value: false}, nil
						}
						if fault == "wrong-object" {
							if typ == stuntgres.HostBool {
								return stuntgres.HostObject{Value: "false"}, nil
							}
							return stuntgres.HostObject{Value: int(7)}, nil
						}
						return originalFrom(ctx, v)
					}
					d.Conversions[i].ToSQL = func(ctx context.Context, v stuntgres.HostObject) (stuntgres.HostSQLValue, error) {
						if fault == "to-error" {
							return stuntgres.HostSQLValue{}, errors.New("primitive: TO rejected")
						}
						out, err := originalTo(ctx, v)
						if err != nil {
							return out, err
						}
						switch fault {
						case "tag":
							out.Type = stuntgres.HostHstore
						case "extra-bool":
							out.Bool = true
						case "extra-int":
							out.Int64 = 7
						case "extra-text":
							out.Text = "extra"
						case "extra-hstore":
							out.Hstore = []stuntgres.HostHstoreEntry{}
						case "null-sql":
							out.Null = true
							out.Bool = true
						}
						return out, nil
					}
				}
				program := map[stuntgres.HostType]string{stuntgres.HostBool: "bool-not-v1", stuntgres.HostInt4: "int4-offset-v1", stuntgres.HostInt8: "int8-offset-v1", stuntgres.HostText: "text-mark-v1"}[typ]
				if fault == "null-result" || fault == "wrong-result" {
					for i := range d.Programs {
						if d.Programs[i].ID != program {
							continue
						}
						d.Programs[i].Execute = func(context.Context, []stuntgres.HostObject) (stuntgres.HostObject, error) {
							if fault == "null-result" {
								return stuntgres.HostObject{Null: true, Value: int64(7)}, nil
							}
							if typ == stuntgres.HostBool {
								return stuntgres.HostObject{Value: "false"}, nil
							}
							return stuntgres.HostObject{Value: int(7)}, nil
						}
					}
				}
				_, b := hostOpenNative(t, &d)
				c, err := b.DB.Conn(p.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer c.Close()
				hostPrimitiveFunction(t, p, b, c, "hp_adversary", "v "+string(typ), string(typ), program, "", "")
				message := map[string]string{"tag": "wrong SQL type", "extra-bool": "contradictory host SQL result payload", "extra-int": "contradictory host SQL result payload", "extra-text": "contradictory host SQL result payload", "extra-hstore": "contradictory host SQL result payload", "null-sql": "contradictory host SQL NULL", "null-object": "contradictory host null object", "null-result": "contradictory host null result", "from-error": "primitive: FROM rejected", "to-error": "primitive: TO rejected"}[fault]
				if fault == "wrong-object" || fault == "wrong-result" {
					message = "primitive: expected int64 object"
					if typ == stuntgres.HostBool {
						message = "primitive: expected bool object"
					}
					if typ == stuntgres.HostText {
						message = "primitive: expected string object"
					}
				}
				literal := "7"
				if typ == stuntgres.HostBool {
					literal = "false"
				}
				if typ == stuntgres.HostText {
					literal = "'ok'"
				}
				hostNativeError(t, p.Ctx, c, "SELECT hp_adversary("+literal+"::"+string(typ)+")", message)
			})
		}
	}
}

func hostPrimitiveConverterRollback(t *testing.T, p *fixture.Pair) {
	t.Helper()
	for _, direction := range []string{"from", "to"} {
		t.Run(direction+"-rollback", func(t *testing.T) {
			d := hostPrimitiveDefinition()
			for i := range d.Conversions {
				if d.Conversions[i].Type != stuntgres.HostText {
					continue
				}
				from, to := d.Conversions[i].FromSQL, d.Conversions[i].ToSQL
				d.Conversions[i].FromSQL = func(ctx context.Context, v stuntgres.HostSQLValue) (stuntgres.HostObject, error) {
					if direction == "from" && v.Text == "bad" {
						return stuntgres.HostObject{}, errors.New("primitive: converter rejected text")
					}
					return from(ctx, v)
				}
				d.Conversions[i].ToSQL = func(ctx context.Context, v stuntgres.HostObject) (stuntgres.HostSQLValue, error) {
					if direction == "to" && v.Value == "bad!" {
						return stuntgres.HostSQLValue{}, errors.New("primitive: converter rejected text")
					}
					return to(ctx, v)
				}
			}
			_, b := hostOpenNative(t, &d)
			c, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			hostExec(t, p, b, c, "CREATE ROLE hp_converter_owner")
			hostExec(t, p, b, c, "CREATE TABLE hp_converter_effect(v integer)")
			hostExec(t, p, b, c, "ALTER TABLE hp_converter_effect OWNER TO hp_converter_owner")
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO hp_converter_effect VALUES (1)", 1))
			hostPrimitiveFunction(t, p, b, c, "hp_converter", "v text", "text", "text-mark-v1", "", "SET application_name='converter-inner'")
			hostExec(t, p, b, c, "CREATE FUNCTION hp_converter_prefix(v text) RETURNS text LANGUAGE plpgsql SECURITY DEFINER SET application_name='converter-wrapper' AS $$ BEGIN INSERT INTO hp_converter_effect VALUES (2); RETURN hp_converter(v); END $$")
			hostExec(t, p, b, c, "ALTER FUNCTION hp_converter_prefix(text) OWNER TO hp_converter_owner")
			q := "SELECT hp_converter_prefix($1::text) AS v"
			stmt, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			var value any
			err = stmt.QueryRowContext(p.Ctx, "bad").Scan(&value)
			if err == nil || !strings.Contains(err.Error(), "primitive: converter rejected text") {
				t.Errorf("held converter failure=%v", err)
			}
			var state interface{ SQLState() string }
			if errors.As(err, &state) {
				t.Errorf("converter fabricated SQLSTATE: %v", err)
			}
			hostRows(t, p, b, c, "SELECT v FROM hp_converter_effect ORDER BY v", []string{"v"}, []string{"INT4"}, [][]any{{int64(1)}})
			hostRows(t, p, b, c, "SELECT count(*) AS v FROM hp_converter_effect WHERE v=2", []string{"v"}, []string{"INT8"}, [][]any{{int64(0)}})
			hostRows(t, p, b, c, "SELECT current_user::text AS u,current_setting('application_name') AS a", []string{"u", "a"}, []string{"TEXT", "TEXT"}, [][]any{{"postgres", ""}})
			held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
			for _, v := range []string{"ok", "λ"} {
				hostRows(t, p, b, held, q, []string{"v"}, []string{"TEXT"}, [][]any{{v + "!"}}, v)
			}
			hostRows(t, p, b, c, "SELECT count(*) AS v FROM hp_converter_effect WHERE v=2", []string{"v"}, []string{"INT8"}, [][]any{{int64(2)}})
		})
	}
}
