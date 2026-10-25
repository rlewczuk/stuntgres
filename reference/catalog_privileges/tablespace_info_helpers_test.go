package reference_test

import (
	"database/sql"
	"fmt"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

func tablespaceInfoBackends(t *testing.T, p *fixture.Pair, body func(*testing.T, fixture.Backend)) {
	t.Helper()
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) { body(t, b) })
	}
}

func tablespaceInfoOID(b fixture.Backend, oid int64) any {
	if b.PG {
		return fmt.Sprint(oid)
	}
	return oid
}

func tablespaceInfoCurrent(t *testing.T, p *fixture.Pair, x fixture.Executor) int64 {
	t.Helper()
	rows, err := x.QueryContext(p.Ctx, "SELECT oid FROM pg_database WHERE datname=current_database()")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var oid int64
	if !rows.Next() {
		t.Fatal("missing current database", rows.Err())
	}
	if err := rows.Scan(&oid); err != nil {
		t.Fatal(err)
	}
	if oid <= 0 || rows.Next() || rows.Err() != nil {
		t.Fatal("invalid database identity", oid, rows.Err())
	}
	return oid
}

func tablespaceInfoSelect(q string, typ string, rows [][]any, args ...any) fixture.Step {
	return fixture.Select(q, []string{"v"}, []string{typ}, rows, args...)
}

func tablespaceInfoMetadata(pg bool) func(*testing.T, *sql.Rows) {
	return func(t *testing.T, rows *sql.Rows) {
		t.Helper()
		columns, err := rows.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		for _, c := range columns {
			scan := reflect.TypeFor[int64]()
			switch c.DatabaseTypeName() {
			case "TEXT":
				scan = reflect.TypeFor[string]()
			case "OID":
				if pg {
					scan = reflect.TypeFor[any]()
				}
			case "INT8":
			default:
				t.Fatal("unexpected inquiry descriptor", c.DatabaseTypeName())
			}
			n, nk := c.Nullable()
			l, lk := c.Length()
			p, s, dk := c.DecimalSize()
			wantLength, wantLengthKnown := int64(0), false
			// Existing unbounded TEXT codec contract, also asserted by comment inquiries.
			if c.DatabaseTypeName() == "TEXT" {
				wantLength, wantLengthKnown = 1<<63-1, true
			}
			if c.ScanType() != scan || n || nk || l != wantLength || lk != wantLengthKnown || p != 0 || s != 0 || dk {
				t.Fatalf("metadata %s: scan=%v want=%v nullable=%t/%t length=%d/%t decimal=%d,%d/%t", c.Name(), c.ScanType(), scan, n, nk, l, lk, p, s, dk)
			}
		}
	}
}

func tablespaceInfoLocationError(pg bool) fixture.Diagnostic {
	if pg {
		return tablespaceDiagnostic("58P01", `could not stat file "pg_tblspc/4294967295": No such file or directory`)
	}
	return tablespaceDiagnostic("0A000", "pg_tablespace_location physical directory inspection is unsupported in embedded mode")
}

func tablespaceInfoNotices(t *testing.T, got []fixture.Diagnostic, messages ...string) {
	t.Helper()
	want := make([]fixture.Diagnostic, len(messages))
	for i, m := range messages {
		want[i] = fixture.Diagnostic{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "01000", Message: m}
	}
	if len(got) != len(want) {
		t.Fatalf("warnings got=%+v want=%+v", got, want)
	}
	for i := range want {
		if !reflect.DeepEqual(got[i], want[i]) {
			t.Fatalf("warning %d got=%+v want=%+v", i, got[i], want[i])
		}
	}
}
