package reference_test

import (
	"database/sql"
	"reflect"
	"stuntgres/reference/fixture"
	"testing"
)

func binaryConversionScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)) {
	t.Helper()
	p := fixture.NewScenario(t)
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
func binaryConversionBoth(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, s fixture.Step) {
	t.Helper()
	if s.State == "" && s.SelectRows && s.CheckMetadata == nil {
		s.CheckMetadata = binaryConversionMetadata(b.PG, s.Columns, s.Types)
	}
	fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
}
func binaryConversionValue(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, expr, typ string, want any) {
	t.Helper()
	binaryConversionBoth(t, p, b, c, fixture.Select("SELECT "+expr+" AS v", []string{"v"}, []string{typ}, [][]any{{want}}))
}
func binaryConversionHeld(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, q string, frames []fixture.Step) {
	t.Helper()
	stmt, err := c.PrepareContext(p.Ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
	for _, s := range frames {
		if s.State == "" {
			s.CheckMetadata = binaryConversionMetadata(b.PG, s.Columns, s.Types)
		}
		fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
	}
}
