package reference_test

import (
	"database/sql"
	"reflect"
	"testing"
)

func binaryConversionScenario(t *testing.T, body func(*testing.T, *referencePair, referenceBackend, *sql.Conn)) {
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
func binaryConversionMetadata(pg bool, names, types []string) func(*testing.T, *sql.Rows) {
	return func(t *testing.T, r *sql.Rows) {
		t.Helper()
		ct, err := r.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		binaryConversionColumns(t, ct, pg, names, types)
	}
}
func binaryConversionViewMetadata(pg bool, names, types []string) func(*testing.T, *sql.Rows) {
	return func(t *testing.T, r *sql.Rows) {
		t.Helper()
		ct, err := r.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		binaryConversionColumnContract(t, ct, pg, names, types, true)
	}
}
func binaryConversionColumns(t *testing.T, ct []*sql.ColumnType, pg bool, names, types []string) {
	binaryConversionColumnContract(t, ct, pg, names, types, false)
}
func binaryConversionColumnContract(t *testing.T, ct []*sql.ColumnType, pg bool, names, types []string, view bool) {
	t.Helper()
	if len(ct) != len(types) {
		t.Fatal(ct)
	}
	for i, c := range ct {
		scan := reflect.TypeFor[string]()
		if types[i] == "BYTEA" {
			scan = reflect.TypeFor[[]byte]()
		}
		n, nk := c.Nullable()
		l, lk := c.Length()
		_, _, dk := c.DecimalSize()
		wantLK := types[i] == "TEXT" || pg
		wantNullable := view && !pg
		if c.Name() != names[i] || c.DatabaseTypeName() != types[i] || c.ScanType() != scan || n != wantNullable || nk != wantNullable || dk || lk != wantLK || (lk && l != 9223372036854775807) {
			t.Fatalf("column %d: %s %s %v nullable=%v/%v length=%d/%v decimal=%v", i, c.Name(), c.DatabaseTypeName(), c.ScanType(), n, nk, l, lk, dk)
		}
	}
}
func binaryConversionBoth(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn, s referenceStep) {
	t.Helper()
	if s.state == "" && s.selectRows && s.checkMetadata == nil {
		s.checkMetadata = binaryConversionMetadata(b.pg, s.columns, s.types)
	}
	referenceRunStep(t, p.ctx, c, b.recorder, s)
	referenceRunStep(t, p.ctx, c, b.recorder, referencePrepared(s))
}
func binaryConversionValue(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn, expr, typ string, want any) {
	t.Helper()
	binaryConversionBoth(t, p, b, c, referenceSelect("SELECT "+expr+" AS v", []string{"v"}, []string{typ}, [][]any{{want}}))
}
func binaryConversionHeld(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn, q string, frames []referenceStep) {
	t.Helper()
	stmt, err := c.PrepareContext(p.ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	held := referenceHeldStatement{referenceExecutor: c, statement: stmt, query: q}
	for _, s := range frames {
		if s.state == "" {
			s.checkMetadata = binaryConversionMetadata(b.pg, s.columns, s.types)
		}
		referenceRunStep(t, p.ctx, held, b.recorder, s)
	}
}
