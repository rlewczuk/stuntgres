package reference_test

import (
	"bytes"
	"database/sql"
	"reflect"
	"testing"
)

func stringPadColumns(t *testing.T, ct []*sql.ColumnType, names, types []string, pg bool) {
	t.Helper()
	if len(ct) != len(types) {
		t.Fatal(ct)
	}
	for i, typ := range types {
		scan := reflect.TypeFor[string]()
		if typ == "BYTEA" {
			scan = reflect.TypeFor[[]byte]()
		}
		l, lk := ct[i].Length()
		n, nk := ct[i].Nullable()
		a, z, dk := ct[i].DecimalSize()
		wantLength := int64(0)
		wantKnown := typ == "TEXT" || pg
		if wantKnown {
			wantLength = 9223372036854775807
		}
		if ct[i].Name() != names[i] || ct[i].DatabaseTypeName() != typ || ct[i].ScanType() != scan || l != wantLength || lk != wantKnown || n || nk || dk {
			t.Fatalf("column %d name=%s type=%s scan=%v length=%d/%v nullable=%v/%v decimal=%d,%d/%v", i, ct[i].Name(), ct[i].DatabaseTypeName(), ct[i].ScanType(), l, lk, n, nk, a, z, dk)
		}
	}
}

func TestReferenceStringPadMetadata(t *testing.T) {
	stringPadScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		q := "SELECT lpad($1,$2,$3),rpad($1,$2),repeat($1,$2),reverse($1),reverse($4::bytea) AS binary"
		names := []string{"lpad", "rpad", "repeat", "reverse", "binary"}
		types := []string{"TEXT", "TEXT", "TEXT", "TEXT", "BYTEA"}
		stmt, err := c.PrepareContext(p.ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := referenceHeldStatement{referenceExecutor: c, statement: stmt, query: q}
		for _, tc := range []struct {
			args []any
			row  []any
		}{
			{[]any{"ab", 3, "x", []byte{0, 0xff}}, []any{"xab", "ab ", "ababab", "ba", string([]byte{0xff, 0})}},
			{[]any{"é🙂", 1, "界", []byte{0xc3, 0xa9}}, []any{"é", "é", "é🙂", "🙂é", string([]byte{0xa9, 0xc3})}},
			{[]any{nil, 3, "x", nil}, []any{nil, nil, nil, nil, nil}},
			{[]any{"ab", 3, "x", []byte{0, 0xff}}, []any{"xab", "ab ", "ababab", "ba", string([]byte{0xff, 0})}},
		} {
			s := referenceSelect(q, names, types, [][]any{tc.row}, tc.args...)
			s.checkMetadata = func(t *testing.T, r *sql.Rows) {
				ct, err := r.ColumnTypes()
				if err != nil {
					t.Fatal(err)
				}
				stringPadColumns(t, ct, names, types, b.pg)
			}
			s.checkRaw = func(t *testing.T, col, row int, v any) bool {
				if col != 4 || v == nil {
					return false
				}
				got, ok := v.([]byte)
				if !ok || !bytes.Equal(got, []byte(tc.row[4].(string))) {
					t.Fatalf("binary payload %T %v", v, v)
				}
				return true
			}
			referenceRunStep(t, p.ctx, held, b.recorder, s)
		}
		referenceRunStep(t, p.ctx, c, b.recorder, referenceExec("CREATE TABLE sp_empty(s varchar(4),v bytea)", 0))
		for _, empty := range []bool{true, false} {
			if !empty {
				referenceRunStep(t, p.ctx, c, b.recorder, referenceExec("INSERT INTO sp_empty VALUES(NULL,NULL)", 1))
			}
			for _, prepared := range []bool{false, true} {
				q := "SELECT lpad(s,2),rpad(s,2),repeat(s,2),reverse(s),reverse(v) AS binary FROM sp_empty"
				var stmt *sql.Stmt
				var rows *sql.Rows
				var err error
				if prepared {
					stmt, err = c.PrepareContext(p.ctx, q)
					if err != nil {
						t.Fatal(err)
					}
					rows, err = stmt.QueryContext(p.ctx)
				} else {
					rows, err = c.QueryContext(p.ctx, q)
				}
				if err != nil {
					t.Fatal(err)
				}
				ct, err := rows.ColumnTypes()
				if err != nil {
					t.Fatal(err)
				}
				stringPadColumns(t, ct, names, types, b.pg)
				if !empty {
					if !rows.Next() {
						t.Fatal("missing NULL row", rows.Err())
					}
					var v [5]any
					if err := rows.Scan(&v[0], &v[1], &v[2], &v[3], &v[4]); err != nil {
						t.Fatal(err)
					}
					for _, x := range v {
						if x != nil {
							t.Fatal(v)
						}
					}
				}
				if rows.Next() {
					t.Fatal("extra row")
				}
				if err := rows.Err(); err != nil {
					t.Fatal(err)
				}
				stringPadColumns(t, ct, names, types, b.pg)
				if err := rows.Close(); err != nil {
					t.Fatal(err)
				}
				stringPadColumns(t, ct, names, types, b.pg)
				if stmt != nil {
					stmt.Close()
				}
			}
		}
	})
}

