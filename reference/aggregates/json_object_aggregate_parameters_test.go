package reference_test

import (
	"context"
	"reflect"
	"stuntgres/reference/fixture"
	"testing"
)

func jsonObjectAggregateHeld(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	t.Helper()
	for _, f := range jsonObjectAggregateFamilies {
		t.Run(f.name, func(t *testing.T) {
			q := "SELECT " + f.name + "($1::text,$2::int) AS v"
			stmt, err := b.DB.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			x := fixture.HeldStatement{b.DB, stmt, q}
			for _, plain := range []bool{false, true} {
				for _, frame := range []struct {
					k, v any
					want any
					bad  bool
				}{
					{"a", 1, f.singleton, false}, {"a", nil, f.nullValue, false}, {nil, nil, nil, true}, {"a", 1, f.singleton, false},
				} {
					if frame.bad {
						for _, query := range []bool{true, false} {
							s := fixture.ErrorDiagnostic(q, jsonObjectAggregateNullKey(f.typ), frame.k, frame.v)
							s.SelectRows, s.ErrorStage, s.NonContext = query, "execute", plain
							fixture.RunStep(t, p.Ctx, x, b.Recorder, s)
						}
					} else {
						s := jsonArrayAggregateSelect(q, f.typ, frame.want, frame.k, frame.v)
						s.NonContext = plain
						fixture.RunStep(t, p.Ctx, x, b.Recorder, s)
					}
				}
			}
			want := `{ "b" : 2 }`
			if f.typ == "JSONB" {
				want = `{"b": 2}`
			}
			fixture.RunStep(t, p.Ctx, x, b.Recorder, jsonArrayAggregateSelect(q, f.typ, want, "b", 2))
		})
	}
}

