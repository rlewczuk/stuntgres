package reference_test

import (
	"database/sql"
	"reflect"
	"testing"
)

func stringEncodingScenario(t *testing.T, body func(*testing.T, *referencePair, referenceBackend, *sql.Conn)) {
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

func stringEncodingBoth(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn, s referenceStep) {
	t.Helper()
	referenceRunStep(t, p.ctx, c, b.recorder, s)
	referenceRunStep(t, p.ctx, c, b.recorder, referencePrepared(s))
}

func stringEncodingValue(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn, expr, typ string, want any) {
	t.Helper()
	s := referenceSelect("SELECT "+expr+" AS v", []string{"v"}, []string{typ}, [][]any{{want}})
	s.checkMetadata = stringEncodingMetadata(b.pg, s.columns, s.types)
	stringEncodingBoth(t, p, b, c, s)
}

func stringEncodingMetadata(pg bool, names, types []string) func(*testing.T, *sql.Rows) {
	return func(t *testing.T, rows *sql.Rows) {
		t.Helper()
		ct, err := rows.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		stringEncodingColumns(t, ct, pg, names, types)
	}
}

func stringEncodingViewMetadata(pg bool, names, types []string) func(*testing.T, *sql.Rows) {
	return func(t *testing.T, rows *sql.Rows) {
		t.Helper()
		ct, err := rows.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		stringEncodingColumnContract(t, ct, pg, names, types, true)
	}
}

func stringEncodingColumns(t *testing.T, ct []*sql.ColumnType, pg bool, names, types []string) {
	stringEncodingColumnContract(t, ct, pg, names, types, false)
}

func stringEncodingColumnContract(t *testing.T, ct []*sql.ColumnType, pg bool, names, types []string, view bool) {
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

func stringEncodingFailure(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn, q string, d referenceDiagnostic, analysis bool, args ...any) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		s := referenceErrorDiagnostic(q, d, args...)
		s.selectRows, s.errorStage = true, "execute"
		if prepared {
			s = referencePrepared(s)
			if analysis {
				s.errorStage = "prepare"
			}
		}
		referenceRunStep(t, p.ctx, c, b.recorder, s)
	}
}

func stringEncodingHeld(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn, q string, frames []referenceStep) {
	t.Helper()
	stmt, err := c.PrepareContext(p.ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	held := referenceHeldStatement{referenceExecutor: c, statement: stmt, query: q}
	for _, s := range frames {
		if s.state == "" && s.checkMetadata == nil {
			s.checkMetadata = stringEncodingMetadata(b.pg, s.columns, s.types)
		}
		referenceRunStep(t, p.ctx, held, b.recorder, s)
	}
}
