package reference_test

import (
	"context"
	"database/sql"
	"math"
	"reflect"
	"testing"
)

func TestReferenceBinaryBasicBehavior(t *testing.T) {
	for route, nonContext := range []bool{false, true} {
		t.Run([]string{"context", "consumer"}[route], func(t *testing.T) {
			p := newReferenceScenario(t)
			apply := func(t *testing.T, s referenceStep) { s.nonContext = nonContext; p.step(t, s) }
			run := func(t *testing.T, ctx context.Context, x referenceExecutor, r *referenceRecorder, s referenceStep) {
				s.nonContext = nonContext
				referenceRunStep(t, ctx, x, r, s)
			}
			apply(t, referenceExec("CREATE TABLE t(id INTEGER,v BYTEA,s BYTEA)", 0))
			if nonContext {
				apply(t, referenceExec(`INSERT INTO t VALUES(1,'\x00ff8041008042ff00','\xff8000')`, 1))
				apply(t, referenceExec(`INSERT INTO t VALUES(2,'\x010201030201','\x0201'),(3,'\x010201030201','\x01010102'),(4,'\x010201','\x0201'),(5,'\xc3a9f09f9880',''),(6,'','\xff'),(7,'',''),(8,NULL,'\xff'),(9,'\xff',NULL),(10,NULL,NULL),(11,' A ','\x00')`, 10))
			} else {
				apply(t, referenceExec(`INSERT INTO t VALUES(1,'\x00ff8041008042ff00','\xff8000'),(2,'\x010201030201','\x0201'),(3,'\x010201030201','\x01010102'),(4,'\x010201','\x0201'),(5,'\xc3a9f09f9880',''),(6,'','\xff'),(7,'',''),(8,NULL,'\xff'),(9,'\xff',NULL),(10,NULL,NULL),(11,' A ','\x00')`, 11))
			}
			s := referenceSelect("SELECT v||s AS cat,bit_length(v) AS bits,octet_length(v) AS octets,length(v) AS len,btrim(v,s) AS bt,ltrim(v,s) AS lt,rtrim(v,s) AS rt FROM t WHERE id=1", []string{"cat", "bits", "octets", "len", "bt", "lt", "rt"}, []string{"BYTEA", "INT4", "INT4", "INT4", "BYTEA", "BYTEA", "BYTEA"}, [][]any{{"\x00\xff\x80A\x00\x80B\xff\x00\xff\x80\x00", int64(72), int64(9), int64(9), "A\x00\x80B", "A\x00\x80B\xff\x00", "\x00\xff\x80A\x00\x80B"}})
			for _, mode := range []string{"hex", "escape"} {
				apply(t, referenceExec("SET bytea_output="+mode, 0))
				for _, backend := range p.backends() {
					s.checkMetadata = func(t *testing.T, rows *sql.Rows) {
						types, err := rows.ColumnTypes()
						if err != nil || len(types) != 7 {
							t.Fatal(types, err)
						}
						for i, typ := range types {
							scan := reflect.TypeFor[[]byte]()
							if i >= 1 && i <= 3 {
								scan = reflect.TypeFor[int64]()
								if backend.pg {
									scan = reflect.TypeFor[int32]()
								}
							}
							if typ.ScanType() != scan {
								t.Fatal(i, typ.ScanType(), scan)
							}
						}
					}
					run(t, p.ctx, backend.db, backend.recorder, s)
					run(t, p.ctx, backend.db, backend.recorder, referencePrepared(s))
				}
			}
			// Canonical fixture rows use raw strings; separate ownership tests assert []byte.
			apply(t, referenceExec("SET bytea_output=hex", 0))
			var boundaryRows [][]any
			for _, tc := range []struct {
				id         int
				bt, lt, rt any
				n, bits    any
			}{
				{2, "\x03", "\x03\x02\x01", "\x01\x02\x01\x03", int64(6), int64(48)},
				{3, "\x03", "\x03\x02\x01", "\x01\x02\x01\x03", int64(6), int64(48)},
				{4, "", "", "", int64(3), int64(24)},
				{5, "\xc3\xa9\xf0\x9f\x98\x80", "\xc3\xa9\xf0\x9f\x98\x80", "\xc3\xa9\xf0\x9f\x98\x80", int64(6), int64(48)},
				{6, "", "", "", int64(0), int64(0)},
				{7, "", "", "", int64(0), int64(0)},
				{8, nil, nil, nil, nil, nil}, {9, nil, nil, nil, int64(1), int64(8)}, {10, nil, nil, nil, nil, nil},
				{11, " A ", " A ", " A ", int64(3), int64(24)},
			} {
				boundaryRows = append(boundaryRows, []any{tc.bt, tc.lt, tc.rt, tc.n, tc.n, tc.bits})
				step := referenceSelect("SELECT btrim(v,s) AS bt,ltrim(v,s) AS lt,rtrim(v,s) AS rt,length(v) AS len,octet_length(v) AS octets,bit_length(v) AS bits FROM t WHERE id=$1", []string{"bt", "lt", "rt", "len", "octets", "bits"}, []string{"BYTEA", "BYTEA", "BYTEA", "INT4", "INT4", "INT4"}, [][]any{{tc.bt, tc.lt, tc.rt, tc.n, tc.n, tc.bits}}, tc.id)
				apply(t, step)
				apply(t, referencePrepared(step))
			}
			apply(t, referenceSelect("SELECT btrim(v,s),ltrim(v,s),rtrim(v,s),length(v),octet_length(v),bit_length(v) FROM t WHERE id>1 ORDER BY id", []string{"btrim", "ltrim", "rtrim", "length", "octet_length", "bit_length"}, []string{"BYTEA", "BYTEA", "BYTEA", "INT4", "INT4", "INT4"}, boundaryRows))
			for _, q := range []string{"SELECT length(v) AS n FROM t WHERE id=99", "SELECT length(v) AS n FROM t WHERE id=10"} {
				rows := [][]any{}
				if q == "SELECT length(v) AS n FROM t WHERE id=10" {
					rows = [][]any{{nil}}
				}
				step := referenceSelect(q, []string{"n"}, []string{"INT4"}, rows)
				apply(t, step)
				apply(t, referencePrepared(step))
			}
			for _, id := range []int{10, 99} {
				want := [][]any(nil)
				if id == 10 {
					want = [][]any{{nil, nil}}
				}
				empty := referenceSelect("SELECT btrim(v,s) AS b,length(v) AS n FROM t WHERE id=$1", []string{"b", "n"}, []string{"BYTEA", "INT4"}, want, id)
				for _, backend := range p.backends() {
					var captured []*sql.ColumnType
					empty.checkMetadata = func(t *testing.T, rows *sql.Rows) {
						ct, err := rows.ColumnTypes()
						captured = ct
						scan := reflect.TypeFor[int64]()
						if backend.pg {
							scan = reflect.TypeFor[int32]()
						}
						if err != nil || len(ct) != 2 || ct[0].ScanType() != reflect.TypeFor[[]byte]() || ct[1].ScanType() != scan {
							t.Fatal(ct, err)
						}
						length, known := ct[0].Length()
						if backend.pg {
							if !known || length != math.MaxInt64 {
								t.Fatal("pq BYTEA length", length, known)
							}
						} else if known {
							t.Fatal("native computed BYTEA length known")
						}
						if _, _, known := ct[0].DecimalSize(); known {
							t.Fatal("computed BYTEA precision known")
						}

					}
					run(t, p.ctx, backend.db, backend.recorder, empty)
					if captured[0].DatabaseTypeName() != "BYTEA" {
						t.Fatal("metadata lost after close")
					}
				}
			}
			apply(t, referenceSelect("SELECT bit_length(v||s) AS bits,octet_length(v||s) AS octets,length(v||s) AS len FROM t WHERE id=1", []string{"bits", "octets", "len"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(96), int64(12), int64(12)}}))
			apply(t, referenceSelect("SELECT v||'' AS a,''||v AS b,btrim(v,NULL) AS c,btrim(NULL,v) AS d FROM t WHERE id=7", []string{"a", "b", "c", "d"}, []string{"BYTEA", "BYTEA", "BYTEA", "BYTEA"}, [][]any{{"", "", nil, nil}}))
			apply(t, referenceSelect("SELECT pg_catalog.btrim(v,s) AS bt,v OPERATOR(pg_catalog.||) s AS cat FROM t WHERE id=1", []string{"bt", "cat"}, []string{"BYTEA", "BYTEA"}, [][]any{{"A\x00\x80B", "\x00\xff\x80A\x00\x80B\xff\x00\xff\x80\x00"}}))
		})
	}
}

func TestReferenceBinaryBasicConcatNullAndEmpty(t *testing.T) {
	for route, nonContext := range []bool{false, true} {
		t.Run([]string{"context", "consumer"}[route], func(t *testing.T) {
			p := newReferenceScenario(t)
			apply := func(t *testing.T, s referenceStep) { s.nonContext = nonContext; p.step(t, s) }
			apply(t, referenceExec("CREATE TABLE t(id INTEGER,v BYTEA,s BYTEA)", 0))
			apply(t, referenceExec(`INSERT INTO t VALUES(1,'','\xff'),(2,'\xff',''),(3,'',''),(4,NULL,'\xff'),(5,'\xff',NULL),(6,NULL,NULL)`, 6))
			s := referenceSelect("SELECT v||s AS cat FROM t ORDER BY id", []string{"cat"}, []string{"BYTEA"}, [][]any{{"\xff"}, {"\xff"}, {""}, {nil}, {nil}, {nil}})
			apply(t, s)
			apply(t, referencePrepared(s))
		})
	}
}
