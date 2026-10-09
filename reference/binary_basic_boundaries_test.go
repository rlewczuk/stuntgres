package reference_test

import (
	"context"
	"database/sql"
	"reflect"
	"testing"
)

func TestReferenceBinaryBasicPreparedFrames(t *testing.T) {
	p := newReferenceScenario(t)
	q := "SELECT v||$1 AS cat,btrim(v,$1) AS bt,ltrim(v,$1) AS lt,rtrim(v,$1) AS rt FROM t"
	v := "\x00\xff\x80A\x00\x80B\xff\x00"
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			conn, err := backend.db.Conn(p.ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			for _, s := range []referenceStep{referenceExec("CREATE TABLE t(v BYTEA,s BYTEA)", 0), referenceExec(`INSERT INTO t VALUES('\x00ff8041008042ff00','\xff8000')`, 1)} {
				referenceRunStep(t, p.ctx, conn, backend.recorder, s)
			}
			stmt, err := conn.PrepareContext(p.ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			executor := referenceHeldStatement{referenceExecutor: conn, statement: stmt, query: q}
			for _, mode := range []string{"hex", "escape"} {
				referenceRunStep(t, p.ctx, conn, backend.recorder, referenceExec("SET bytea_output="+mode, 0))
				for _, tc := range []struct {
					arg  any
					want []any
				}{
					{[]byte{255, 128, 0}, []any{"\x00\xff\x80A\x00\x80B\xff\x00\xff\x80\x00", "A\x00\x80B", "A\x00\x80B\xff\x00", "\x00\xff\x80A\x00\x80B"}},
					{[]byte{}, []any{v, v, v, v}}, {nil, []any{nil, nil, nil, nil}},
					{`\x41`, []any{"\x00\xff\x80A\x00\x80B\xff\x00\\x41", v, v, v}},
					{[]byte{255, 128, 0}, []any{"\x00\xff\x80A\x00\x80B\xff\x00\xff\x80\x00", "A\x00\x80B", "A\x00\x80B\xff\x00", "\x00\xff\x80A\x00\x80B"}},
				} {
					step := referenceSelect(q, []string{"cat", "bt", "lt", "rt"}, []string{"BYTEA", "BYTEA", "BYTEA", "BYTEA"}, [][]any{tc.want}, tc.arg)
					step.checkMetadata = func(t *testing.T, rows *sql.Rows) {
						if input, ok := tc.arg.([]byte); ok && len(input) > 0 {
							input[0] = 42
						}
					}
					step.checkScan = func(t *testing.T, rows *sql.Rows, _ int) {
						var cat, bt, lt, rt any
						if err := rows.Scan(&cat, &bt, &lt, &rt); err != nil {
							t.Fatal(err)
						}
						if output, ok := cat.([]byte); ok && len(output) > 0 {
							output[0] = 99
						}
						if !reflect.DeepEqual(referenceNormalize(bt), tc.want[1]) {
							t.Fatal("sibling buffer alias", bt, tc.want[1])
						}
					}
					referenceRunStep(t, p.ctx, executor, backend.recorder, step)
				}
			}
			referenceRunStep(t, p.ctx, conn, backend.recorder, referencePrepared(referenceSelect("SELECT $1||v AS cat,btrim($1,s) AS bt FROM t", []string{"cat", "bt"}, []string{"BYTEA", "BYTEA"}, [][]any{{"\x00A\xff\x00\xff\x80A\x00\x80B\xff\x00", "A"}}, []byte{0, 65, 255})))
			for _, tc := range []struct {
				q    string
				arg  any
				want []byte
			}{
				{"SELECT $1||v FROM t", []byte{1}, []byte{1, 0, 255, 128, 65, 0, 128, 66, 255, 0}},
				{"SELECT btrim($1,s) FROM t", []byte{0, 65, 255}, []byte{65}},
				{`SELECT btrim(v,'\x0080ff') FROM t`, nil, []byte{65, 0, 128, 66}},
			} {
				single, err := conn.PrepareContext(p.ctx, tc.q)
				if err != nil {
					t.Fatal(err)
				}
				var got []byte
				if tc.arg == nil {
					err = single.QueryRowContext(p.ctx).Scan(&got)
				} else {
					err = single.QueryRowContext(p.ctx, tc.arg).Scan(&got)
				}
				if err := single.Close(); err != nil {
					t.Fatal(err)
				}
				if err != nil || !reflect.DeepEqual(got, tc.want) {
					t.Fatal(got, tc.want, err)
				}
			}
			var stored []byte
			if err := conn.QueryRowContext(p.ctx, "SELECT v FROM t").Scan(&stored); err != nil || !reflect.DeepEqual(stored, []byte(v)) {
				t.Fatal(stored, err)
			}

		})
	}
}

