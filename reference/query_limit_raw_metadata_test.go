package reference_test

import (
	"reflect"
	"testing"
)

func TestReferenceQueryLimitRawMetadata(t *testing.T) {
	p := newReferenceScenario(t)
	p.step(t, referenceExec("CREATE TABLE meta(id INT NOT NULL,n NUMERIC(6,2),s VARCHAR(5))", 0))
	p.step(t, referenceExec("INSERT INTO meta VALUES(1,12.30,'abc')", 1))
	for _, backend := range p.backends() {
		for _, prepared := range []bool{false, true} {
			for _, suffix := range []string{"LIMIT 1", "LIMIT 0", "OFFSET 99", "FETCH FIRST ROW WITH TIES", "FETCH FIRST 0 ROWS WITH TIES", "OFFSET 99 FETCH FIRST ROW WITH TIES"} {
				func() {
					rows, closeQuery, err := transactionQuery(p.ctx, backend.db, "SELECT id AS ident,n,s,id+1 AS computed FROM meta ORDER BY id "+suffix, prepared)
					if closeQuery != nil {
						defer closeQuery()
					}
					if err != nil {
						t.Fatal(err)
					}
					defer rows.Close()
					names, err := rows.Columns()
					if err != nil || !reflect.DeepEqual(names, []string{"ident", "n", "s", "computed"}) {
						t.Fatal(names, err)
					}
					meta, err := rows.ColumnTypes()
					if err != nil || len(meta) != 4 {
						t.Fatal(meta, err)
					}
					for i, typ := range []string{"INT4", "NUMERIC", "VARCHAR", "INT4"} {
						if meta[i].Name() != names[i] || meta[i].DatabaseTypeName() != typ {
							t.Fatal(meta[i])
						}
						nullable, known := meta[i].Nullable()
						if backend.pg {
							if known {
								t.Fatal("pq physical nullable unknown", i)
							}
							wantScan := []reflect.Type{reflect.TypeFor[int32](), reflect.TypeFor[any](), reflect.TypeFor[string](), reflect.TypeFor[int32]()}[i]
							if meta[i].ScanType() != wantScan {
								t.Fatal(meta[i].ScanType(), wantScan)
							}
						} else if i == 0 {
							if !known || nullable {
								t.Fatal("physical NOT NULL lost")
							}
						} else if i == 1 {
							if !known || !nullable {
								t.Fatal("physical nullable lost")
							}
						} else if i == 3 && known {
							t.Fatal("computed nullable invented")
						}
					}
					if precision, scale, known := meta[1].DecimalSize(); !known || precision != 6 || scale != 2 {
						t.Fatal(precision, scale, known)
					}
					if length, known := meta[2].Length(); !known || length != 5 {
						t.Fatal(length, known)
					}
					count := 0
					for rows.Next() {
						count++
						var id, n, s, k any
						if err := rows.Scan(&id, &n, &s, &k); err != nil {
							t.Fatal(err)
						}
						if backend.pg {
							bytes, ok := n.([]byte)
							if id != int64(1) || !ok || string(bytes) != "12.30" || s != "abc" || k != int64(2) {
								t.Fatal(id, n, s, k)
							}
						}
					}
					want := 0
					if suffix == "LIMIT 1" || suffix == "FETCH FIRST ROW WITH TIES" {
						want = 1
					}
					if rows.Err() != nil || count != want {
						t.Fatal(count, want, rows.Err())
					}
					if err := closeQuery(); err != nil {
						t.Fatal(err)
					}
				}()
			}
		}
	}
	p.step(t, referenceExec("INSERT INTO meta VALUES(2,10.00,'def')", 1))
	for _, tc := range []struct {
		suffix string
		rows   [][]any
	}{{"LIMIT 2", [][]any{{}, {}}}, {"LIMIT 0", nil}, {"OFFSET 99", nil}} {
		step := referenceSelect("SELECT FROM meta "+tc.suffix, []string{}, []string{}, tc.rows)
		p.step(t, step)
		p.step(t, referencePrepared(step))
	}
}
