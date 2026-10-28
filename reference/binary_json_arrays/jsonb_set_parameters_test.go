package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func jsonbSetHeldModes(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	q := "SELECT jsonb_set_lax($1,$2,$3,$4,$5) AS changed"
	stmt, err := c.PrepareContext(p.Ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
	run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, held, b.Recorder, s) }
	good := jsonbSetSelect(q, `{"a": 2, "b": 2}`, `{"a":1,"b":2}`, "{a}", "2", true, "ignored")
	good.CheckMetadata = func(t *testing.T, r *sql.Rows) { fixture.JSONBObjectMetadata(t, r, b.PG, "changed") }
	run(good)
	fixture.RunStep(t, p.Ctx, c, b.Recorder, good)
	for _, tc := range []struct{ mode, want string }{
		{"use_json_null", `{"a": null, "b": 2}`}, {"delete_key", `{"b": 2}`}, {"return_target", `{"a": 1, "b": 2}`},
	} {
		run(jsonbSetSelect(q, tc.want, `{"a":1,"b":2}`, "{a}", nil, true, tc.mode))
	}
	run(jsonbSetError(q, jsonbSetRaise(), `{"a":1}`, "{a}", nil, true, "raise_exception"))
	run(good)
	run(jsonbSetError(q, fixture.ControlsReferenceDiagnostic("22P02", `path element at position 1 is not an integer: "x"`), "[1]", "{x}", "2", true, "return_target"))
	run(good)
	run(jsonbSetSelect(q, nil, nil, "{a}", nil, true, nil))
	run(jsonbSetSelect(q, nil, `{"a":1}`, nil, "2", true, "bad"))
	run(jsonbSetSelect(q, nil, `{"a":1}`, "{a}", nil, nil, "raise_exception"))
	d := fixture.ControlsReferenceDiagnostic("22023", `null_value_treatment must be "delete_key", "return_target", "use_json_null", or "raise_exception"`)
	run(jsonbSetError(q, d, `{"a":1}`, "{a}", "2", true, nil))
	run(good)
	for _, tc := range []struct {
		slot                     int
		input, code, msg, detail string
	}{
		{1, "{a,,b}", "22P02", `malformed array literal: "{a,,b}"`, `Unexpected "," character.`},
		{3, "bad", "22P02", `invalid input syntax for type boolean: "bad"`, ""},
	} {
		args := []any{`{"a":1}`, "{a}", "2", true, "use_json_null"}
		args[tc.slot] = tc.input
		d := fixture.ControlsReferenceDiagnostic(tc.code, tc.msg)
		d.Detail = tc.detail
		if tc.slot == 1 {
			d.Context = "unnamed portal parameter $2 = '...'"
		} else {
			d.Context = "unnamed portal parameter $4 = '...'"
		}
		run(jsonbSetError(q, d, args...))
		run(good)
	}
	for _, slot := range []int{0, 2} {
		args := []any{`{"a":1}`, "{a}", "2", true, "use_json_null"}
		args[slot] = "TRUE"
		d := fixture.ControlsReferenceDiagnostic("22P02", "invalid input syntax for type json")
		d.Detail = `Token "TRUE" is invalid.`
		d.Context = "JSON data, line 1: TRUE\nunnamed portal parameter $1 = '...'"
		if slot == 2 {
			d.Context = "JSON data, line 1: TRUE\nunnamed portal parameter $3 = '...'"
		}
		run(jsonbSetError(q, d, args...))
		run(good)
	}
	owned := []byte(`{"a":1,"b":2}`)
	rows, err := stmt.QueryContext(p.Ctx, owned, []byte("{a}"), []byte("2"), true, []byte("use_json_null"))
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	if !rows.Next() {
		t.Fatal("missing byte-input row", rows.Err())
	}
	var retained string
	if err := rows.Scan(&retained); err != nil {
		t.Fatal(err)
	}
	for i := range owned {
		owned[i] = 'x'
	}
	var again string
	if err := rows.Scan(&again); err != nil || again != `{"a": 2, "b": 2}` {
		t.Fatalf("retained=%q err=%v", again, err)
	}
	if rows.Next() || rows.Err() != nil {
		t.Fatal("unexpected extra row", rows.Err())
	}
	rows.Close()
	if retained != `{"a": 2, "b": 2}` {
		t.Fatal(retained)
	}
	run(good)
}

func TestReferenceJSONBSetParameters(t *testing.T) {
	jsonbSetScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		jsonbSetHeldModes(t, p, b, c)
		for _, q := range []string{"SELECT jsonb_set($1,$2,$3) AS changed", "SELECT jsonb_set($1,$2,$3,$4) AS changed", "SELECT jsonb_set_lax($1,$2,$3) AS changed"} {
			t.Run(q, func(t *testing.T) {
				stmt, err := c.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
				args := []any{`{"a":1}`, "{a}", "2"}
				if q == "SELECT jsonb_set($1,$2,$3,$4) AS changed" {
					args = append(args, true)
				}
				fixture.RunStep(t, p.Ctx, held, b.Recorder, jsonbSetSelect(q, `{"a": 2}`, args...))
				args[2] = nil
				var want any
				if q == "SELECT jsonb_set_lax($1,$2,$3) AS changed" {
					want = `{"a": null}`
				}
				fixture.RunStep(t, p.Ctx, held, b.Recorder, jsonbSetSelect(q, want, args...))
			})
		}
	})
}