func TestReferenceBinaryBasicConsumersAndNativeControls(t *testing.T) {
	for route, nonContext := range []bool{false, true} {
		t.Run([]string{"context", "consumer"}[route], func(t *testing.T) {
			p := newReferenceScenario(t)
			apply := func(t *testing.T, s referenceStep) { s.nonContext = nonContext; p.step(t, s) }
			apply(t, referenceExec("CREATE TABLE t(id INTEGER,v BYTEA,s BYTEA)", 0))
			apply(t, referenceExec(`INSERT INTO t VALUES(1,'\x00ff8041008042ff00','\xff8000')`, 1))
			// The selected provider composes with existing master identity casts;
			// this part adds no cast admission or conversion edges.
			cast := referenceSelect("SELECT v::bytea AS v,length(v::bytea) AS n FROM t", []string{"v", "n"}, []string{"BYTEA", "INT4"}, [][]any{{"\x00\xff\x80A\x00\x80B\xff\x00", int64(9)}})
			apply(t, cast)
			apply(t, referencePrepared(cast))
			apply(t, referenceExec("CREATE TABLE dst(v BYTEA)", 0))
			apply(t, referenceExec("INSERT INTO dst SELECT v||s FROM t WHERE length(v)=9", 1))
			apply(t, referenceSelect("SELECT v FROM dst", []string{"v"}, []string{"BYTEA"}, [][]any{{"\x00\xff\x80A\x00\x80B\xff\x00\xff\x80\x00"}}))
			apply(t, referenceSelect("SELECT length(v) AS n,count(*) AS c FROM t GROUP BY length(v)", []string{"n", "c"}, []string{"INT4", "INT8"}, [][]any{{int64(9), int64(1)}}))
			grouped := referenceSelect("SELECT length(v) AS n,count(*) AS c FROM t GROUP BY 1", []string{"n", "c"}, []string{"INT4", "INT8"}, [][]any{{int64(9), int64(1)}})
			apply(t, grouped)
			apply(t, referencePrepared(grouped))
			apply(t, referenceSelect(`SELECT bit_length(v) FROM t WHERE btrim(v,s)='\x41008042' ORDER BY octet_length(v)`, []string{"bit_length"}, []string{"INT4"}, [][]any{{int64(72)}}))
			apply(t, referenceSelect("SELECT CASE WHEN length(v)=9 THEN btrim(v,s) ELSE v END FROM t", []string{"v"}, []string{"BYTEA"}, [][]any{{"A\x00\x80B"}}))
			apply(t, referencePrepared(referenceSelect("UPDATE dst SET v=btrim(v,$1) RETURNING v,length(v) AS n", []string{"v", "n"}, []string{"BYTEA", "INT4"}, [][]any{{"A\x00\x80B", int64(4)}}, []byte{0, 128, 255})))
			bad := referenceImplicitError(`INSERT INTO dst SELECT v||'\q' FROM t`, referenceDiagnostic{Code: "22P02", Message: "invalid input syntax for type bytea", Position: "27"})
			bad.errorStage = "execute"
			apply(t, bad)
			bad.errorStage = "prepare"
			apply(t, referencePrepared(bad))
			apply(t, referenceSelect("SELECT v FROM dst", []string{"v"}, []string{"BYTEA"}, [][]any{{"A\x00\x80B"}}))
			apply(t, referenceExec("CREATE TABLE dst_updated(v BYTEA)", 0))
			apply(t, referenceExec("INSERT INTO dst_updated SELECT v FROM dst", 1))
			apply(t, referenceExec(`UPDATE dst SET v='\x00ff8041008042ff00ff8000'`, 1))
			apply(t, referenceSelect("SELECT v FROM dst", []string{"v"}, []string{"BYTEA"}, [][]any{{"\x00\xff\x80A\x00\x80B\xff\x00\xff\x80\x00"}}))
			apply(t, referenceSelect("SELECT v FROM dst_updated", []string{"v"}, []string{"BYTEA"}, [][]any{{"A\x00\x80B"}}))
		})
	}
}