func TestReferenceStringPadBinaryOwnership(t *testing.T) {
	stringPadScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		q := "SELECT reverse($1::bytea)"
		stmt, err := c.PrepareContext(p.ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		input := []byte{0, 0xff, 0x80, 0x41}
		var first, second []byte
		if err := stmt.QueryRowContext(p.ctx, input).Scan(&first); err != nil {
			t.Fatal(err)
		}
		input[0] = 0x42
		if !bytes.Equal(first, []byte{0x41, 0x80, 0xff, 0}) {
			t.Fatal(first)
		}
		first[0] = 0x43
		if err := stmt.QueryRowContext(p.ctx, input).Scan(&second); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(first, []byte{0x43, 0x80, 0xff, 0}) || !bytes.Equal(second, []byte{0x41, 0x80, 0xff, 0x42}) {
			t.Fatal(first, second)
		}
	})
}

func TestReferenceStringPadFeatureSmoke(t *testing.T) {
	stringPadScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		stringPadBoth(t, p, b, c, referenceSelect(`SELECT lpad('é🙂',5,'界x'),rpad('é🙂',5,'界x'),lpad('hi',5),rpad('hi',5),repeat('Pg',3),reverse('Aé🙂Z'),lpad('é🙂',1,'x'),reverse('\x00ff8041'::bytea)`, []string{"lpad", "rpad", "lpad", "rpad", "repeat", "reverse", "lpad", "reverse"}, []string{"TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "BYTEA"}, [][]any{{"界x界é🙂", "é🙂界x界", "   hi", "hi   ", "PgPgPg", "Z🙂éA", "é", string([]byte{0x41, 0x80, 0xff, 0})}}))
		q := "SELECT lpad($1,$2,$3) AS v"
		stmt, err := c.PrepareContext(p.ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := referenceHeldStatement{referenceExecutor: c, statement: stmt, query: q}
		for _, tc := range []struct {
			arg  any
			want any
		}{{"hi", "xyxhi"}, {nil, nil}, {"é🙂", "xyxé🙂"}} {
			referenceRunStep(t, p.ctx, held, b.recorder, referenceSelect(q, []string{"v"}, []string{"TEXT"}, [][]any{{tc.want}}, tc.arg, 5, "xy"))
		}
		s := referenceErrorDiagnostic(q, controlsReferenceDiagnostic("54000", "requested length too large"), "x", 268435455, "x")
		s.selectRows = true
		s.errorStage = "execute"
		referenceRunStep(t, p.ctx, held, b.recorder, s)
		referenceRunStep(t, p.ctx, held, b.recorder, referenceSelect(q, []string{"v"}, []string{"TEXT"}, [][]any{{"xyxhi"}}, "hi", 5, "xy"))
	})
}
