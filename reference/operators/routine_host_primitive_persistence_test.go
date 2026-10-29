package reference_test

import (
	"bytes"
	"database/sql"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceRoutineHostPrimitivePersistence(t *testing.T) {
	hostPrimitiveScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		defer routineLanguagesCleanup(t, c, "DROP OWNED BY hp_reader CASCADE", "DROP ROLE hp_reader")()
		hostExec(t, p, b, c, "CREATE ROLE hp_reader")
		for _, tc := range []struct{ name, sig, result, program, python string }{
			{"hp_p_bool", "v boolean", "boolean", "bool-not-v1", "return None if v is None else not v"},
			{"hp_p_i4", "v integer", "integer", "int4-offset-v1", "return None if v is None else v+1"},
			{"hp_p_i8", "v bigint", "bigint", "int8-offset-v1", "return None if v is None else v+5"},
			{"hp_p_text", "v text", "text", "text-mark-v1", "return None if v is None else v+'!'"},
			{"hp_p_zero", "", "bigint", "zero-v1", "return 17"},
			{"hp_p_add", "a bigint,b bigint", "bigint", "int8-add-v1", "return None if a is None or b is None else a+b"},
		} {
			hostPrimitiveFunction(t, p, b, c, tc.name, tc.sig, tc.result, tc.program, tc.python, "")
		}
		hostExec(t, p, b, c, "CREATE TABLE hp_p_data(b boolean DEFAULT hp_p_bool(true),i integer DEFAULT hp_p_i4(-1),j bigint DEFAULT hp_p_zero(),s text DEFAULT hp_p_text(''))")
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO hp_p_data DEFAULT VALUES", 1))
		hostExec(t, p, b, c, "CREATE VIEW hp_p_view AS SELECT hp_p_bool(b) AS b,hp_p_i4(i) AS i,hp_p_i8(j) AS j,hp_p_text(s) AS s FROM hp_p_data")
		hostPrimitivePersistenceResults(t, p, b, c)
		hostPrimitivePersistenceHeld(t, p, b, c)
		if b.PG {
			return
		}
		var stream bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &stream); err != nil {
			t.Fatal(err)
		}
		if stream.Len() == 0 || !strings.Contains(stream.String(), "stuntgres-host-runtime-v1 ") {
			t.Fatal("missing logical primitive prerequisites")
		}
		for round := range 2 {
			d := hostPrimitiveDefinition()
			raw, destination := hostOpenNative(t, &d)
			hostExec(t, p, destination, destination.DB, "CREATE ROLE hp_reader")
			if err := stuntgres.Restore(p.Ctx, raw, bytes.NewReader(stream.Bytes())); err != nil {
				t.Fatalf("fresh matching driver round%d: %v", round, err)
			}
			current, err := destination.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			hostPrimitivePersistenceResults(t, p, destination, current)
			hostPrimitivePersistenceHeld(t, p, destination, current)
			current.Close()
			var next bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, raw, &next); err != nil {
				t.Fatal(err)
			}
			stream.Reset()
			stream.Write(next.Bytes())
		}
		for _, kind := range []string{"absent", "abi", "program", "conversion", "signature"} {
			t.Run(kind, func(t *testing.T) {
				d := hostPrimitiveDefinition()
				switch kind {
				case "abi":
					d.ABIVersion = "incompatible"
				case "program":
					d.Programs[0].Version = "incompatible"
				case "conversion":
					d.Conversions[0].ToName.Name = "different_bool_to"
				case "signature":
					d.Programs[0].Inputs[0] = stuntgres.HostText
					d.Programs[0].InputConversions[0] = "text"
				}
				definition := &d
				if kind == "absent" {
					definition = nil
				}
				raw, destination := hostOpenNative(t, definition)
				hostExec(t, p, destination, destination.DB, "CREATE ROLE hp_reader")
				current, err := destination.DB.Conn(p.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer current.Close()
				hostExec(t, p, destination, current, "CREATE TABLE hp_sentinel(b boolean,i integer,j bigint,s text)")
				fixture.RunStep(t, p.Ctx, current, destination.Recorder, fixture.Exec("INSERT INTO hp_sentinel VALUES (false,0,71,'')", 1))
				q := "SELECT b,i,j,s FROM hp_sentinel"
				stmt, err := current.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				rows, err := stmt.QueryContext(p.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				cols, err := rows.ColumnTypes()
				if err != nil {
					t.Fatal(err)
				}
				rows.Close()
				if err := stuntgres.Restore(p.Ctx, raw, bytes.NewReader(stream.Bytes())); err == nil {
					t.Fatal("incompatible primitive provider replaced target")
				}
				held := fixture.HeldStatement{Executor: current, Statement: stmt, SQL: q}
				hostRows(t, p, destination, held, q, []string{"b", "i", "j", "s"}, []string{"BOOL", "INT4", "INT8", "TEXT"}, [][]any{{false, int64(0), int64(71), ""}})
				hostRows(t, p, destination, current, q, []string{"b", "i", "j", "s"}, []string{"BOOL", "INT4", "INT8", "TEXT"}, [][]any{{false, int64(0), int64(71), ""}})
				for i, typ := range []string{"BOOL", "INT4", "INT8", "TEXT"} {
					if cols[i].DatabaseTypeName() != typ {
						t.Errorf("retained column%d type=%s", i, cols[i].DatabaseTypeName())
					}
				}
			})
		}
	})
}