func TestReferenceBinaryBasicRawMetadataAndOwnership(t *testing.T) {
	p := newReferenceScenario(t)
	p.step(t, referenceExec("CREATE TABLE t(v BYTEA,s BYTEA)", 0))
	p.step(t, referenceExec(`INSERT INTO t VALUES('\x00ff8041008042ff00','\xff8000')`, 1))
	q := "SELECT v||s AS cat,bit_length(v) AS bits,octet_length(v) AS octets,length(v) AS len,btrim(v,s) AS bt,ltrim(v,s) AS lt,rtrim(v,s) AS rt FROM t"
	want := []any{[]byte{0, 255, 128, 65, 0, 128, 66, 255, 0, 255, 128, 0}, int64(72), int64(9), int64(9), []byte{65, 0, 128, 66}, []byte{65, 0, 128, 66, 255, 0}, []byte{0, 255, 128, 65, 0, 128, 66}}
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			conn, err := backend.db.Conn(p.ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			stmt, err := conn.PrepareContext(p.ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			for _, prepared := range []bool{false, true} {
				var rows *sql.Rows
				if prepared {
					rows, err = stmt.QueryContext(p.ctx)
				} else {
					rows, err = conn.QueryContext(p.ctx, q)
				}
				if err != nil {
					t.Fatal(err)
				}
				types, err := rows.ColumnTypes()
				if err != nil || len(types) != 7 {
					t.Fatal(types, err)
				}
				for i, typ := range types {
					name, scan := "BYTEA", reflect.TypeFor[[]byte]()
					if i >= 1 && i <= 3 {
						name, scan = "INT4", reflect.TypeFor[int64]()
						if backend.pg {
							scan = reflect.TypeFor[int32]()
						}
					}
					if typ.DatabaseTypeName() != name || typ.ScanType() != scan {
						t.Fatal(i, typ.DatabaseTypeName(), typ.ScanType())
					}
				}
				if !rows.Next() {
					t.Fatal(rows.Err())
				}
				got := make([]any, 7)
				dest := make([]any, 7)
				for i := range got {
					dest[i] = &got[i]
				}
				if err := rows.Scan(dest...); err != nil || !reflect.DeepEqual(got, want) {
					t.Fatal(got, want, err)
				}
				got[0].([]byte)[0] = 99
				if !reflect.DeepEqual(got[4], want[4]) {
					t.Fatal("exported sibling alias")
				}
				if rows.Next() || rows.Err() != nil {
					t.Fatal(rows.Err())
				}
				rows.Close()
			}
			input := []byte{255, 128, 0}
			held, err := conn.PrepareContext(p.ctx, "SELECT v||$1 AS cat FROM t")
			if err != nil {
				t.Fatal(err)
			}
			defer held.Close()
			rows, err := held.QueryContext(p.ctx, input)
			if err != nil {
				t.Fatal(err)
			}
			input[0] = 42
			if !rows.Next() {
				t.Fatal(rows.Err())
			}
			var got []byte
			if err := rows.Scan(&got); err != nil || !reflect.DeepEqual(got, want[0]) {
				t.Fatal(got, err)
			}
			if rows.Next() || rows.Err() != nil {
				t.Fatal(rows.Err())
			}
			rows.Close()
			var stored []byte
			if err := conn.QueryRowContext(p.ctx, "SELECT v FROM t").Scan(&stored); err != nil || !reflect.DeepEqual(stored, []byte{0, 255, 128, 65, 0, 128, 66, 255, 0}) {
				t.Fatal(stored, err)
			}
		})
	}
}

