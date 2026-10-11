package reference_test

import (
	"context"
	"database/sql"
	"reflect"
	"stuntgres/reference/fixture"
	"testing"
)

// pq can return a cursor before execution produces an error. Drain the actual
// stream; native's eager publication barrier must instead expose no cursor.
func regexpTimeoutRows(t *testing.T, p *fixture.Pair, b fixture.Backend, conn *sql.Conn, stmt *sql.Stmt, q string) {
	t.Helper()
	ctx := context.WithValue(p.Ctx, fixture.RecorderKey{}, b.Recorder)
	b.Recorder.Mu.Lock()
	before := len(b.Recorder.Operations)
	b.Recorder.Mu.Unlock()
	var rows *sql.Rows
	var err error
	if stmt != nil {
		rows, err = stmt.QueryContext(ctx)
	} else {
		rows, err = conn.QueryContext(ctx, q)
	}
	name, typ, value := "s", "TEXT", "a"
	if q == `SELECT regexp_matches(s,p,'g') AS m FROM cancel_regexp` {
		name, typ, value = "m", "_TEXT", `{""}`
	}
	n := 0
	hadCursor := rows != nil
	if hadCursor {
		defer rows.Close()
		if !b.PG {
			t.Fatal("native timeout exposed partially published selected SRF cursor")
		}
		if err != nil {
			t.Fatal("failed query also returned cursor", err)
		}
		columns, columnErr := rows.Columns()
		if columnErr != nil {
			t.Fatal(columnErr)
		}
		if !reflect.DeepEqual(columns, []string{name}) {
			t.Fatal("timeout cursor columns", columns)
		}
		types, columnErr := rows.ColumnTypes()
		if columnErr != nil {
			t.Fatal(columnErr)
		}
		if len(types) != 1 || types[0].DatabaseTypeName() != typ {
			t.Fatal("timeout cursor types", types)
		}
		regexpMetadata(b.PG)(t, rows)
		for rows.Next() {
			var raw any
			if scanErr := rows.Scan(&raw); scanErr != nil {
				t.Fatal(scanErr)
			}
			fixture.AssertRawValue(t, b.PG, typ, raw)
			if fixture.Normalize(raw) != value {
				t.Fatalf("streamed timeout value %#v want %q", raw, value)
			}
			n++
		}
		err = rows.Err()
		if closeErr := rows.Close(); closeErr != nil && err == nil {
			err = closeErr
		}
	}
	if err == nil {
		t.Fatal("selected SRF finished without required statement timeout")
	}
	want := fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "57014", Message: "canceling statement due to statement timeout"}
	fixture.AssertDiagnostic(t, err, &want)
	b.Recorder.Mu.Lock()
	if len(b.Recorder.Operations) != before+1 {
		b.Recorder.Mu.Unlock()
		t.Fatal("timeout query middleware operation missing")
	}
	op := b.Recorder.Operations[before]
	b.Recorder.Mu.Unlock()
	if op.SQL != q {
		t.Fatalf("recorded timeout SQL %q want %q", op.SQL, q)
	}
	if op.Err == nil {
		t.Fatal("middleware lost streamed timeout")
	}
	fixture.AssertDiagnostic(t, op.Err, &want)
	if len(op.Rows) != n {
		t.Fatalf("middleware streamed rows %d want %d", len(op.Rows), n)
	}
	if hadCursor {
		if !op.Closed || !reflect.DeepEqual(op.Columns, []string{name}) {
			t.Fatal("middleware timeout cursor lifecycle", op)
		}
	} else if len(op.Columns) != 0 || len(op.Rows) != 0 {
		t.Fatal("failed query recorded partial output", op)
	}
	for _, r := range op.Rows {
		if len(r) != 1 {
			t.Fatal("recorded timeout row width", r)
		}
		fixture.AssertRawValue(t, b.PG, typ, r[0])
		if fixture.Normalize(r[0]) != value {
			t.Fatal("recorded timeout value", r[0])
		}
	}
}
