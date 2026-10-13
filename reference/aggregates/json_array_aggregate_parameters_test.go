package reference_test

import (
	"context"
	"database/sql"
	"reflect"
	"stuntgres/reference/fixture"
	"testing"
)

func jsonArrayAggregateHeld(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	t.Helper()
	for _, tc := range []struct{ q string; names, types []string; frames []struct{ arg any; row []any } }{
		{"SELECT json_agg($1::text ORDER BY id) AS a,json_agg_strict($1::text ORDER BY id) AS s FROM ja_input WHERE g='a'", []string{"a","s"}, []string{"JSON","JSON"}, []struct{ arg any; row []any }{
			{"x", []any{`["x", "x", "x"]`, `["x", "x", "x"]`}}, {nil, []any{`[null, null, null]`, `[]`}}, {"y", []any{`["y", "y", "y"]`, `["y", "y", "y"]`}},
		}},
		{"SELECT json_arrayagg($1 ORDER BY id) AS v FROM ja_input WHERE g='a'", []string{"v"}, []string{"JSON"}, []struct{ arg any; row []any }{
			{"x", []any{`["x", "x", "x"]`}}, {nil, []any{`[]`}}, {"y", []any{`["y", "y", "y"]`}},
		}},
		{"SELECT jsonb_agg_strict($1::json ORDER BY id) AS v FROM ja_input WHERE g='a'", []string{"v"}, []string{"JSONB"}, []struct{ arg any; row []any }{
			{nil, []any{`[]`}}, {"null", []any{`[null, null, null]`}}, {"1", []any{`[1, 1, 1]`}},
		}},
	} {
		t.Run(tc.q, func(t *testing.T) {
			stmt, err := b.DB.PrepareContext(p.Ctx, tc.q)
			if err != nil { t.Fatal(err) }
			defer stmt.Close()
			x := fixture.HeldStatement{b.DB, stmt, tc.q}
			for _, frame := range tc.frames { fixture.RunStep(t, p.Ctx, x, b.Recorder, fixture.Select(tc.q, tc.names, tc.types, [][]any{frame.row}, frame.arg)) }
		})
	}
	const q = "SELECT json_arrayagg($1::text FORMAT JSON ORDER BY id RETURNING jsonb) AS v FROM ja_input WHERE g='a'"
	stmt, err := b.DB.PrepareContext(p.Ctx, q)
	if err != nil { t.Fatal(err) }
	defer stmt.Close()
	x := fixture.HeldStatement{b.DB, stmt, q}
	for _, frame := range []struct{ arg any; want any }{{"[1]", "[[1], [1], [1]]"}, {"no", nil}, {nil, "[]"}, {"{}", "[{}, {}, {}]"}} {
		if frame.arg == "no" {
			d := fixture.ControlsReferenceDiagnostic("22P02", "invalid input syntax for type json")
			d.Detail, d.Context = `Token "no" is invalid.`, "JSON data, line 1: no"
			s := fixture.ErrorDiagnostic(q, d, frame.arg); s.SelectRows, s.ErrorStage = true, "execute"
			fixture.RunStep(t, p.Ctx, x, b.Recorder, s)
			// Exec on the SAME prepared statement cannot skip formatted conversion.
			s.SelectRows = false
			fixture.RunStep(t, p.Ctx, x, b.Recorder, s)
		} else { fixture.RunStep(t, p.Ctx, x, b.Recorder, jsonArrayAggregateSelect(q, "JSONB", frame.want, frame.arg)) }
	}
}

