package reference_test

import (
	"bytes"
	"database/sql"
	"reflect"
	"testing"
)

func binaryAccessColumns(t *testing.T, columns []*sql.ColumnType, pg, stored bool) {
	t.Helper()
	if len(columns) != 6 {
		t.Fatal(columns)
	}
	for i, c := range columns {
		scan := reflect.TypeFor[[]byte]()
		if i == 0 {
			scan = reflect.TypeFor[int64]()
		}
		if i == 1 || i == 2 {
			scan = reflect.TypeFor[int64]()
			if pg {
				scan = reflect.TypeFor[int32]()
			}
		}
		if c.Name() != binaryAccessNames[i] || c.DatabaseTypeName() != binaryAccessTypes[i] || c.ScanType() != scan {
			t.Errorf("column %d: %s %s %v", i, c.Name(), c.DatabaseTypeName(), c.ScanType())
		}
		n, nok := c.Nullable()
		a, z, dok := c.DecimalSize()
		l, lok := c.Length()
		// Native stored view columns retain their nullable declaration; pq
		// cannot expose it. Fresh scalar projections have unknown nullability.
		wantNullable := stored && !pg
		if n != wantNullable || nok != wantNullable || dok || a != 0 || z != 0 {
			t.Errorf("metadata %d: nullable %v/%v want %v/%v decimal %d,%d/%v", i, n, nok, wantNullable, wantNullable, a, z, dok)
		}
		if pg && i >= 3 {
			if !lok || l != 9223372036854775807 {
				t.Error("bytea length", l, lok)
			}
		} else if lok || l != 0 {
			t.Error("unknown length", l, lok)
		}
	}
}

func TestReferenceBinaryAccessMetadata(t *testing.T) {
	binaryAccessScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		referenceRunStep(t, p.ctx, c, b.recorder, referenceExec("CREATE TABLE access_meta(id integer,v bytea)", 0))
		referenceRunStep(t, p.ctx, c, b.recorder, referenceExec(`INSERT INTO access_meta VALUES(1,'\x0180'),(2,NULL)`, 2))
		q := "SELECT " + binaryAccessProjection + " FROM access_meta WHERE id=$1"
		stmt, err := c.PrepareContext(p.ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		h := referenceHeldStatement{referenceExecutor: c, statement: stmt, query: q}
		var retained []*sql.ColumnType
		for _, id := range []int{99, 2, 1} {
			want := [][]any{}
			if id == 2 {
				want = [][]any{{nil, nil, nil, nil, nil, nil}}
			}
			if id == 1 {
				want = [][]any{{int64(2), int64(1), int64(1), "\x01\x00", "\x00\x80", "\x80\x01"}}
			}
			s := referenceSelect(q, binaryAccessNames, binaryAccessTypes, want, id)
			s.checkMetadata = func(t *testing.T, r *sql.Rows) {
				cols, err := r.ColumnTypes()
				if err != nil {
					t.Fatal(err)
				}
				binaryAccessColumns(t, cols, b.pg, false)
				retained = cols
			}
			referenceRunStep(t, p.ctx, h, b.recorder, s)
			binaryAccessColumns(t, retained, b.pg, false)
		}
		if err := stmt.Close(); err != nil {
			t.Fatal(err)
		}
		binaryAccessColumns(t, retained, b.pg, false)
		for _, q := range []string{`SELECT bit_count('\x'::bytea) AS count,get_byte(NULL::bytea,0) AS byte`, `SELECT bit_count('\x'::bytea) AS count,get_byte(NULL::bytea,0) AS byte WHERE FALSE`} {
			want := [][]any{{int64(0), nil}}
			if q[len(q)-5:] == "FALSE" {
				want = [][]any{}
			}
			binaryAccessBoth(t, p, b, c, referenceSelect(q, []string{"count", "byte"}, []string{"INT8", "INT4"}, want))
		}
	})
}

func TestReferenceBinaryAccessOwnership(t *testing.T) {
	binaryAccessScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		referenceRunStep(t, p.ctx, c, b.recorder, referenceExec("CREATE TABLE access_owned(id integer,v bytea)", 0))
		input := []byte{1, 128}
		referenceRunStep(t, p.ctx, c, b.recorder, referenceExec("INSERT INTO access_owned VALUES(1,$1)", 1, input))
		input[0] = 255
		q := "SELECT v,set_byte(v,0,256),set_bit(v,15::bigint,0),v FROM access_owned"
		r, err := c.QueryContext(p.ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer r.Close()
		if !r.Next() {
			t.Fatal(r.Err())
		}
		var original, changed, bit, other []byte
		if err := r.Scan(&original, &changed, &bit, &other); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(original, []byte{1, 128}) || !bytes.Equal(changed, []byte{0, 128}) || !bytes.Equal(bit, []byte{1, 0}) || !bytes.Equal(other, []byte{1, 128}) {
			t.Fatal(original, changed, bit, other)
		}
		original[0] = 99
		changed[1] = 99
		bit[0] = 99
		if !bytes.Equal(other, []byte{1, 128}) {
			t.Fatal("exported result aliases another projection")
		}
		r.Close()
		binaryAccessBoth(t, p, b, c, referenceSelect("SELECT v FROM access_owned", []string{"v"}, []string{"BYTEA"}, [][]any{{"\x01\x80"}}))
		q = "SELECT bit_count($1::bytea),get_bit($1::bytea,15::bigint),get_byte($1::bytea,0),set_bit($1::bytea,15::bigint,0),set_byte($1::bytea,0,$2::integer),reverse($1::bytea)"
		stmt, err := c.PrepareContext(p.ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		h := referenceHeldStatement{referenceExecutor: c, statement: stmt, query: q}
		for _, tc := range []struct {
			input any
			n     int
			want  []any
		}{
			{[]byte{1, 128}, 256, []any{int64(2), int64(1), int64(1), "\x01\x00", "\x00\x80", "\x80\x01"}},
			{[]byte{0, 255}, -1, []any{int64(8), int64(1), int64(0), "\x00\x7f", "\xff\xff", "\xff\x00"}},
			{nil, 2, []any{nil, nil, nil, nil, nil, nil}},
			{[]byte(nil), 2, []any{nil, nil, nil, nil, nil, nil}},
		} {
			referenceRunStep(t, p.ctx, h, b.recorder, referenceSelect(q, binaryAccessNames, binaryAccessTypes, [][]any{tc.want}, tc.input, tc.n))
		}
		old, err := stmt.QueryContext(p.ctx, []byte{1, 128}, 256)
		if err != nil {
			t.Fatal(err)
		}
		defer old.Close()
		meta, err := old.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		binaryAccessColumns(t, meta, b.pg, false)
		// PostgreSQL has one portal per borrowed session; another connection proves
		// that retained output survives a later frame without replacing its portal.
		referenceRunStep(t, p.ctx, b.db, b.recorder, referenceSelect(q, binaryAccessNames, binaryAccessTypes, [][]any{{int64(8), int64(1), int64(0), "\x00\x7f", "\xff\xff", "\xff\x00"}}, []byte{0, 255}, -1))
		stringCoreRetainedRows(t, old, nil, binaryAccessNames, binaryAccessTypes, [][]any{{int64(2), int64(1), int64(1), "\x01\x00", "\x00\x80", "\x80\x01"}}, false)
		binaryAccessColumns(t, meta, b.pg, false)
	})
}
