package reference_test

import (
	"database/sql"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

// Preserve the actual oid[] and internal "char"[] descriptors, including NULL
// values. Text-cast witnesses alone cannot detect a mistyped NULL fallback.
func resultsFormalRaw(t *testing.T, p *fixture.Pair, b fixture.Backend, c fixture.Executor, id int, all, modes, kind string) {
	t.Helper()
	var a, m any
	if all != "" {
		a = all
		m = modes
	}
	types := []string{"_OID", "_CHAR", "CHAR"}
	s := fixture.Select("SELECT proallargtypes,proargmodes,prokind FROM pg_proc WHERE oid=$1::oid", []string{"proallargtypes", "proargmodes", "prokind"}, types, [][]any{{a, m, kind}}, id)
	s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
		cols, err := rows.ColumnTypes()
		if err != nil || len(cols) != 3 {
			t.Fatal(cols, err)
		}
		for i, col := range cols {
			scan := reflect.TypeFor[string]()
			if b.PG {
				scan = reflect.TypeFor[any]()
			}
			length, lk := col.Length()
			precision, scale, dk := col.DecimalSize()
			nullable, nk := col.Nullable()
			wantNullable, wantKnown := true, true
			if i == 2 {
				wantNullable = false
			}
			if b.PG {
				wantNullable = false
				wantKnown = false
			}
			if col.DatabaseTypeName() != types[i] || col.ScanType() != scan || length != 0 || lk || precision != 0 || scale != 0 || dk || nullable != wantNullable || nk != wantKnown {
				t.Errorf("raw formal column %d: type=%s scan=%v length=%d/%t decimal=%d,%d/%t nullable=%t/%t", i, col.DatabaseTypeName(), col.ScanType(), length, lk, precision, scale, dk, nullable, nk)
			}
		}
	}
	fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
}
