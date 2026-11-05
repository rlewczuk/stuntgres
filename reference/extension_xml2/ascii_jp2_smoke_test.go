package reference_test

import (
	"context"
	"database/sql"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceXML2ASCIIJP2FeatureSmoke(t *testing.T) {
	xml2Case(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		xml2Install(t, p, b, c)
		for _, tc := range []struct { label, body, text string; valid bool }{
			{"ASCII", "A", "A", true}, {"US-ASCII", "A", "A", true},
			{"ISO-2022-JP-2", "\x1b$(D+1\x1b(B", "é", false},
			{"ISO-2022-JP-2", "\x1b$(CGQ\x1b(B", "한", false},
		} {
			xml2Both(t, p, b, c, xml2RawValues(xml2RawDocument(tc.label, tc.body), tc.valid, tc.text, float64(1), true))
		}
		mixed := xml2RawDocument("ISO-2022-JP-2", "\x1b.A\x1bNi\x1b$(CGQ\x1b(B\x1bNq")
		q := xml2RawValues(mixed, false, "é한ñ", float64(3), true).Query
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil { t.Fatal(err) }
		defer stmt.Close()
		h := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}

		// Keep a real Rows object and the values/metadata scanned from it alive
		// across later executions. Exhaust the physical stream before reuse.
		rows, err := stmt.QueryContext(context.WithValue(p.Ctx, fixture.RecorderKey{}, b.Recorder), mixed)
		if err != nil { t.Fatal(err) }
		defer rows.Close()
		columns, err := rows.Columns()
		if err != nil || !reflect.DeepEqual(columns, []string{"v", "s", "n", "b"}) { t.Fatal(columns, err) }
		meta, err := rows.ColumnTypes()
		if err != nil || len(meta) != 4 { t.Fatal(meta, err) }
		for i, want := range []string{"BOOL", "TEXT", "FLOAT4", "BOOL"} {
			if meta[i].DatabaseTypeName() != want { t.Fatalf("type %d: %s want %s", i, meta[i].DatabaseTypeName(), want) }
		}
		if !rows.Next() { t.Fatal("missing retained row", rows.Err()) }
		var v, boolean bool
		var text string
		var length float64
		if err := rows.Scan(&v, &text, &length, &boolean); err != nil { t.Fatal(err) }
		if rows.Next() || rows.Err() != nil { t.Fatal("retained tail", rows.Err()) }
		if err := rows.Close(); err != nil { t.Fatal(err) }
		b.Recorder.Mu.Lock()
		var retained *fixture.Operation
		for _, op := range b.Recorder.Operations {
			if op.SQL == q && op.Columns != nil { retained = op }
		}
		b.Recorder.Mu.Unlock()
		if retained == nil || !retained.Closed || retained.CloseErr != nil || !reflect.DeepEqual(retained.Columns, columns) || len(retained.Rows) != 1 || len(retained.Rows[0]) != 4 { t.Fatal("retained middleware consumption/closure", retained) }
		recorded := make([]any, 4)
		for i, value := range retained.Rows[0] { recorded[i] = fixture.Normalize(value) }
		if !reflect.DeepEqual(recorded, []any{false, "é한ñ", float64(3), true}) { t.Fatal("retained middleware values", recorded) }
		for _, step := range []fixture.Step{
			xml2RawValues(xml2RawDocument("ISO-2022-JP-2", "\x1b$X"), false, nil, nil, false),
			xml2RawValues(xml2RawDocument("ISO-2022-JP-2", "\x1b$(D+1\x1b(B"), false, "é", float64(1), true),
			xml2RawValues(nil, nil, nil, nil, nil),
			xml2RawValues(xml2RawDocument("US-ASCII", "A"), true, "A", float64(1), true),
			xml2RawValues(mixed, false, "é한ñ", float64(3), true),
		} { fixture.RunStep(t, p.Ctx, h, b.Recorder, step) }
		if v || text != "é한ñ" || length != 3 || !boolean || columns[1] != "s" || meta[2].DatabaseTypeName() != "FLOAT4" { t.Fatal("later calls changed retained values", v, text, length, boolean, columns, meta) }
		xml2Both(t, p, b, c, xml2RawValues(xml2RawDocument("ASCII", "é"), true, nil, nil, false))
		xml2Both(t, p, b, c, xml2Scalars(xml2RawDocument("ISO-2022-JP-2", "\x1b$X"), "[", nil, nil, false))
		xml2Failure(t, p, b, c, "SELECT xpath_string($1::text,$2::text)", xml2Compile("Invalid expression"), false, mixed, "[")
		xml2Both(t, p, b, c, xml2Scalars(mixed, ".", "é한ñ", nil, true))
		compileQuery := "SELECT xpath_string($1::text,$2::text) AS s"
		compileFailure := fixture.ErrorDiagnostic(compileQuery, xml2Compile("Invalid expression"), mixed, "[")
		compileFailure.SelectRows, compileFailure.ErrorStage = true, "execute"
		xml2Held(t, p, b, c,
			fixture.Select(compileQuery, []string{"s"}, []string{"TEXT"}, [][]any{{"é한ñ"}}, mixed, "."),
			compileFailure,
			fixture.Select(compileQuery, []string{"s"}, []string{"TEXT"}, [][]any{{nil}}, xml2RawDocument("ISO-2022-JP-2", "\x1b$X"), "["),
			fixture.Select(compileQuery, []string{"s"}, []string{"TEXT"}, [][]any{{"é한ñ"}}, mixed, "."))
		if !retained.Closed || retained.CloseErr != nil || !reflect.DeepEqual(retained.Columns, columns) { t.Fatal("later statements changed retained middleware record") }
	})
}