func TestReferenceJSONObjectAggregateParameters(t *testing.T) {
	jsonObjectAggregateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		jsonObjectAggregateSetup(t, p, b)
		jsonObjectAggregateHeld(t, p, b)
		for _, f := range jsonObjectAggregateFamilies {
			for _, args := range []string{"$1,1", "'a',$1"} {
				q := "SELECT " + f.name + "(" + args + ") AS v"
				jsonArrayAggregateError(t, p, b, q, fixture.ControlsReferenceDiagnostic("42P18", "could not determine data type of parameter $1"), true, "x")
			}
			for _, args := range []string{"$1,1", "'a',$1"} {
				q := "/*é😀*/ SELECT " + f.name + "(" + args + ") AS v,$1::int AS n"
				d := fixture.ControlsReferenceDiagnostic("42P08", "could not determine data type of parameter $1")
				d.Position = jsonArrayAggregatePosition(q, "$1")
				jsonArrayAggregateError(t, p, b, q, d, true, 2)
			}
			want := `{ "2" : 2 }`
			if f.typ == "JSONB" {
				want = `{"2": 2}`
			}
			jsonArrayAggregateRoutes(t, p, b, fixture.Select("SELECT $1::int AS n,"+f.name+"($1,$1) AS v", []string{"n", "v"}, []string{"INT4", f.typ}, [][]any{{int64(2), want}}, 2))
			want = `{ "a" : null }`
			if f.typ == "JSONB" {
				want = `{"a": null}`
			}
			// Pinned PG18.0 omits converted JSON null in binary strict mode.
			if f.name == "jsonb_object_agg_strict" {
				want = `{}`
			}
			jsonArrayAggregateRoutes(t, p, b, jsonArrayAggregateSelect("SELECT "+f.name+"($1::text,$2::json) AS v", f.typ, want, "a", "null"))
		}
		const q = "SELECT json_object_agg(k,v ORDER BY id) AS v FROM joa_input"
		stmt, err := b.DB.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held, err := stmt.QueryContext(context.WithValue(p.Ctx, fixture.RecorderKey{}, b.Recorder))
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		before, err := held.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("INSERT INTO joa_input VALUES(4,'b','c',9,NULL)", 1))
		const fresh = `{ "b" : 1, "a" : 2, "a" : null, "c" : 9 }`
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{b.DB, stmt, q}, b.Recorder, jsonArrayAggregateSelect(q, "JSON", fresh))
		jsonArrayAggregateMetadata(t, held, b.PG, []string{"v"}, []string{"JSON"}, nil)
		after, err := held.ColumnTypes()
		if err != nil || !reflect.DeepEqual(before, after) {
			t.Fatalf("retained metadata=%v err=%v", after, err)
		}
		if !held.Next() {
			t.Fatal("retained row missing", held.Err())
		}
		var raw any
		if err = held.Scan(&raw); err != nil {
			t.Fatal(err)
		}
		fixture.AssertRawValue(t, b.PG, "JSON", raw)
		if fixture.Normalize(raw) != `{ "b" : 1, "a" : 2, "a" : null }` || held.Next() || held.Err() != nil {
			t.Fatalf("retained row=%#v err=%v", raw, held.Err())
		}
		for _, prepared := range []bool{false, true} {
			s := jsonArrayAggregateSelect(q, "JSON", fresh)
			s.Prepared, s.NonContext = prepared, true
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
		}
		var got string
		if err = b.DB.QueryRow(q).Scan(&got); err != nil || got != fresh {
			t.Fatalf("QueryRow=%q/%v", got, err)
		}
		if err = b.DB.QueryRowContext(p.Ctx, q).Scan(&got); err != nil || got != fresh {
			t.Fatalf("QueryRowContext=%q/%v", got, err)
		}
		if err = stmt.QueryRow().Scan(&got); err != nil || got != fresh {
			t.Fatalf("Stmt.QueryRow=%q/%v", got, err)
		}
		if err = stmt.QueryRowContext(p.Ctx).Scan(&got); err != nil || got != fresh {
			t.Fatalf("Stmt.QueryRowContext=%q/%v", got, err)
		}
	})
	jsonObjectAggregateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		conn, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		for _, tc := range []struct {
			q, typ string
			frames []struct {
				setting string
				args    []any
				want    string
			}
		}{
			{"SELECT json_object_agg($1::timestamptz,$2::timestamptz) AS v", "JSON", []struct {
				setting string
				args    []any
				want    string
			}{
				{"SET TIME ZONE 'UTC'", []any{"2024-01-02 03:04:05+00", "2024-01-02 03:04:05+00"}, `{ "2024-01-02T03:04:05+00:00" : "2024-01-02T03:04:05+00:00" }`},
				{"SET TIME ZONE 'America/New_York'", []any{"2024-01-02 03:04:05+00", "2024-01-02 03:04:05+00"}, `{ "2024-01-01T22:04:05-05:00" : "2024-01-01T22:04:05-05:00" }`},
				{"SET TIME ZONE 'UTC'", []any{"2024-01-02 03:04:05+00", "2024-01-02 03:04:05+00"}, `{ "2024-01-02T03:04:05+00:00" : "2024-01-02T03:04:05+00:00" }`},
			}},
			{"SELECT json_object_agg($1::bytea,$2::bytea) AS v", "JSON", []struct {
				setting string
				args    []any
				want    string
			}{
				{"SET bytea_output='hex'", []any{[]byte("AB"), []byte("AB")}, `{ "\\x4142" : "\\x4142" }`},
				{"SET bytea_output='escape'", []any{[]byte("AB"), []byte("AB")}, `{ "AB" : "AB" }`},
				{"SET bytea_output='hex'", []any{[]byte("AB"), []byte("AB")}, `{ "\\x4142" : "\\x4142" }`},
			}},
			{"SELECT jsonb_object_agg('a',$1::float8) AS v", "JSONB", []struct {
				setting string
				args    []any
				want    string
			}{
				{"SET extra_float_digits=0", []any{"1.234567890123456"}, `{"a": 1.23456789012346}`},
				{"SET extra_float_digits=3", []any{"1.234567890123456"}, `{"a": 1.234567890123456}`},
				{"SET extra_float_digits=0", []any{"1.234567890123456"}, `{"a": 1.23456789012346}`},
			}},
		} {
			stmt, err := conn.PrepareContext(p.Ctx, tc.q)
			if err != nil {
				t.Fatal(err)
			}
			for _, frame := range tc.frames {
				fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Exec(frame.setting, 0))
				fixture.RunStep(t, p.Ctx, fixture.HeldStatement{conn, stmt, tc.q}, b.Recorder, jsonArrayAggregateSelect(tc.q, tc.typ, frame.want, frame.args...))
			}
			if err = stmt.Close(); err != nil {
				t.Fatal(err)
			}
		}
		fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Exec("CREATE TABLE joa_identifier(v int)", 0))
		jsonArrayAggregateRoutesOnConn(t, p, b, conn, "SELECT json_object_agg('joa_identifier'::regclass,'joa_identifier'::regclass) AS v", "JSON", `{ "joa_identifier" : "joa_identifier" }`)
	})
}
