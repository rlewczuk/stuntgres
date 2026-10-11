package fixture

import (
	"database/sql"
	"reflect"
	"testing"
)

func StringEncodingScenario(t *testing.T, body func(*testing.T, *Pair, Backend, *sql.Conn)) {
	t.Helper()
	p := NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			body(t, p, b, c)
		})
	}
}

func StringEncodingBoth(t *testing.T, p *Pair, b Backend, c *sql.Conn, s Step) {
	t.Helper()
	RunStep(t, p.Ctx, c, b.Recorder, s)
	RunStep(t, p.Ctx, c, b.Recorder, Prepared(s))
}

func StringEncodingValue(t *testing.T, p *Pair, b Backend, c *sql.Conn, expr, typ string, want any) {
	t.Helper()
	s := Select("SELECT "+expr+" AS v", []string{"v"}, []string{typ}, [][]any{{want}})
	s.CheckMetadata = StringEncodingMetadata(b.PG, s.Columns, s.Types)
	StringEncodingBoth(t, p, b, c, s)
}

func StringEncodingMetadata(pg bool, names, types []string) func(*testing.T, *sql.Rows) {
	return func(t *testing.T, rows *sql.Rows) {
		t.Helper()
		ct, err := rows.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		StringEncodingColumns(t, ct, pg, names, types)
	}
}

func StringEncodingViewMetadata(pg bool, names, types []string) func(*testing.T, *sql.Rows) {
	return func(t *testing.T, rows *sql.Rows) {
		t.Helper()
		ct, err := rows.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		StringEncodingColumnContract(t, ct, pg, names, types, true)
	}
}

func StringEncodingColumns(t *testing.T, ct []*sql.ColumnType, pg bool, names, types []string) {
	StringEncodingColumnContract(t, ct, pg, names, types, false)
}

func StringEncodingColumnContract(t *testing.T, ct []*sql.ColumnType, pg bool, names, types []string, view bool) {
	t.Helper()
	if len(ct) != len(types) {
		t.Fatalf("metadata width: %#v", ct)
	}
	for i, typ := range types {
		scan := reflect.TypeFor[string]()
		if typ == "INT4" {
			scan = reflect.TypeFor[int64]()
			if pg {
				scan = reflect.TypeFor[int32]()
			}
		}
		if typ == "BOOL" {
			scan = reflect.TypeFor[bool]()
		}
		length, lk := ct[i].Length()
		nullable, nk := ct[i].Nullable()
		precision, scale, dk := ct[i].DecimalSize()
		wantLength, wantLK := int64(0), false
		if typ == "TEXT" {
			wantLength, wantLK = 9223372036854775807, true
		}
		wantNullable := view && !pg
		wantNullableKnown := view && !pg
		if ct[i].Name() != names[i] || ct[i].DatabaseTypeName() != typ || ct[i].ScanType() != scan || length != wantLength || lk != wantLK || nullable != wantNullable || nk != wantNullableKnown || dk {
			t.Fatalf("column %d: %s %s scan=%v length=%d/%v nullable=%v/%v decimal=%d,%d/%v", i, ct[i].Name(), ct[i].DatabaseTypeName(), ct[i].ScanType(), length, lk, nullable, nk, precision, scale, dk)
		}
	}
}

func StringEncodingFailure(t *testing.T, p *Pair, b Backend, c *sql.Conn, q string, d Diagnostic, analysis bool, args ...any) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		s := ErrorDiagnostic(q, d, args...)
		s.SelectRows, s.ErrorStage = true, "execute"
		if prepared {
			s = Prepared(s)
			if analysis {
				s.ErrorStage = "prepare"
			}
		}
		RunStep(t, p.Ctx, c, b.Recorder, s)
	}
}

func StringEncodingHeld(t *testing.T, p *Pair, b Backend, c *sql.Conn, q string, frames []Step) {
	t.Helper()
	stmt, err := c.PrepareContext(p.Ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	held := HeldStatement{Executor: c, Statement: stmt, SQL: q}
	for _, s := range frames {
		if s.State == "" && s.CheckMetadata == nil {
			s.CheckMetadata = StringEncodingMetadata(b.PG, s.Columns, s.Types)
		}
		RunStep(t, p.Ctx, held, b.Recorder, s)
	}
}
