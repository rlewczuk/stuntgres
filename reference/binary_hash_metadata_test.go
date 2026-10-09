package reference_test

import (
	"database/sql"
	"reflect"
	"testing"
)

func binaryHashColumns(t *testing.T, columns []*sql.ColumnType, pg bool) {
	t.Helper()
	if len(columns) != 7 {
		t.Fatal(columns)
	}
	for i, c := range columns {
		scan := reflect.TypeFor[[]byte]()
		if i < 2 {
			scan = reflect.TypeFor[int64]()
		} else if i == 2 {
			scan = reflect.TypeFor[string]()
		}
		n, nok := c.Nullable()
		l, lok := c.Length()
		a, b, dok := c.DecimalSize()
		if c.Name() != binaryHashNames[i] || c.DatabaseTypeName() != binaryHashTypes[i] || c.ScanType() != scan {
			t.Errorf("column %d name=%s type=%s scan=%v want %s %s %v", i, c.Name(), c.DatabaseTypeName(), c.ScanType(), binaryHashNames[i], binaryHashTypes[i], scan)
		}
		if n || nok || dok {
			t.Errorf("computed nullable/decimal: %v/%v %d,%d/%v", n, nok, a, b, dok)
		}
		if i == 2 || (pg && i > 2) {
			if !lok || l != int64(9223372036854775807) {
				t.Error("variable-length result metadata", l, lok)
			}
		} else if lok {
			t.Error("unexpected length", l)
		}
	}
}
func TestReferenceBinaryHashMetadata(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			binaryHashSetup(t, p, backend.db, backend.recorder)
			for _, id := range []int{99, 0, 2} {
				for _, prepared := range []bool{false, true} {
					q := "SELECT " + binaryHashProjection + " FROM hash_input WHERE id=$1"
					var stmt *sql.Stmt
					var rows *sql.Rows
					var err error
					if prepared {
						stmt, err = backend.db.PrepareContext(p.ctx, q)
						if err != nil {
							t.Fatal(err)
						}
						defer stmt.Close()
						rows, err = stmt.QueryContext(p.ctx, id)
					} else {
						rows, err = backend.db.QueryContext(p.ctx, q, id)
					}
					if err != nil {
						t.Fatal(err)
					}
					defer rows.Close()
					columns, err := rows.ColumnTypes()
					if err != nil {
						t.Fatal(err)
					}
					binaryHashColumns(t, columns, backend.pg)
					if id == 99 {
						if rows.Next() {
							t.Fatal("empty result produced row")
						}
					} else {
						if !rows.Next() {
							t.Fatal("missing row", rows.Err())
						}
						binaryHashRawScan(t, rows, id == 0)
						if rows.Next() {
							t.Fatal("extra row")
						}
					}
					if err := rows.Err(); err != nil {
						t.Fatal(err)
					}
					binaryHashColumns(t, columns, backend.pg)
					if err := rows.Close(); err != nil {
						t.Fatal(err)
					}
					binaryHashColumns(t, columns, backend.pg)
				}
			}
		})
	}
}

