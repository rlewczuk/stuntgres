package reference_test

import (
	"database/sql"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

// XPath XML[] has an array codec, never the scalar XML or generic results
// metadata profile. Profiles describe expressions versus nullable stored columns,
// not observed rows: empty sets, SQL NULL and {NULL} retain the same descriptors.
func xpathArrayMetadata(pg, stored bool) func(*testing.T, *sql.Rows) {
	return func(t *testing.T, r *sql.Rows) {
		t.Helper()
		names, err := r.Columns()
		if err != nil || !reflect.DeepEqual(names, []string{"v"}) {
			t.Fatal("XML[] columns", names, err)
		}
		cols, err := r.ColumnTypes()
		if err != nil || len(cols) != 1 {
			t.Fatal(cols, err)
		}
		c := cols[0]
		scan := reflect.TypeFor[string]()
		if pg {
			scan = reflect.TypeFor[any]()
		}
		length, lk := c.Length()
		precision, scale, dk := c.DecimalSize()
		nullable, nk := c.Nullable()
		wantNullable := stored && !pg
		if c.Name() != "v" || c.DatabaseTypeName() != "_XML" || c.ScanType() != scan || length != 0 || lk || precision != 0 || scale != 0 || dk || nullable != wantNullable || nk != wantNullable {
			t.Fatalf("XML[] descriptor name=%q type=%q scan=%v length=%d/%t decimal=%d,%d/%t nullable=%t/%t; want v/_XML scan=%v unknown length/decimal nullable=%t/%t", c.Name(), c.DatabaseTypeName(), c.ScanType(), length, lk, precision, scale, dk, nullable, nk, scan, wantNullable, wantNullable)
		}
	}
}

func xpathStep(b fixture.Backend, q, typ string, values ...any) fixture.Step {
	rows := make([][]any, len(values))
	for i, v := range values {
		rows[i] = []any{v}
	}
	s := fixture.Select(q, []string{"v"}, []string{typ}, rows)
	if typ == "_XML" {
		s.CheckMetadata = xpathArrayMetadata(b.PG, false)
		s.RawTypes = []string{"_XML"}
	}
	return s
}

func xpathStoredArrayStep(q string, pg bool, values ...any) fixture.Step {
	s := xpathArrayStep(q, pg, values...)
	s.CheckMetadata = xpathArrayMetadata(pg, true)
	return s
}

func xpathError(code, message, detail string, position int) fixture.Diagnostic {
	d := fixture.ControlsReferenceDiagnostic(code, message)
	d.Detail = detail
	d.Position = fixture.Position(position)
	return d
}

const xpathBadA = "line 1: Premature end of data in tag a line 1\n<a>\n   ^"

// Keep direct Query's no-cursor and independent PREPARE/execute boundaries in
// the existing result diagnostic runner, including every logical error field.
func xpathFailure(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, q string, d fixture.Diagnostic, analysis bool, args ...any) {
	t.Helper()
	stage := "execute"
	if analysis {
		stage = "prepare"
	}
	t.Run(q, func(t *testing.T) {
		resultsError(t, p, c, q, true, stage, d, args...)
	})
}
