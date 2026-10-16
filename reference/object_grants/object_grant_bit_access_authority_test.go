package reference_test

import (
	"database/sql"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

// REL_18_0 pg_proc: 3032 (1560,23)->23 and 3033 (1560,23,23)->1560.
// Both are strict immutable parallel-safe INTERNAL functions, with no defaults,
// variadic inputs, OUT arguments or SQL bodies. Physical VARBIT still selects
// the BIT declaration; the BYTEA siblings instead have an INT8 index formal.
func TestReferenceObjectGrantBitAccessAuthority(t *testing.T) {
	for _, name := range []string{"get_bit", "set_bit"} {
		t.Run(name, func(t *testing.T) {
			objectGrantScenario(t, nil, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
				typ, signature := "INT4", "bit,integer"
				want := any(int64(1))
				formals := []string{"f", "n"}
				literal := []string{"B'100000001'", "8"}
				args := []any{"100000001", int64(8)}
				if name == "set_bit" {
					typ, signature, want = "BIT", "bit,integer,integer", "100000000"
					formals = append(formals, "k")
					literal = append(literal, "0")
					args = append(args, int64(0))
				}
				target := "pg_catalog." + name + "(" + signature + ")"
				call := func(a []string) string { return name + "(" + strings.Join(a, ",") + ")" }
				run(fixture.Exec("GRANT USAGE ON SCHEMA public TO og_object_reader", 0))
				run(fixture.Exec("CREATE TABLE og_bit_access(id INT,f BIT(9),v VARBIT(9),w VARBIT,n SMALLINT,k SMALLINT)", 0))
				run(fixture.Exec("INSERT INTO og_bit_access VALUES(1,B'100000001',B'100000001',B'100000001',8,0),(2,NULL,NULL,NULL,8,0),(3,B'100000001',B'100000001',B'100000001',NULL,0),(4,B'100000001',B'100000001',B'100000001',8,NULL)", 4))
				run(fixture.Exec("GRANT SELECT ON og_bit_access TO og_object_reader", 0))
				run(fixture.Exec("CREATE VIEW og_bit_access_view AS SELECT "+call(formals)+" AS v FROM og_bit_access WHERE id=1", 0))
				run(fixture.Exec("GRANT SELECT ON og_bit_access_view TO og_object_reader", 0))
				value := func(expr, tail string, expected any, params ...any) fixture.Step {
					s := fixture.Select("SELECT "+expr+" AS v FROM og_bit_access"+tail, []string{"v"}, []string{typ}, [][]any{{expected}}, params...)
					s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
						cols, err := rows.ColumnTypes()
						if err != nil {
							t.Fatal(err)
						}
						if len(cols) != 1 {
							t.Fatal("column count", len(cols))
						}
						col := cols[0]
						scan := reflect.TypeFor[string]()
						length, lengthKnown := int64(-1), true
						if typ == "INT4" {
							scan, length, lengthKnown = reflect.TypeFor[int64](), 0, false
							if b.PG {
								scan = reflect.TypeFor[int32]()
							}
						}
						if col.Name() != "v" || col.DatabaseTypeName() != typ || col.ScanType() != scan {
							t.Fatal("descriptor", col.Name(), col.DatabaseTypeName(), col.ScanType())
						}
						if n, known := col.Length(); n != length || known != lengthKnown {
							t.Fatal("length", n, known)
						}
						if p, s, known := col.DecimalSize(); p != 0 || s != 0 || known {
							t.Fatal("precision", p, s, known)
						}
						if nullable, known := col.Nullable(); nullable || known {
							t.Fatal("nullable", nullable, known)
						}
					}
					return s
				}
				bodyError := func(expr, code, message string) fixture.Step {
					s := fixture.ImplicitError("SELECT "+expr+" AS v FROM og_bit_access WHERE id=1", fixture.Diagnostic{Code: code, Message: message})
					s.SelectRows, s.ErrorStage = true, "execute"
					return s
				}
				cases := []objectGrantProviderCase{
					{value(call(formals), " WHERE id=1", want), true, true},
					{value("pg_catalog."+call(formals), " WHERE id=1", want), true, true},
					{value("CASE WHEN id=1 THEN "+call(formals)+" ELSE NULL END", " WHERE id=1", want), true, true},
					{fixture.Select("SELECT v FROM og_bit_access_view", []string{"v"}, []string{typ}, [][]any{{want}}), true, true},
					{fixture.Select("SELECT "+call(formals)+" AS v FROM og_bit_access WHERE id>99", []string{"v"}, []string{typ}, nil), true, true},
					{fixture.Select("SELECT "+call(formals)+" AS v FROM og_bit_access WHERE FALSE", []string{"v"}, []string{typ}, nil), true, true},
					{value("CASE WHEN FALSE THEN "+call(formals)+" ELSE NULL END", " WHERE id=1", nil), false, true},
					// Immutable all-constant calls are fresh-only: PostgreSQL may
					// legitimately retain a previously folded result in a held plan.
					{value(call(literal), " WHERE id=1", want), true, false},
				}
				for _, source := range []string{"v", "w"} {
					a := append([]string(nil), formals...)
					a[0] = source
					cases = append(cases, objectGrantProviderCase{value(call(a), " WHERE id=1", want), true, true})
				}
				// A row-dependent CASE gives imported parameters their genuine
				// logical type without unsupported explicit BIT/VARBIT casts.
				parameter := []string{"CASE WHEN id=1 THEN $1 ELSE f END", "$2::INTEGER"}
				fresh := []string{"CASE WHEN TRUE THEN $1 ELSE f END", "$2::INTEGER"}
				if name == "set_bit" {
					parameter, fresh = append(parameter, "$3::INTEGER"), append(fresh, "$3::INTEGER")
				}
				cases = append(cases, objectGrantProviderCase{value(call(parameter), " WHERE id=1", want, args...), true, true})
				for slot := range formals {
					// Every runtime NULL remains a surviving invocation, unlike a
					// literal or execution-known whole NULL at the same formal.
					cases = append(cases, objectGrantProviderCase{value(call(formals), " WHERE id="+strconv.Itoa(slot+2), nil), true, true})
					a := append([]string(nil), formals...)
					a[slot] = "NULL::INTEGER"
					if slot == 0 {
						a[slot] = "CASE WHEN FALSE THEN f ELSE NULL END"
					}
					cases = append(cases, objectGrantProviderCase{value(call(a), " WHERE id=1", nil), false, true})
					nullArgs := append([]any(nil), args...)
					nullArgs[slot] = nil
					cases = append(cases, objectGrantProviderCase{value(call(fresh), " WHERE id=1", nil, nullArgs...), false, false})
				}
				for _, vector := range []struct {
					bits, index, bit, result string
					got                      int64
				}{
					{"10000000", "0", "0", "00000000", 1},
					{"000000000", "0", "1", "100000000", 0},
					{"101010101010101010", "6", "0", "101010001010101010", 1},
					{"1001010", "6", "1", "1001011", 0},
				} {
					a := []string{"B'" + vector.bits + "'", vector.index + "::SMALLINT"}
					expected := any(vector.got)
					if name == "set_bit" {
						a, expected = append(a, vector.bit+"::SMALLINT"), vector.result
					}
					cases = append(cases, objectGrantProviderCase{value(call(a), " WHERE id=1", expected), true, false})
				}
				// Empty bits are non-NULL. Authorization precedes real body
				// errors, including index-range before invalid replacement-bit.
				for _, bad := range []struct{ source, index, code, message string }{
					{"B''", "0", "2202E", "bit index 0 out of valid range (0..-1)"},
					{"B'101'", "-1", "2202E", "bit index -1 out of valid range (0..2)"},
					{"B'101'", "3", "2202E", "bit index 3 out of valid range (0..2)"},
					{"B'1'", "1", "2202E", "bit index 1 out of valid range (0..0)"},
				} {
					a := []string{bad.source, bad.index}
					if name == "set_bit" {
						a = append(a, "2")
					}
					cases = append(cases, objectGrantProviderCase{bodyError(call(a), bad.code, bad.message), true, false})
				}
				rowBad := append([]string(nil), formals...)
				rowBad[1] = "n+1"
				cases = append(cases, objectGrantProviderCase{bodyError(call(rowBad), "2202E", "bit index 9 out of valid range (0..8)"), true, true})
				child := append([]string(nil), formals...)
				child[1] = "n/(n-n)"
				childError := bodyError(call(child), "22012", "division by zero")
				childError.Query = strings.Replace(childError.Query, "WHERE id=1", "WHERE id=2", 1)
				cases = append(cases, objectGrantProviderCase{childError, true, true})
				if name == "set_bit" {
					for _, bit := range []string{"2", "-1", "k+2"} {
						cases = append(cases, objectGrantProviderCase{bodyError("set_bit(f,n,"+bit+")", "22023", "new bit must be 0 or 1"), true, true})
					}
				}
				// Child analysis/import failures are not waived by outer NULL or
				// by a denied outer function. Exact fields and stages are retained.
				var controls []fixture.Step
				analysis := func(q, code, message, hint, position string) {
					s := fixture.ImplicitError(q, fixture.Diagnostic{Code: code, Message: message, Hint: hint, Position: position})
					s.SelectRows, s.ErrorStage = true, "execute"
					if prepared {
						s.ErrorStage = "prepare"
					}
					controls = append(controls, s)
				}
				badSource := "SELECT get_bit('2',NULL::integer)"
				if name == "set_bit" {
					badSource = "SELECT set_bit('2',0,NULL::integer)"
				}
				analysis(badSource, "22P02", `"2" is not a valid binary digit`, "", "16")
				for _, bad := range []struct{ expr, signature string }{
					{"get_bit(B'1',0::bigint)", "get_bit(bit, bigint)"},
					{"get_bit(B'1',0::numeric)", "get_bit(bit, numeric)"},
					{"get_bit(B'1','0'::text)", "get_bit(bit, text)"},
					{"get_bit(B'1')", "get_bit(bit)"},
					{"set_bit(B'1',0,1::bigint)", "set_bit(bit, integer, bigint)"},
					{"set_bit(B'1',0)", "set_bit(bit, integer)"},
					{"set_bit(B'1',0::bigint,1)", "set_bit(bit, bigint, integer)"},
					{"set_bit(B'1',0,1::numeric)", "set_bit(bit, integer, numeric)"},
					{"set_bit(B'1',0,'1'::text)", "set_bit(bit, integer, text)"},
				} {
					analysis("SELECT "+bad.expr, "42883", "function "+bad.signature+" does not exist", fixture.BinaryHashHint, "8")
				}
				badIndex := append([]string(nil), formals...)
				badIndex[1] = "'bad'"
				analysis("SELECT "+call(badIndex)+" FROM og_bit_access WHERE id=2", "22P02", `invalid input syntax for type integer: "bad"`, "", "18")
				constantChild := append([]string(nil), formals...)
				constantChild[1] = "1/0"
				controls = append(controls, bodyError(call(constantChild), "22012", "division by zero"))
				overflow := append([]string(nil), formals...)
				overflow[1] = "2147483648::integer"
				controls = append(controls, bodyError(call(overflow), "22003", "integer out of range"))
				importSQL := "SELECT " + call(fresh) + " AS v FROM og_bit_access WHERE id=1"
				for _, bad := range []struct {
					slot          int
					value         any
					code, message string
				}{
					{0, "2", "22P02", `"2" is not a valid binary digit`},
					{1, "bad", "22P02", `invalid input syntax for type integer: "bad"`},
					{1, "2147483648", "22003", `value "2147483648" is out of range for type integer`},
					{2, "bad", "22P02", `invalid input syntax for type integer: "bad"`},
					{2, "2147483648", "22003", `value "2147483648" is out of range for type integer`},
				} {
					if bad.slot >= len(args) {
						continue
					}
					a := append([]any(nil), args...)
					a[bad.slot] = bad.value
					if bad.slot != 0 {
						a[0] = nil
					} else {
						a[1] = nil
					}
					d := fixture.Diagnostic{Code: bad.code, Message: bad.message, Context: "unnamed portal parameter $" + strconv.Itoa(bad.slot+1) + " = '...'"}
					s := fixture.ImplicitError(importSQL, d, a...)
					s.SelectRows, s.ErrorStage = true, "execute"
					controls = append(controls, s)
				}
				sibling, siblingValue, siblingType := "set_bit(f,n,k)", any("100000000"), "BIT"
				bytea, byteaValue, byteaType := `get_bit('\x80'::bytea,0::bigint)`, any(int64(0)), "INT4"
				if name == "set_bit" {
					sibling, siblingValue, siblingType = "get_bit(f,n)", int64(1), "INT4"
					bytea, byteaValue, byteaType = `set_bit('\x80'::bytea,0::bigint,1)`, "\x81", "BYTEA"
				}
				controls = append(controls,
					fixture.Select("SELECT "+sibling+" AS v FROM og_bit_access WHERE id=1", []string{"v"}, []string{siblingType}, [][]any{{siblingValue}}),
					fixture.Select("SELECT "+bytea+" AS v", []string{"v"}, []string{byteaType}, [][]any{{byteaValue}}),
				)
				objectGrantProviderLifecycle(t, p, b, c, run, target, "function", name, cases, controls)
				// Conversely, revoking BYTEA's distinct formal leaves BIT callable.
				byteaTarget := "pg_catalog." + name + "(bytea,bigint"
				if name == "set_bit" {
					byteaTarget += ",integer"
				}
				byteaTarget += ")"
				defer objectGrantRoutineCleanup(t, p, c, []string{byteaTarget})
				run(fixture.Exec("REVOKE EXECUTE ON FUNCTION "+byteaTarget+" FROM PUBLIC", 0))
				fixture.RoleGrantActor(run, "og_object_reader", func() {
					run(value(call(formals), " WHERE id=1", want))
					s := fixture.ImplicitError("SELECT "+bytea+" AS v", fixture.Diagnostic{Code: "42501", Message: "permission denied for function " + name})
					s.SelectRows, s.ErrorStage = true, "execute"
					run(s)
				})
			})
		})
		t.Run(name+"_stored_writes", func(t *testing.T) {
			objectGrantScenario(t, []string{"og_bit_writes"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
				target, resultType, expression, constant, condition := "pg_catalog.get_bit(bit,integer)", "INT4", "get_bit(v,n)", "get_bit(B'100',0)", "get_bit(v,n)=1"
				expected := any(int64(1))
				if name == "set_bit" {
					target, resultType, expression, constant, condition = "pg_catalog.set_bit(bit,integer,integer)", "BIT", "set_bit(v,n,k)", "set_bit(B'000',0,1)", "set_bit(v,n,k)=B'100'"
					expected = "100"
				}
				indexExpression := expression
				if name == "set_bit" {
					// Exercise the setter without requiring a native BIT btree class.
					indexExpression = "get_bit(" + expression + ",n)"
				}
				defer objectGrantRoutineCleanup(t, p, c, []string{target})
				run(fixture.Exec("CREATE SCHEMA og_bit_writes", 0))
				run(fixture.Exec("GRANT USAGE,CREATE ON SCHEMA og_bit_writes TO og_object_reader", 0))
				// The actual table owner is the non-superuser actor, so stored
				// computation cannot borrow a superuser definer's EXECUTE.
				fixture.RoleGrantActor(run, "og_object_reader", func() {
					generatedType := resultType
					if name == "set_bit" {
						generatedType = `"bit"`
					}
					for _, q := range []string{
						"CREATE TABLE og_bit_writes.d(id INT,r " + generatedType + " DEFAULT " + constant + ")",
						"CREATE TABLE og_bit_writes.g(id INT,v VARBIT,n INT,k INT,r " + generatedType + " GENERATED ALWAYS AS (" + expression + ") STORED)",
						"CREATE TABLE og_bit_writes.c(id INT,v VARBIT,n INT,k INT,CONSTRAINT og_bit_valid CHECK(" + condition + "))",
						"CREATE TABLE og_bit_writes.i(id INT,v VARBIT,n INT,k INT)",
						"CREATE INDEX og_bit_key ON og_bit_writes.i((" + indexExpression + "))",
					} {
						run(fixture.Exec(q, 0))
					}
				})
				type consumer struct {
					table, query string
					held         *fixture.HeldStatement
				}
				consumers := []consumer{
					{table: "d", query: "INSERT INTO og_bit_writes.d(id) VALUES(1),(2)"},
					{table: "g", query: "INSERT INTO og_bit_writes.g(id,v,n,k) VALUES(1,$1,$2,$3),(2,$1,$2,$3)"},
					{table: "c", query: "INSERT INTO og_bit_writes.c(id,v,n,k) VALUES(1,$1,$2,$3),(2,$1,$2,$3)"},
					{table: "i", query: "INSERT INTO og_bit_writes.i(id,v,n,k) VALUES(1,$1,$2,$3),(2,$1,$2,$3)"},
				}
				fixture.RoleGrantActor(run, "og_object_reader", func() {
					for i := 1; i < len(consumers); i++ {
						stmt, err := c.PrepareContext(p.Ctx, consumers[i].query)
						if err != nil {
							t.Fatal(err)
						}
						consumers[i].held = &fixture.HeldStatement{Executor: c, Statement: stmt, SQL: consumers[i].query}
					}
				})
				for _, consumer := range consumers {
					if consumer.held != nil {
						defer consumer.held.Statement.Close()
					}
				}
				seeded := false
				phase := func(allowed bool) {
					fixture.RoleGrantActor(run, "og_object_reader", func() {
						for _, consumer := range consumers {
							table := "og_bit_writes." + consumer.table
							if allowed && seeded {
								run(fixture.Exec("DELETE FROM "+table, 2))
							}
							var params []any
							if consumer.table != "d" {
								params = []any{"100", int64(0), int64(1)}
							}
							for _, held := range []bool{false, true} {
								if held && consumer.held == nil {
									continue // Constant DEFAULT is intentionally fresh-only.
								}
								s := fixture.Exec(consumer.query, 2, params...)
								if !allowed {
									s = fixture.ImplicitError(consumer.query, fixture.Diagnostic{Code: "42501", Message: "permission denied for function " + name}, params...)
									s.ErrorStage = "execute"
								}
								if held {
									fixture.RunStep(t, p.Ctx, *consumer.held, b.Recorder, s)
								} else {
									run(s)
								}
								ids := [][]any{{int64(1)}, {int64(2)}}
								run(fixture.Select("SELECT id FROM "+table+" ORDER BY id", []string{"id"}, []string{"INT4"}, ids))
								if consumer.table == "d" || consumer.table == "g" {
									run(fixture.Select("SELECT r FROM "+table+" ORDER BY id", []string{"r"}, []string{resultType}, [][]any{{expected}, {expected}}))
								} else {
									run(fixture.Select("SELECT v,n,k FROM "+table+" ORDER BY id", []string{"v", "n", "k"}, []string{"VARBIT", "INT4", "INT4"}, [][]any{{"100", int64(0), int64(1)}, {"100", int64(0), int64(1)}}))
								}
								if allowed {
									run(fixture.Exec("DELETE FROM "+table, 2))
								}
							}
							if allowed {
								// Keep actual rows across revoke, not merely an
								// empty destination whose contents cannot change.
								run(fixture.Exec(consumer.query, 2, params...))
							} else if consumer.table != "d" {
								q := "UPDATE " + table + " SET n=n+1"
								s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "42501", Message: "permission denied for function " + name})
								s.ErrorStage = "execute"
								run(s)
								run(fixture.Select("SELECT v,n,k FROM "+table+" ORDER BY id", []string{"v", "n", "k"}, []string{"VARBIT", "INT4", "INT4"}, [][]any{{"100", int64(0), int64(1)}, {"100", int64(0), int64(1)}}))
							}
						}
					})
					seeded = true
				}
				phase(true)
				run(fixture.Exec("REVOKE EXECUTE ON FUNCTION "+target+" FROM PUBLIC", 0))
				phase(false)
				run(fixture.Exec("GRANT EXECUTE ON FUNCTION "+target+" TO og_object_reader", 0))
				phase(true)
				run(fixture.Exec("REVOKE EXECUTE ON FUNCTION "+target+" FROM og_object_reader", 0))
				phase(false)
			})
		})
	}
}