func TestReferenceBinaryHashInference(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			for i, name := range binaryHashNames {
				if i == 2 {
					continue
				}
				value := binaryHashRow(t, 1, 891568578, 910901175)[i]
				for _, input := range []string{"'abc'", `'\x616263'`, "VARIADIC 'abc'"} {
					s := referenceSelect("SELECT "+name+"("+input+")", []string{name}, []string{binaryHashTypes[i]}, [][]any{{value}})
					referenceRunStep(t, p.ctx, backend.db, backend.recorder, s)
					referenceRunStep(t, p.ctx, backend.db, backend.recorder, referencePrepared(s))
				}
				referenceRunStep(t, p.ctx, backend.db, backend.recorder, referenceSelect("SELECT "+name+"(NULL)", []string{name}, []string{binaryHashTypes[i]}, [][]any{{nil}}))
				for _, arg := range []any{nil, []byte(nil), []byte{}, "", []byte("abc"), "abc", []byte{0}, string([]byte{0}), []byte{0, 128, 255}, string([]byte{0, 128, 255})} {
					index := 0
					if arg != nil {
						switch v := arg.(type) {
						case string:
							if v == "abc" {
								index = 1
							} else if len(v) == 1 {
								index = 2
							} else if len(v) == 3 {
								index = 3
							}
						case []byte:
							if string(v) == "abc" {
								index = 1
							} else if len(v) == 1 {
								index = 2
							} else if len(v) == 3 {
								index = 3
							}
						}
					}
					var want any
					nilBytes, ok := arg.([]byte)
					if arg != nil && !(ok && nilBytes == nil) {
						crc, crcc := int64(0), int64(0)
						if index == 1 {
							crc, crcc = 891568578, 910901175
						}
						if index == 2 {
							crc, crcc = 3523407757, 1383945041
						}
						if index == 3 {
							crc, crcc = 3921718996, 920259282
						}
						want = binaryHashRow(t, index, crc, crcc)[i]
					}
					s := referenceSelect("SELECT "+name+"($1)", []string{name}, []string{binaryHashTypes[i]}, [][]any{{want}}, arg)
					referenceRunStep(t, p.ctx, backend.db, backend.recorder, s)
					referenceRunStep(t, p.ctx, backend.db, backend.recorder, referencePrepared(s))
				}
			}
			binaryHashSetup(t, p, backend.db, backend.recorder)
			for _, name := range binaryHashNames {
				i := 0
				for binaryHashNames[i] != name {
					i++
				}
				referenceRunStep(t, p.ctx, backend.db, backend.recorder, referenceSelect("SELECT pg_catalog."+name+"(VARIADIC v) FROM hash_input WHERE id=2", []string{name}, []string{binaryHashTypes[i]}, [][]any{{binaryHashRow(t, 1, 891568578, 910901175)[i]}}))
			}
			for _, tc := range []struct {
				q, hex string
				args   []any
			}{
				{`SELECT sha256('\x41')`, "559aead08264d5795d3909718cdd05abd49572e84fe55590eef31a88a08fdffd", nil},
				{`SELECT sha256($1)`, "1896ffde59d289c620736dfe59b6cd146ab433b57ef92479bd77bdd57fcc3e27", []any{`\x41`}},
			} {
				s := referenceSelect(tc.q, []string{"sha256"}, []string{"BYTEA"}, [][]any{{binaryHashBytes(t, tc.hex)}}, tc.args...)
				referenceRunStep(t, p.ctx, backend.db, backend.recorder, s)
				referenceRunStep(t, p.ctx, backend.db, backend.recorder, referencePrepared(s))
			}
			s := referenceSelect("SELECT sha256($1),md5($1) FROM hash_input WHERE v=$1 AND v=$2", []string{"sha256", "md5"}, []string{"BYTEA", "TEXT"}, [][]any{{binaryHashBytes(t, binaryHashCrypto[1][2]), binaryHashCrypto[1][0]}}, []byte("abc"), []byte("abc"))
			referenceRunStep(t, p.ctx, backend.db, backend.recorder, s)
			referenceRunStep(t, p.ctx, backend.db, backend.recorder, referencePrepared(s))
		})
	}
}

func TestReferenceBinaryHashPreparedOwnership(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			conn, err := backend.db.Conn(p.ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			binaryHashSetup(t, p, conn, backend.recorder)
			q := "SELECT sha256($1)"
			stmt, err := conn.PrepareContext(p.ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := referenceHeldStatement{referenceExecutor: conn, statement: stmt, query: q}
			for _, mode := range []string{"hex", "escape"} {
				referenceRunStep(t, p.ctx, conn, backend.recorder, referenceExec("SET bytea_output="+mode, 0))
				invariant := referenceSelect("SELECT "+binaryHashProjection+" FROM hash_input WHERE id=4", binaryHashNames, binaryHashTypes, [][]any{binaryHashRow(t, 3, 3921718996, 920259282)})
				referenceRunStep(t, p.ctx, conn, backend.recorder, invariant)
				referenceRunStep(t, p.ctx, conn, backend.recorder, referencePrepared(invariant))
				for _, tc := range []struct {
					arg   any
					index int
				}{{[]byte("abc"), 1}, {[]byte{0}, 2}, {[]byte{0, 128, 255}, 3}, {[]byte{}, 0}, {nil, -1}, {[]byte(nil), -1}, {[]byte("abc"), 1}} {
					var want any
					if tc.index >= 0 {
						want = binaryHashBytes(t, binaryHashCrypto[tc.index][2])
					}
					s := referenceSelect(q, []string{"sha256"}, []string{"BYTEA"}, [][]any{{want}}, tc.arg)
					s.checkScan = func(t *testing.T, rows *sql.Rows, _ int) {
						var buf []byte
						if err := rows.Scan(&buf); err != nil {
							t.Fatal(err)
						}
						if want == nil {
							if buf != nil {
								t.Fatal(buf)
							}
						} else {
							if string(buf) != want {
								t.Fatal(buf, want)
							}
							buf[0] ^= 255
							var again []byte
							if err := rows.Scan(&again); err != nil || string(again) != want {
								t.Fatal("scan alias", again, err)
							}
						}
					}
					referenceRunStep(t, p.ctx, held, backend.recorder, s)
					referenceRunStep(t, p.ctx, held, backend.recorder, referenceSelect(q, []string{"sha256"}, []string{"BYTEA"}, [][]any{{want}}, tc.arg))
				}
			}
		})
	}
}
