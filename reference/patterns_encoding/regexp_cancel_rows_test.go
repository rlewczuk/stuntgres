package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"reflect"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

type regexpCanceledStream struct {
	err, validation error
	cursor          bool
	columns         []string
	types           []*sql.ColumnType
	count           int
}

// The pinned transport streams: admission and server cancellation are real,
// but a RowDescription/cursor or a valid prefix is not native publication.
func regexpCancelPostgresRows(t *testing.T, p *fixture.Pair, b fixture.Backend, conn *sql.Conn, prepared bool, q string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(p.Ctx, 15*time.Second)
	defer cancel()
	var pid int
	if err := conn.QueryRowContext(ctx, `SELECT pg_backend_pid()`).Scan(&pid); err != nil {
		t.Fatal(err)
	}
	var stmt *sql.Stmt
	if prepared {
		var err error
		stmt, err = conn.PrepareContext(ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
	}
	name, typ, value, maxRows := "s", "TEXT", "a", 2*1024*1024
	if q == `SELECT regexp_matches(s,p,'g') AS m FROM cancel_regexp` {
		name, typ, value, maxRows = "m", "_TEXT", `{""}`, maxRows+1
	}
	b.Recorder.Mu.Lock()
	before := len(b.Recorder.Operations)
	b.Recorder.Mu.Unlock()
	queryCtx := context.WithValue(ctx, fixture.RecorderKey{}, b.Recorder)
	result := make(chan regexpCanceledStream, 1)
	joined := false
	defer func() {
		cancel()
		if !joined {
			select {
			case <-result:
			case <-time.After(5 * time.Second):
				t.Error("stream cancellation worker failed to join")
			}
		}
	}()
	go func() {
		var out regexpCanceledStream
		var rows *sql.Rows
		if stmt != nil {
			rows, out.err = stmt.QueryContext(queryCtx)
		} else {
			rows, out.err = conn.QueryContext(queryCtx, q)
		}
		if rows != nil {
			out.cursor = true
			if out.err != nil {
				out.validation = fmt.Errorf("failed query also returned cursor: %v", out.err)
			}
			var err error
			out.columns, err = rows.Columns()
			if err != nil && out.validation == nil {
				out.validation = err
			}
			out.types, err = rows.ColumnTypes()
			if err != nil && out.validation == nil {
				out.validation = err
			}
			for rows.Next() {
				var raw any
				if err := rows.Scan(&raw); err != nil {
					if out.validation == nil {
						out.validation = err
					}
					continue
				}
				valid := false
				if typ == "TEXT" {
					s, ok := raw.(string)
					valid = ok && s == value
				} else {
					s, ok := raw.([]byte)
					valid = ok && s != nil && string(s) == value
				}
				if !valid && out.validation == nil {
					out.validation = fmt.Errorf("streamed canceled %s value %T %#v want %q", typ, raw, raw, value)
				}
				out.count++
			}
			out.err = rows.Err()
			if closeErr := rows.Close(); closeErr != nil && out.err == nil {
				out.err = closeErr
			}
		}
		result <- out
	}()
	join := func() regexpCanceledStream {
		select {
		case out := <-result:
			joined = true
			return out
		case <-ctx.Done():
			cancel()
			select {
			case out := <-result:
				joined = true
				return out
			case <-time.After(5 * time.Second):
				t.Fatal("cancellation worker failed to join after deadline")
				return regexpCanceledStream{err: ctx.Err()}
			}
		}
	}
	for {
		var active bool
		err := fixture.Admin.QueryRowContext(ctx, `SELECT COALESCE((SELECT state='active' AND query=$2 FROM pg_stat_activity WHERE pid=$1),FALSE)`, pid, q).Scan(&active)
		if err != nil {
			cancel()
			out := join()
			t.Fatalf("selected SRF admission: %v; statement: %v", err, out.err)
		}
		if active {
			break
		}
		select {
		case out := <-result:
			joined = true
			t.Fatalf("selected SRF completed before observed admission: %v", out.err)
		default:
		}
	}
	var signaled bool
	if err := fixture.Admin.QueryRowContext(ctx, `SELECT pg_cancel_backend($1)`, pid).Scan(&signaled); err != nil || !signaled {
		cancel()
		out := join()
		t.Fatalf("cancel selected SRF: signaled=%t error=%v; statement: %v", signaled, err, out.err)
	}
	out := join()
	if out.validation != nil {
		t.Fatal(out.validation)
	}
	if out.err == nil {
		t.Fatal("admitted selected SRF completed without cancellation")
	}
	want := fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "57014", Message: "canceling statement due to user request"}
	fixture.AssertDiagnostic(t, out.err, &want)
	if out.count < 0 || out.count > maxRows {
		t.Fatalf("canceled prefix count %d exceeds full invocation %d", out.count, maxRows)
	}
	if out.cursor {
		if !reflect.DeepEqual(out.columns, []string{name}) || len(out.types) != 1 || out.types[0].DatabaseTypeName() != typ {
			t.Fatal("canceled cursor schema", out.columns, out.types)
		}
		c := out.types[0]
		scan := reflect.TypeFor[string]()
		length, known := int64(math.MaxInt64), true
		if typ == "_TEXT" {
			scan = reflect.TypeFor[any]()
			length, known = 0, false
		}
		if c.ScanType() != scan {
			t.Fatalf("canceled cursor ScanType %v want %v", c.ScanType(), scan)
		}
		if nullable, ok := c.Nullable(); nullable || ok {
			t.Fatal("canceled computed nullability", nullable, ok)
		}
		if l, ok := c.Length(); l != length || ok != known {
			t.Fatal("canceled Length", l, ok)
		}
		if precision, scale, ok := c.DecimalSize(); precision != 0 || scale != 0 || ok {
			t.Fatal("canceled DecimalSize", precision, scale, ok)
		}
	}
	b.Recorder.Mu.Lock()
	if len(b.Recorder.Operations) != before+1 {
		b.Recorder.Mu.Unlock()
		t.Fatal("missing canceled query middleware operation")
	}
	op := b.Recorder.Operations[before]
	b.Recorder.Mu.Unlock()
	if op.SQL != q || len(op.Rows) != out.count {
		t.Fatal("canceled middleware stream mismatch", op.SQL, len(op.Rows), out.count)
	}
	if op.Err == nil {
		t.Fatal("middleware lost cancellation")
	}
	fixture.AssertDiagnostic(t, op.Err, &want)
	if out.cursor {
		if !op.Closed || !reflect.DeepEqual(op.Columns, out.columns) {
			t.Fatal("canceled cursor not recorded/closed")
		}
	} else if len(op.Rows) != 0 || len(op.Columns) != 0 {
		t.Fatal("failed query recorded partial output")
	}
	for _, r := range op.Rows {
		if len(r) != 1 {
			t.Fatal("recorded canceled width", r)
		}
		fixture.AssertRawValue(t, true, typ, r[0])
		if fixture.Normalize(r[0]) != value {
			t.Fatal("recorded canceled value", r[0])
		}
	}
}
