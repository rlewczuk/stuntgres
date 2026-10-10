package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/hex"
	"reflect"
	"strings"
	"testing"
	"time"
)

// Independent RFC 9562 decoder: no engine helpers or UUID extractor functions.
func uuidGenerationDecode(t *testing.T, text string, version byte) ([16]byte, int64) {
	t.Helper()
	var out [16]byte
	if len(text) != 36 || text[8] != '-' || text[13] != '-' || text[18] != '-' || text[23] != '-' || strings.ToLower(text) != text {
		t.Fatalf("noncanonical UUID %q", text)
	}
	raw, err := hex.DecodeString(strings.ReplaceAll(text, "-", ""))
	if err != nil || len(raw) != 16 {
		t.Fatalf("UUID %q: %v", text, err)
	}
	copy(out[:], raw)
	if out[6]>>4 != version || out[8]>>6 != 2 {
		t.Fatalf("UUID version/variant %q", text)
	}
	var ms int64
	for _, b := range out[:6] {
		ms = ms<<8 | int64(b)
	}
	return out, ms
}

func uuidGenerationScenario(t *testing.T, body func(*testing.T, *referencePair, referenceBackend, *sql.Conn)) {
	t.Helper()
	p := newReferenceScenario(t)
	for _, b := range p.backends() {
		t.Run(b.name, func(t *testing.T) {
			c, err := b.db.Conn(p.ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			body(t, p, b, c)
		})
	}
}

func uuidGenerationColumns(t *testing.T, cols []*sql.ColumnType, pg bool, names []string, declared bool, nullable *bool) {
	t.Helper()
	if len(cols) != len(names) {
		t.Fatalf("UUID metadata count %d want %d", len(cols), len(names))
	}
	for i, col := range cols {
		scan := reflect.TypeFor[string]()
		if pg {
			scan = reflect.TypeFor[any]()
		}
		if col.Name() != names[i] || col.DatabaseTypeName() != "UUID" || col.ScanType() != scan {
			t.Fatalf("UUID column %d got %s/%s/%v want %s/UUID/%v", i, col.Name(), col.DatabaseTypeName(), col.ScanType(), names[i], scan)
		}
		if _, ok := col.Length(); ok {
			t.Fatal("UUID length known")
		}
		if _, _, ok := col.DecimalSize(); ok {
			t.Fatal("UUID precision known")
		}
		n, known := col.Nullable()
		if pg {
			if n || known {
				t.Fatal("pq UUID nullability known")
			}
		} else if declared && (!known || !n) {
			t.Fatal("native declared nullable UUID column lacks its nullability proof")
		} else if known && nullable != nil && n != *nullable {
			t.Fatalf("UUID expression nullability got %v want %v", n, *nullable)
		}
	}
}

func uuidGenerationColumnName(query string) string {
	name := strings.TrimPrefix(query, "SELECT ")
	if i := strings.IndexAny(name, "( "); i >= 0 {
		name = name[:i]
	}
	return strings.TrimPrefix(name, "pg_catalog.")
}

func uuidGenerationRead(t *testing.T, p *referencePair, b referenceBackend, q referenceExecutor, query string, args ...any) *string {
	return uuidGenerationReadWithDeclaration(t, p, b, q, query, false, args...)
}

func uuidGenerationReadWithDeclaration(t *testing.T, p *referencePair, b referenceBackend, q referenceExecutor, query string, declared bool, args ...any) *string {
	t.Helper()
	rows, err := q.QueryContext(context.WithValue(p.ctx, referenceRecorderKey{}, b.recorder), query, args...)
	if err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	defer rows.Close()
	cols, err := rows.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	var nullable *bool
	if name := uuidGenerationColumnName(query); name == "uuidv7" || name == "uuidv4" || name == "gen_random_uuid" {
		allowNull := strings.Contains(query, "NULL") || strings.Contains(query, "$")
		nullable = &allowNull
	}
	uuidGenerationColumns(t, cols, b.pg, []string{uuidGenerationColumnName(query)}, declared, nullable)
	if !rows.Next() {
		t.Fatalf("missing UUID row: %v", rows.Err())
	}
	var raw any
	if err := rows.Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var text *string
	if raw != nil {
		var s string
		if b.pg {
			v, ok := raw.([]byte)
			if !ok {
				t.Fatalf("pq UUID %T", raw)
			}
			s = string(v)
		} else {
			v, ok := raw.(string)
			if !ok {
				t.Fatalf("native UUID %T", raw)
			}
			s = v
		}
		text = &s
	}
	if rows.Next() || rows.Err() != nil {
		t.Fatalf("UUID cursor tail %v", rows.Err())
	}
	return text
}

func uuidGenerationWindow(t *testing.T, ms int64, before, after time.Time) {
	t.Helper()
	if ms < before.UnixMilli()-2000 || ms > after.UnixMilli()+2000 {
		t.Fatalf("UUID ms %d outside independently bracketed [%d,%d]", ms, before.UnixMilli()-2000, after.UnixMilli()+2000)
	}
}

func uuidGenerationBasic(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
	seen := map[string]bool{}
	for _, name := range []string{"gen_random_uuid()", "uuidv4()", "uuidv7()", "uuidv7(interval '0')"} {
		query := "SELECT pg_catalog." + name
		stmt, err := c.PrepareContext(p.ctx, query)
		if err != nil {
			t.Fatal(err)
		}
		held := referenceHeldStatement{referenceExecutor: c, statement: stmt, query: query}
		for i := range 8 {
			var q referenceExecutor = c
			if i%2 == 1 {
				q = held
			}
			before := time.Now()
			v := uuidGenerationRead(t, p, b, q, query)
			after := time.Now()
			if v == nil || seen[*v] {
				t.Fatalf("NULL/repeated generated UUID %v", v)
			}
			seen[*v] = true
			version := byte(4)
			if strings.HasPrefix(name, "uuidv7") {
				version = 7
			}
			_, ms := uuidGenerationDecode(t, *v, version)
			if version == 7 {
				uuidGenerationWindow(t, ms, before, after)
			}
		}
		if err := stmt.Close(); err != nil {
			t.Fatal(err)
		}
	}
	var previous [16]byte
	stmt, err := c.PrepareContext(p.ctx, "SELECT uuidv7($1)")
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	held := referenceHeldStatement{referenceExecutor: c, statement: stmt, query: "SELECT uuidv7($1)"}
	for i := range 64 {
		var v *string
		if i%2 == 0 {
			v = uuidGenerationRead(t, p, b, c, "SELECT uuidv7()")
		} else {
			v = uuidGenerationRead(t, p, b, held, "SELECT uuidv7($1)", "0")
		}
		raw, _ := uuidGenerationDecode(t, *v, 7)
		if i > 0 && bytes.Compare(previous[:], raw[:]) >= 0 {
			t.Fatalf("nonascending v7 at %d: %x >= %x", i, previous, raw)
		}
		previous = raw
	}
	if uuidGenerationRead(t, p, b, held, "SELECT uuidv7($1)", nil) != nil {
		t.Fatal("strict NULL generated a UUID")
	}
	for _, query := range []string{"SELECT uuidv7(NULL)", "SELECT uuidv7(NULL) WHERE false"} {
		rows, err := c.QueryContext(p.ctx, query)
		if err != nil {
			t.Fatal(err)
		}
		cols, err := rows.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		nullable := true
		uuidGenerationColumns(t, cols, b.pg, []string{"uuidv7"}, false, &nullable)
		rows.Close()
	}
	tx, err := c.BeginTx(p.ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	v := uuidGenerationRead(t, p, b, tx, "SELECT uuidv7()")
	raw, _ := uuidGenerationDecode(t, *v, 7)
	if bytes.Compare(previous[:], raw[:]) >= 0 {
		t.Fatal("transaction lost v7 order")
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	v = uuidGenerationRead(t, p, b, c, "SELECT uuidv7()")
	next, _ := uuidGenerationDecode(t, *v, 7)
	if bytes.Compare(raw[:], next[:]) >= 0 {
		t.Fatal("rollback reset v7 clock")
	}
}

func TestReferenceUUIDGenerationBitsMetadataAndOrder(t *testing.T) {
	uuidGenerationScenario(t, uuidGenerationBasic)
}

func uuidGenerationDiagnostic(code, message, detail string) referenceDiagnostic {
	return referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: code, Message: message, Detail: detail}
}
func uuidGenerationError(t *testing.T, err error, want referenceDiagnostic) {
	t.Helper()
	if err == nil {
		t.Fatal("expected SQL error")
	}
	got := referenceExtractDiagnostic(t, err)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("diagnostic got %#v want %#v", got, want)
	}
}
