//go:build reference

package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"reflect"
	"stuntgres"
	"testing"
)

func TestReferenceBinaryBasicPreparedFrames(t *testing.T) {
	p := newReferencePair(t)
	p.step(t, referenceExec("CREATE TABLE t(v BYTEA,s BYTEA)", 0))
	p.step(t, referenceExec(`INSERT INTO t VALUES('\x00ff8041008042ff00','\xff8000')`, 1))
	q := "SELECT v||$1 AS cat,btrim(v,$1) AS bt,ltrim(v,$1) AS lt,rtrim(v,$1) AS rt FROM t"
	v := "\x00\xff\x80A\x00\x80B\xff\x00"
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			stmt, err := backend.db.PrepareContext(p.ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			executor := referenceHeldStatement{referenceExecutor: backend.db, statement: stmt, query: q}
			for _, tc := range []struct {
				arg  any
				want []any
			}{
				{[]byte{255, 128, 0}, []any{"\x00\xff\x80A\x00\x80B\xff\x00\xff\x80\x00", "A\x00\x80B", "A\x00\x80B\xff\x00", "\x00\xff\x80A\x00\x80B"}},
				{[]byte{}, []any{v, v, v, v}}, {nil, []any{nil, nil, nil, nil}},
				{`\x41`, []any{"\x00\xff\x80A\x00\x80B\xff\x00\\x41", v, v, v}},
				{[]byte{255, 128, 0}, []any{"\x00\xff\x80A\x00\x80B\xff\x00\xff\x80\x00", "A\x00\x80B", "A\x00\x80B\xff\x00", "\x00\xff\x80A\x00\x80B"}},
			} {
				referenceRunStep(t, p.ctx, executor, backend.recorder, referenceSelect(q, []string{"cat", "bt", "lt", "rt"}, []string{"BYTEA", "BYTEA", "BYTEA", "BYTEA"}, [][]any{tc.want}, tc.arg))
			}
			referenceRunStep(t, p.ctx, backend.db, backend.recorder, referencePrepared(referenceSelect("SELECT $1||v AS cat,btrim($1,s) AS bt FROM t", []string{"cat", "bt"}, []string{"BYTEA", "BYTEA"}, [][]any{{"\x00A\xff\x00\xff\x80A\x00\x80B\xff\x00", "A"}}, []byte{0, 65, 255})))
		})
	}
}