func TestReferenceJSONArrayAggregateParameters(t *testing.T) {
	jsonArrayAggregateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		jsonArrayAggregateSetup(t, p, b)
		jsonArrayAggregateHeld(t, p, b)
		const q = "SELECT json_agg(v ORDER BY id) AS v FROM ja_input WHERE g='a'"
		stmt, err := b.DB.PrepareContext(p.Ctx, q)
		if err != nil { t.Fatal(err) }
		defer stmt.Close()
		held, err := stmt.QueryContext(context.WithValue(p.Ctx, fixture.RecorderKey{}, b.Recorder))
		if err != nil { t.Fatal(err) }
		defer held.Close()
		before, err := held.ColumnTypes()
		if err != nil { t.Fatal(err) }
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("INSERT INTO ja_input VALUES(5,'a',9,NULL,NULL)", 1))
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{b.DB, stmt, q}, b.Recorder, jsonArrayAggregateSelect(q, "JSON", "[2, null, 1, 9]"))
		jsonArrayAggregateMetadata(t, held, b.PG, []string{"v"}, []string{"JSON"}, nil)
		after, err := held.ColumnTypes()
		if err != nil || !reflect.DeepEqual(before, after) { t.Fatalf("retained schema changed %v", err) }
		if !held.Next() { t.Fatal("held result missing", held.Err()) }
		var raw any
		if err = held.Scan(&raw); err != nil { t.Fatal(err) }
		fixture.AssertRawValue(t, b.PG, "JSON", raw)
		if fixture.Normalize(raw) != "[2, null, 1]" || held.Next() || held.Err() != nil { t.Fatalf("old cursor=%v err=%v", raw, held.Err()) }
		for _, prepared := range []bool{false, true} {
			s := jsonArrayAggregateSelect(q, "JSON", "[2, null, 1, 9]"); s.Prepared, s.NonContext = prepared, true
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
		}
		var value string
		if err := b.DB.QueryRow(q).Scan(&value); err != nil || value != "[2, null, 1, 9]" { t.Fatalf("QueryRow=%q/%v", value, err) }
		if err := stmt.QueryRowContext(p.Ctx).Scan(&value); err != nil || value != "[2, null, 1, 9]" { t.Fatalf("Stmt.QueryRowContext=%q/%v", value, err) }
		if err := stmt.QueryRow().Scan(&value); err != nil || value != "[2, null, 1, 9]" { t.Fatalf("Stmt.QueryRow=%q/%v", value, err) }
		if err := b.DB.QueryRowContext(p.Ctx, q).Scan(&value); err != nil || value != "[2, null, 1, 9]" { t.Fatalf("QueryRowContext=%q/%v", value, err) }
		t.Run("plain-discarded-conversion",func(t *testing.T) {
			const formatted = "SELECT json_arrayagg($1::text FORMAT JSON) AS v"
			for _, prepared := range []bool{false,true} {
				for _, query := range []bool{false,true} {
					t.Run(map[bool]string{false:"exec",true:"query"}[query]+"/"+map[bool]string{false:"direct",true:"prepared"}[prepared],func(t *testing.T) {
						d := fixture.ControlsReferenceDiagnostic("22P02","invalid input syntax for type json")
						d.Detail,d.Context = `Token "no" is invalid.`,"JSON data, line 1: no"
						s := fixture.ErrorDiagnostic(formatted,d,"no")
						s.Prepared,s.NonContext,s.SelectRows,s.ErrorStage = prepared,true,query,"execute"
						fixture.RunStep(t,p.Ctx,b.DB,b.Recorder,s)
						recovered := jsonArrayAggregateSelect(formatted,"JSON","[[1]]","[1]")
						recovered.Prepared,recovered.NonContext = prepared,true
						fixture.RunStep(t,p.Ctx,b.DB,b.Recorder,recovered)
					})
				}
			}
		})
	})
	jsonArrayAggregateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		conn, err := b.DB.Conn(p.Ctx)
		if err != nil { t.Fatal(err) }
		defer conn.Close()
		const q = "SELECT json_agg($1::timestamptz) AS v"
		stmt, err := conn.PrepareContext(p.Ctx, q)
		if err != nil { t.Fatal(err) }
		defer stmt.Close()
		for _, tc := range []struct{ zone, want string }{{"UTC", `["2024-01-02T03:04:05+00:00"]`}, {"America/New_York", `["2024-01-01T22:04:05-05:00"]`}, {"UTC", `["2024-01-02T03:04:05+00:00"]`}} {
			fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Exec("SET TIME ZONE '"+tc.zone+"'", 0))
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{conn,stmt,q}, b.Recorder, jsonArrayAggregateSelect(q,"JSON",tc.want,"2024-01-02 03:04:05+00"))
		}
		const byteaQuery = "SELECT json_agg($1::bytea) AS v"
		byteaStatement, err := conn.PrepareContext(p.Ctx, byteaQuery)
		if err != nil { t.Fatal(err) }
		defer byteaStatement.Close()
		for _, tc := range []struct{ setting, want string }{{"hex", `["\\x4142"]`}, {"escape", `["AB"]`}, {"hex", `["\\x4142"]`}} {
			fixture.RunStep(t,p.Ctx,conn,b.Recorder,fixture.Exec("SET bytea_output='"+tc.setting+"'",0))
			fixture.RunStep(t,p.Ctx,fixture.HeldStatement{conn,byteaStatement,byteaQuery},b.Recorder,jsonArrayAggregateSelect(byteaQuery,"JSON",tc.want,[]byte("AB")))
		}
		const floatQuery = "SELECT jsonb_agg($1::float8) AS v"
		floatStatement, err := conn.PrepareContext(p.Ctx, floatQuery)
		if err != nil { t.Fatal(err) }
		defer floatStatement.Close()
		for _, tc := range []struct{ setting, want string }{{"0", "[1.23456789012346]"}, {"3", "[1.234567890123456]"}, {"0", "[1.23456789012346]"}} {
			fixture.RunStep(t,p.Ctx,conn,b.Recorder,fixture.Exec("SET extra_float_digits="+tc.setting,0))
			fixture.RunStep(t,p.Ctx,fixture.HeldStatement{conn,floatStatement,floatQuery},b.Recorder,jsonArrayAggregateSelect(floatQuery,"JSONB",tc.want,"1.234567890123456"))
		}
		fixture.RunStep(t,p.Ctx,conn,b.Recorder,fixture.Exec("CREATE TABLE ja_identifier(v int)",0))
		jsonArrayAggregateRoutesOnConn(t,p,b,conn,"SELECT json_agg('ja_identifier'::regclass) AS v","JSON",`["ja_identifier"]`)
	})
}

func jsonArrayAggregateRoutesOnConn(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn,q,typ string,want any,args ...any) {
	t.Helper()
	for _, prepared := range []bool{false,true} { s:=jsonArrayAggregateSelect(q,typ,want,args...); s.Prepared=prepared; fixture.RunStep(t,p.Ctx,c,b.Recorder,s) }
}