func TestReferenceBinaryBasicLateInputAtomicity(t *testing.T) {
	for route, nonContext := range []bool{false, true} {
		t.Run([]string{"context", "consumer"}[route], func(t *testing.T) {
			p := newReferenceScenario(t)
			apply := func(t *testing.T, s referenceStep) { s.nonContext = nonContext; p.step(t, s) }
			apply(t, referenceExec("CREATE TABLE dst(v BYTEA)", 0))
			apply(t, referenceExec(`INSERT INTO dst VALUES('\x41')`, 1))
			s := referenceImplicitError(`INSERT INTO dst VALUES($1),(btrim($1,'\q'))`, referenceDiagnostic{Code: "22P02", Message: "invalid input syntax for type bytea", Position: "38"}, []byte{0, 255})
			s.errorStage = "execute"
			apply(t, s)
			s.errorStage = "prepare"
			apply(t, referencePrepared(s))
			apply(t, referenceSelect("SELECT v FROM dst", []string{"v"}, []string{"BYTEA"}, [][]any{{"A"}}))
		})
	}
}

func TestReferenceBinaryBasicEncodingLengthSignatures(t *testing.T) {
	for route, nonContext := range []bool{false, true} {
		t.Run([]string{"context", "consumer"}[route], func(t *testing.T) {
			p := newReferenceScenario(t)
			apply := func(t *testing.T, s referenceStep) { s.nonContext = nonContext; p.step(t, s) }
			run := func(t *testing.T, ctx context.Context, x referenceExecutor, r *referenceRecorder, s referenceStep) {
				s.nonContext = nonContext
				referenceRunStep(t, ctx, x, r, s)
			}
			apply(t, referenceExec("CREATE TABLE enc(v BYTEA,t TEXT,vc VARCHAR(4),bp CHAR(4),n NAME)", 0))
			apply(t, referenceExec(`INSERT INTO enc VALUES('\xc3a9','UTF8','UTF8','UTF8','UTF8')`, 1))
			for _, backend := range p.backends() {
				t.Run(backend.name, func(t *testing.T) {
					for _, tc := range []struct {
						q    string
						want any
					}{
						{"SELECT length(NULL,'UTF8') AS n", nil},
						{"SELECT length(v,t) AS n FROM enc", int64(1)},
						{"SELECT length(v,vc) AS n FROM enc", int64(1)},
						{"SELECT length(v,bp) AS n FROM enc", int64(1)},
						{"SELECT length(NULL,t) AS n FROM enc", nil},
						{"SELECT length(NULL,vc) AS n FROM enc", nil},
						{"SELECT length(NULL,bp) AS n FROM enc", nil},
						{"SELECT length(v,n) AS n FROM enc", int64(1)},
						{"SELECT length(v,'UTF8') AS n FROM enc", int64(1)},
						{`SELECT length('\xc3a9','UTF8') AS n`, int64(1)},
					} {
						if backend.pg {
							// Independent PostgreSQL values establish a real overload,
							// not parity with the native capability exclusion.
							run(t, p.ctx, backend.db, backend.recorder, referenceExec(tc.q, 1))
							s := referenceSelect(tc.q, []string{"n"}, []string{"INT4"}, [][]any{{tc.want}})
							run(t, p.ctx, backend.db, backend.recorder, s)
							run(t, p.ctx, backend.db, backend.recorder, referencePrepared(s))
						} else {
							s := referenceImplicitError(tc.q, referenceDiagnostic{Code: "0A000", Message: "encoding-aware length is unsupported", Position: "8"})
							s.errorStage = "execute"
							run(t, p.ctx, backend.db, backend.recorder, s)
							s.selectRows = true
							run(t, p.ctx, backend.db, backend.recorder, s)
							s.errorStage = "prepare"
							run(t, p.ctx, backend.db, backend.recorder, referencePrepared(s))
						}
					}
				})
			}
			s := referenceImplicitError("SELECT length(v,1) FROM enc", referenceDiagnostic{Code: "42883", Message: "function length(bytea, integer) does not exist", Position: "8", Hint: "No function matches the given name and argument types. You might need to add explicit type casts."})
			s.errorStage = "execute"
			apply(t, s)
			s.selectRows = true
			s.errorStage = "execute"
			apply(t, s)
			s.errorStage = "prepare"
			apply(t, referencePrepared(s))
		})
	}
}