func hostPrimitivePersistenceResults(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	hostRows(t, p, b, c, "SELECT b,i,j,s FROM hp_p_data", []string{"b", "i", "j", "s"}, []string{"BOOL", "INT4", "INT8", "TEXT"}, [][]any{{false, int64(0), int64(17), "!"}})
	hostRows(t, p, b, c, "SELECT b,i,j,s FROM hp_p_view", []string{"b", "i", "j", "s"}, []string{"BOOL", "INT4", "INT8", "TEXT"}, [][]any{{true, int64(1), int64(22), "!!"}})
	hostRows(t, p, b, c, "SELECT hp_p_zero() AS v", []string{"v"}, []string{"INT8"}, [][]any{{int64(17)}})
	hostRows(t, p, b, c, "SELECT hp_p_add(2::bigint,3::bigint) AS v", []string{"v"}, []string{"INT8"}, [][]any{{int64(5)}})
	for _, tc := range []struct{ expression, typ string }{{"hp_p_bool(false)", "BOOL"}, {"hp_p_i4(0)", "INT4"}, {"hp_p_i8(7::bigint)", "INT8"}, {"hp_p_text('λ')", "TEXT"}} {
		hostPrimitiveMetadata(t, p, b, c, "SELECT "+tc.expression+" AS v", tc.typ)
		hostPrimitiveMetadata(t, p, b, c, "SELECT "+tc.expression+" AS v WHERE false", tc.typ)
	}
}

func hostPrimitivePersistenceHeld(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	// Isolate function EXECUTE from schema name visibility in every replay owner.
	hostExec(t, p, b, c, "GRANT USAGE ON SCHEMA public TO hp_reader")
	for _, tc := range []struct {
		name, cast, typ string
		input, output   []any
	}{
		{"hp_p_bool", "boolean", "BOOL", []any{false, true}, []any{true, false}},
		{"hp_p_i4", "integer", "INT4", []any{int64(0), int64(-1)}, []any{int64(1), int64(0)}},
		{"hp_p_i8", "bigint", "INT8", []any{int64(6), int64(-5)}, []any{int64(11), int64(0)}},
		{"hp_p_text", "text", "TEXT", []any{"", "λ"}, []any{"!", "λ!"}},
	} {
		q := "SELECT " + tc.name + "($1::" + tc.cast + ") AS v"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		for i, input := range tc.input {
			hostRows(t, p, b, c, q, []string{"v"}, []string{tc.typ}, [][]any{{tc.output[i]}}, input)
			hostRows(t, p, b, held, q, []string{"v"}, []string{tc.typ}, [][]any{{tc.output[i]}}, input)
		}
		hostExec(t, p, b, c, "REVOKE EXECUTE ON FUNCTION "+tc.name+"("+tc.cast+") FROM PUBLIC")
		hostExec(t, p, b, c, "REVOKE ALL ON FUNCTION "+tc.name+"("+tc.cast+") FROM hp_reader")
		hostExec(t, p, b, c, "SET ROLE hp_reader")
		var value any
		err = stmt.QueryRowContext(p.Ctx, tc.input[0]).Scan(&value)
		d := fixture.ControlsReferenceDiagnostic("42501", "permission denied for function "+tc.name)
		fixture.AssertDiagnostic(t, err, &d)
		hostExec(t, p, b, c, "RESET ROLE")
		hostExec(t, p, b, c, "GRANT EXECUTE ON FUNCTION "+tc.name+"("+tc.cast+") TO hp_reader")
		hostExec(t, p, b, c, "SET ROLE hp_reader")
		hostRows(t, p, b, held, q, []string{"v"}, []string{tc.typ}, [][]any{{tc.output[1]}}, tc.input[1])
		hostExec(t, p, b, c, "RESET ROLE")
		stmt.Close()
	}
}
