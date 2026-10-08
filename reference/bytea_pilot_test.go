package reference_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"math"
	"reflect"
	"testing"
)

func TestReferenceByteaInputBindsMetadata(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			ctx := context.WithValue(p.ctx, referenceRecorderKey{}, backend.recorder)
			db := backend.db
			checkExec := func(result sql.Result, err error, count int64) {
				t.Helper()
				if err != nil {
					t.Fatal(err)
				}
				got, err := result.RowsAffected()
				if err != nil || got != count {
					t.Fatal(got, count, err)
				}
				backend.recorder.mu.Lock()
				op := backend.recorder.operations[len(backend.recorder.operations)-1]
				recorded, recordErr := op.Affected, op.Err
				backend.recorder.mu.Unlock()
				if recordErr != nil || recorded != count {
					t.Fatal("recorded affected", recorded, count, recordErr)
				}
			}
			exec := func(q string, count int64) {
				t.Helper()
				result, err := db.ExecContext(ctx, q)
				checkExec(result, err, count)
			}
			exec("CREATE TABLE t(id INTEGER,v BYTEA)", 0)
			exec("INSERT INTO t VALUES(1,''),(2,'\\x'),(3,'\\x00aAfF'),(4,'\\x 00\tAA\nff\r'),(5,'\\000\\177\\200\\377'),(6,'\\\\'),(7,'''') ,(8,'é')", 8)
			want := []any{[]byte{}, []byte{}, []byte{0, 170, 255}, []byte{0, 170, 255}, []byte{0, 127, 128, 255}, []byte{92}, []byte{39}, []byte{195, 169}}
			for _, prepared := range []bool{false, true} {
				func() {
					var stmt *sql.Stmt
					if prepared {
						var err error
						stmt, err = db.PrepareContext(ctx, "INSERT INTO t VALUES($1,$2)")
						if err != nil {
							t.Fatal(err)
						}
						defer func() {
							if err := stmt.Close(); err != nil {
								t.Error(err)
							}
						}()
					}
					for _, tc := range []struct{ arg, want any }{
						{string([]byte{0, 128, 255}), []byte{0, 128, 255}}, {[]byte{0, 128, 255}, []byte{0, 128, 255}},
						{`\x41`, []byte{92, 120, 52, 49}}, {[]byte(`\000`), []byte{92, 48, 48, 48}},
						{nil, nil}, {[]byte(nil), nil}, {[]byte{}, []byte{}}, {"", []byte{}},
					} {
						var result sql.Result
						var err error
						if prepared {
							result, err = stmt.ExecContext(ctx, len(want)+1, tc.arg)
						} else {
							result, err = db.ExecContext(ctx, "INSERT INTO t VALUES($1,$2)", len(want)+1, tc.arg)
						}
						checkExec(result, err, 1)
						backend.recorder.mu.Lock()
						op := backend.recorder.operations[len(backend.recorder.operations)-1]
						arg := op.Args[1].Value
						backend.recorder.mu.Unlock()
						if !reflect.DeepEqual(arg, tc.arg) {
							t.Fatalf("owned bind = %#v, want %#v", arg, tc.arg)
						}
						if b, ok := tc.arg.([]byte); ok && len(b) > 0 {
							first := b[0]
							b[0] ^= 255
							if arg.([]byte)[0] != first {
								t.Fatal("source mutation changed recorded bind")
							}
						}
						want = append(want, tc.want)
					}
				}()
			}
			for _, prepared := range []bool{false, true} {
				for _, empty := range []bool{false, true} {
					func() {
						q := "SELECT v FROM t ORDER BY id"
						if empty {
							q = "SELECT v FROM t WHERE v=NULL"
						}
						var stmt *sql.Stmt
						var rows *sql.Rows
						var err error
						if prepared {
							stmt, err = db.PrepareContext(ctx, q)
							if err != nil {
								t.Fatal(err)
							}
							defer func() {
								if err := stmt.Close(); err != nil {
									t.Error(err)
								}
							}()
							rows, err = stmt.QueryContext(ctx)
						} else {
							rows, err = db.QueryContext(ctx, q)
						}
						if rows != nil {
							defer rows.Close()
						}
						if err != nil {
							t.Fatal(err)
						}
						columns, err := rows.Columns()
						if err != nil || !reflect.DeepEqual(columns, []string{"v"}) {
							t.Fatal(columns, err)
						}
						types, err := rows.ColumnTypes()
						if err != nil || len(types) != 1 {
							t.Fatal(types, err)
						}
						if types[0].Name() != "v" || types[0].DatabaseTypeName() != "BYTEA" || types[0].ScanType() != reflect.TypeFor[[]byte]() {
							t.Fatal(types[0])
						}
						length, known := types[0].Length()
						if backend.pg {
							if !known || length != math.MaxInt64 {
								t.Fatal(length, known)
							}
						} else if known {
							t.Fatal(length, known)
						}
						nullable, known := types[0].Nullable()
						if backend.pg {
							if known {
								t.Fatal(nullable, known)
							}
						} else if !nullable || !known {
							t.Fatal(nullable, known)
						}
						if _, _, known := types[0].DecimalSize(); known {
							t.Fatal("decimal size")
						}
						capturedWant := make([][]driver.Value, 0)
						i := 0
						for rows.Next() {
							var value any
							if err := rows.Scan(&value); err != nil {
								t.Fatal(err)
							}
							if empty || i >= len(want) || !reflect.DeepEqual(value, want[i]) {
								t.Fatalf("row %d = %#v, want %#v", i, value, want)
							}
							capturedWant = append(capturedWant, []driver.Value{want[i]})
							if b, ok := value.([]byte); ok && len(b) > 0 {
								b[0] ^= 255
							}
							i++
						}
						if err := rows.Err(); err != nil {
							t.Fatal(err)
						}
						if err := rows.Close(); err != nil {
							t.Fatal(err)
						}
						count := len(want)
						if empty {
							count = 0
						}
						if i != count || types[0].DatabaseTypeName() != "BYTEA" {
							t.Fatal(i, count, types[0])
						}
						backend.recorder.mu.Lock()
						op := backend.recorder.operations[len(backend.recorder.operations)-1]
						matched := reflect.DeepEqual(op.Columns, []string{"v"}) && len(op.Rows) == len(capturedWant)
						if matched {
							for i := range capturedWant {
								if !reflect.DeepEqual(op.Rows[i], capturedWant[i]) {
									matched = false
								}
							}
						}
						closed, closeErr, recordErr := op.Closed, op.CloseErr, op.Err
						backend.recorder.mu.Unlock()
						if !matched || !closed || closeErr != nil || recordErr != nil {
							t.Fatal("raw owned recording/cleanup mismatch", matched, closed, closeErr, recordErr)
						}
					}()
				}
			}
		})
	}
}