func TestReferenceBinaryBasicConsumersAndNativeControls(t *testing.T) {
	p := newReferencePair(t)
	p.step(t, referenceExec("CREATE TABLE t(v BYTEA,s BYTEA)", 0))
	p.step(t, referenceExec(`INSERT INTO t VALUES('\x00ff8041008042ff00','\xff8000')`, 1))
	// The selected provider composes with existing master identity casts;
	// this part adds no cast admission or conversion edges.
	cast := referenceSelect("SELECT v::bytea AS v,length(v::bytea) AS n FROM t", []string{"v", "n"}, []string{"BYTEA", "INT4"}, [][]any{{"\x00\xff\x80A\x00\x80B\xff\x00", int64(9)}})
	p.step(t, cast)
	p.step(t, referencePrepared(cast))
	p.step(t, referenceExec("CREATE TABLE dst(v BYTEA)", 0))
	p.step(t, referenceExec("INSERT INTO dst SELECT v||s FROM t WHERE length(v)=9", 1))
	cat := []byte{0, 255, 128, 65, 0, 128, 66, 255, 0, 255, 128, 0}
	p.step(t, referenceSelect("SELECT v FROM dst", []string{"v"}, []string{"BYTEA"}, [][]any{{"\x00\xff\x80A\x00\x80B\xff\x00\xff\x80\x00"}}))
	p.step(t, referenceSelect("SELECT length(v) AS n,count(*) AS c FROM t GROUP BY length(v)", []string{"n", "c"}, []string{"INT4", "INT8"}, [][]any{{int64(9), int64(1)}}))
	grouped := referenceSelect("SELECT length(v) AS n,count(*) AS c FROM t GROUP BY 1", []string{"n", "c"}, []string{"INT4", "INT8"}, [][]any{{int64(9), int64(1)}})
	p.step(t, grouped)
	p.step(t, referencePrepared(grouped))
	p.step(t, referencePrepared(referenceSelect("UPDATE dst SET v=btrim(v,$1) RETURNING v,length(v) AS n", []string{"v", "n"}, []string{"BYTEA", "INT4"}, [][]any{{"A\x00\x80B", int64(4)}}, []byte{0, 128, 255})))
	bad := referenceImplicitError(`INSERT INTO dst SELECT v||'\q' FROM t`, referenceDiagnostic{Code: "22P02", Message: "invalid input syntax for type bytea", Position: "27"})
	bad.errorStage = "execute"
	p.step(t, bad)
	bad.errorStage = "prepare"
	p.step(t, referencePrepared(bad))
	p.step(t, referenceSelect("SELECT v FROM dst", []string{"v"}, []string{"BYTEA"}, [][]any{{"A\x00\x80B"}}))
	// Native replacement is exercised only through the public native pool.
	for _, q := range []string{"CREATE VIEW binary_view AS SELECT v||s AS cat,bit_length(v) AS bits,octet_length(v) AS octets,length(v) AS len,btrim(v,s) AS bt,ltrim(v,s) AS lt,rtrim(v,s) AS rt FROM t", `CREATE VIEW binary_literal_view AS SELECT btrim(v,'\x0080ff') AS bt,v||'\x00' AS cat FROM t`} {
		if _, err := p.native.ExecContext(p.ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	held, err := p.native.QueryContext(p.ctx, "SELECT * FROM binary_view")
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.ctx, p.native, &dump); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Reset(p.ctx, p.native); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(p.ctx, p.native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	check := func(q string, want []any) {
		t.Helper()
		rows, err := p.native.QueryContext(p.ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		if !rows.Next() {
			t.Fatal(rows.Err())
		}
		got := make([]any, len(want))
		dest := make([]any, len(want))
		for i := range got {
			dest[i] = &got[i]
		}
		if err := rows.Scan(dest...); err != nil || !reflect.DeepEqual(got, want) {
			t.Fatal(got, want, err)
		}
		if rows.Next() || rows.Err() != nil {
			t.Fatal(rows.Err())
		}
	}
	expected := []any{cat, int64(72), int64(9), int64(9), []byte{65, 0, 128, 66}, []byte{65, 0, 128, 66, 255, 0}, []byte{0, 255, 128, 65, 0, 128, 66}}
	check("SELECT * FROM binary_view", expected)
	check("SELECT * FROM binary_literal_view", []any{[]byte{65, 0, 128, 66}, []byte{0, 255, 128, 65, 0, 128, 66, 255, 0, 0}})
	check("SELECT v FROM dst", []any{[]byte{65, 0, 128, 66}})
	if !held.Next() {
		t.Fatal(held.Err())
	}
	got := make([]any, 7)
	dest := make([]any, 7)
	for i := range got {
		dest[i] = &got[i]
	}
	if err := held.Scan(dest...); err != nil || !reflect.DeepEqual(got, expected) {
		t.Fatal(got, err)
	}
	got[0].([]byte)[0] = 99
	if held.Next() || held.Err() != nil {
		t.Fatal(held.Err())
	}
	held.Close()
	check("SELECT * FROM binary_view", expected)
	canceled, cancel := context.WithCancel(p.ctx)
	cancel()
	stmt, err := p.native.PrepareContext(p.ctx, "INSERT INTO dst SELECT v||s FROM t")
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	if _, err := stmt.ExecContext(canceled); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	check("SELECT v FROM dst", []any{[]byte{65, 0, 128, 66}})
}

func TestReferenceBinaryBasicRawMetadataAndOwnership(t *testing.T) {
	p := newReferencePair(t)
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
	p := newReferencePair(t)
	p.step(t, referenceExec("CREATE TABLE dst(v BYTEA)", 0))
	p.step(t, referenceExec(`INSERT INTO dst VALUES('\x41')`, 1))
	s := referenceImplicitError(`INSERT INTO dst VALUES($1),(btrim($1,'\q'))`, referenceDiagnostic{Code: "22P02", Message: "invalid input syntax for type bytea", Position: "38"}, []byte{0, 255})
	s.errorStage = "execute"
	p.step(t, s)
	s.errorStage = "prepare"
	p.step(t, referencePrepared(s))
	p.step(t, referenceSelect("SELECT v FROM dst", []string{"v"}, []string{"BYTEA"}, [][]any{{"A"}}))
}

func TestReferenceBinaryBasicEncodingLengthSignatures(t *testing.T) {
	p := newReferencePair(t)
	p.step(t, referenceExec("CREATE TABLE enc(v BYTEA,t TEXT,vc VARCHAR(4),bp CHAR(4),n NAME)", 0))
	p.step(t, referenceExec(`INSERT INTO enc VALUES('\xc3a9','UTF8','UTF8','UTF8','UTF8')`, 1))
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
					s := referenceSelect(tc.q, []string{"n"}, []string{"INT4"}, [][]any{{tc.want}})
					referenceRunStep(t, p.ctx, backend.db, backend.recorder, s)
					referenceRunStep(t, p.ctx, backend.db, backend.recorder, referencePrepared(s))
				} else {
					s := referenceImplicitError(tc.q, referenceDiagnostic{Code: "0A000", Message: "encoding-aware length is unsupported", Position: "8"})
					s.selectRows = true
					s.errorStage = "execute"
					referenceRunStep(t, p.ctx, backend.db, backend.recorder, s)
					s.errorStage = "prepare"
					referenceRunStep(t, p.ctx, backend.db, backend.recorder, referencePrepared(s))
				}
			}
		})
	}
	s := referenceImplicitError("SELECT length(v,1) FROM enc", referenceDiagnostic{Code: "42883", Message: "function length(bytea, integer) does not exist", Position: "8", Hint: "No function matches the given name and argument types. You might need to add explicit type casts."})
	s.selectRows = true
	s.errorStage = "execute"
	p.step(t, s)
	s.errorStage = "prepare"
	p.step(t, referencePrepared(s))
}
