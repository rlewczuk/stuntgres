package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceXML2KRCNUTF7Lifecycle(t *testing.T) {
	xml2Case(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		xml2Install(t, p, b, c)
		kr := xml2RawDocument("ISO-2022-KR", "\x1b$)C\x0eGQ\x0f")
		cn := xml2RawDocument("ISO-2022-CN", "\x1b$)A\x0eVP\x0f")
		utf := xml2RawDocument("UTF-7", "+AOk-")
		bad := xml2KRCNUTF7Malformed()[0]
		q := xml2RawValues(kr, false, "한", float64(1), true).Query
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil { t.Fatal(err) }
		defer stmt.Close()
		h := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}

		// Retain the actual Rows and its consumed immutable values, metadata and
		// middleware operation across changing family/state/error/NULL inputs.
		rows, err := stmt.QueryContext(context.WithValue(p.Ctx, fixture.RecorderKey{}, b.Recorder), kr)
		if err != nil { t.Fatal(err) }
		defer rows.Close()
		columns, err := rows.Columns()
		if err != nil || !reflect.DeepEqual(columns, []string{"v", "s", "n", "b"}) { t.Fatal(columns, err) }
		meta, err := rows.ColumnTypes()
		if err != nil || len(meta) != 4 { t.Fatal(meta, err) }
		for i, want := range []string{"BOOL", "TEXT", "FLOAT4", "BOOL"} {
			if meta[i].DatabaseTypeName() != want { t.Fatalf("retained type %d: %s want %s", i, meta[i].DatabaseTypeName(), want) }
		}
		if !rows.Next() { t.Fatal("missing retained row", rows.Err()) }
		var valid, boolean bool
		var text string
		var length float64
		if err := rows.Scan(&valid, &text, &length, &boolean); err != nil { t.Fatal(err) }
		if valid || text != "한" || length != 1 || !boolean { t.Fatal("retained initial values", valid, text, length, boolean) }
		if rows.Next() || rows.Err() != nil { t.Fatal("retained tail", rows.Err()) }
		if err := rows.Close(); err != nil { t.Fatal(err) }
		b.Recorder.Mu.Lock()
		var retained *fixture.Operation
		for _, op := range b.Recorder.Operations { if op.SQL == q && op.Columns != nil { retained = op } }
		b.Recorder.Mu.Unlock()
		assertRetained := func() {
			t.Helper()
			if retained == nil || !retained.Closed || retained.CloseErr != nil || !reflect.DeepEqual(retained.Columns, []string{"v", "s", "n", "b"}) || len(retained.Rows) != 1 || len(retained.Rows[0]) != 4 { t.Fatal("retained middleware consumption/closure", retained) }
			values := make([]any, 4)
			for i, value := range retained.Rows[0] { values[i] = fixture.Normalize(value) }
			if !reflect.DeepEqual(values, []any{false, "한", float64(1), true}) { t.Fatal("retained middleware values changed", values) }
			if valid || text != "한" || length != 1 || !boolean || !reflect.DeepEqual(columns, []string{"v", "s", "n", "b"}) { t.Fatal("retained scanned values changed", valid, text, length, boolean, columns) }
			for i, want := range []string{"BOOL", "TEXT", "FLOAT4", "BOOL"} {
				if meta[i].DatabaseTypeName() != want { t.Fatalf("later call changed retained type %d: %s", i, meta[i].DatabaseTypeName()) }
			}
			if rows.Next() || rows.Err() != nil { t.Fatal("retained exhausted Rows changed", rows.Err()) }
		}
		assertRetained()
		for _, step := range []fixture.Step{
			xml2RawValues(bad, false, nil, nil, false),
			xml2RawValues(cn, false, "中", float64(1), true),
			xml2RawValues(nil, nil, nil, nil, nil),
			xml2RawValues(utf, true, "é", float64(1), true),
			xml2RawValues(kr, false, "한", float64(1), true),
		} { fixture.RunStep(t, p.Ctx, h, b.Recorder, step); assertRetained() }

		// A held path-bearing statement alternates independently owned source
		// and stylesheet documents. The selected node is literal out, not XSLT.
		compileQuery := xml2Scalars(kr, ".", "한", nil, true).Query
		compileFailure := fixture.ErrorDiagnostic(compileQuery, xml2Compile("Invalid expression"), cn, "[")
		compileFailure.SelectRows, compileFailure.ErrorStage = true, "execute"
		xml2Held(t, p, b, c,
			xml2Scalars(kr, ".", "한", nil, true),
			xml2Scalars(xml2KRCNUTF7Style("UTF-7", "+AOk-"), "/*/*/*", "é", nil, true),
			compileFailure,
			xml2Scalars(bad, "[", nil, nil, false),
			xml2Scalars(xml2KRCNUTF7Style("ISO-2022-CN", "\x1b$)G\x0eVP\x1bN!!VP\x0f"), "/*/*/*", "烘乂烘", nil, true),
			xml2Scalars(nil, "[", nil, nil, nil),
			xml2Scalars(utf, ".", "é", nil, true),
			xml2Scalars(xml2KRCNUTF7Style("ISO-2022-KR", "\x0eGQ\x0f"), "/*/*/*", "한", nil, true),
			xml2Scalars(cn, ".", "中", nil, true))
		assertRetained()

		// Pre-canceled database/sql pool admission has ordinary context causes.
		// An already borrowed connection can race its driver's cancellation
		// watcher, so exercise deterministic connection admission in both modes.
		poolStmt, err := b.DB.PrepareContext(p.Ctx, q)
		if err != nil { t.Fatal(err) }
		defer poolStmt.Close()
		for _, prepared := range []bool{false, true} {
			ctx, cancel := context.WithCancel(p.Ctx)
			cancel()
			var canceledRows *sql.Rows
			if prepared { canceledRows, err = poolStmt.QueryContext(ctx, utf) } else { canceledRows, err = b.DB.QueryContext(ctx, q, utf) }
			if canceledRows != nil { canceledRows.Close() }
			if !errors.Is(err, context.Canceled) { t.Fatal("ordinary pre-cancellation cause", prepared, err) }
			var state interface { SQLState() string }
			if errors.As(err, &state) { t.Fatal("context error acquired SQLSTATE", state.SQLState()) }
			fixture.RunStep(t, p.Ctx, h, b.Recorder, xml2RawValues(utf, true, "é", float64(1), true))
			assertRetained()
		}
		xml2Both(t, p, b, c, xml2RawValues(cn, false, "中", float64(1), true))
		assertRetained()
	})
}
