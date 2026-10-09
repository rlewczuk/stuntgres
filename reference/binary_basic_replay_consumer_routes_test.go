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

func TestReferenceBinaryBasicReplayConsumerRoutes(t *testing.T) {
	for route, nonContext := range []bool{false, true} {
		t.Run([]string{"context", "consumer"}[route], func(t *testing.T) {
			p := newReferenceScenario(t)
			apply := func(s referenceStep) { s.nonContext = nonContext; p.step(t, s) }
			for _, s := range []referenceStep{
				referenceExec("CREATE TABLE t(id INTEGER,v BYTEA,s BYTEA)", 0),
				referenceExec(`INSERT INTO t VALUES(1,'\x00ff8041008042ff00','\xff8000')`, 1),
				referenceExec("CREATE TABLE dst(v BYTEA)", 0),
				referenceExec("INSERT INTO dst SELECT v||s FROM t", 1),
				referenceExec("CREATE TABLE dst_updated(v BYTEA)", 0),
				referenceExec(`INSERT INTO dst_updated VALUES('\x41008042')`, 1),
			} {
				apply(s)
			}
			cat := []byte{0, 255, 128, 65, 0, 128, 66, 255, 0, 255, 128, 0}
			for _, q := range []string{"CREATE VIEW binary_view AS SELECT v||s AS cat,bit_length(v) AS bits,octet_length(v) AS octets,length(v) AS len,btrim(v,s) AS bt,ltrim(v,s) AS lt,rtrim(v,s) AS rt FROM t", `CREATE VIEW binary_literal_view AS SELECT btrim(v,'\x0080ff') AS bt,v||'\x00' AS cat FROM t`} {
				apply(referenceExec(q, 0))
			}
			common := referenceSelect("SELECT * FROM binary_view", []string{"cat", "bits", "octets", "len", "bt", "lt", "rt"}, []string{"BYTEA", "INT4", "INT4", "INT4", "BYTEA", "BYTEA", "BYTEA"}, [][]any{{"\x00\xff\x80A\x00\x80B\xff\x00\xff\x80\x00", int64(72), int64(9), int64(9), "A\x00\x80B", "A\x00\x80B\xff\x00", "\x00\xff\x80A\x00\x80B"}})
			apply(common)
			var held *sql.Rows
			var err error
			if nonContext {
				held, err = p.native.Query("SELECT * FROM binary_view")
			} else {
				held, err = p.native.QueryContext(p.ctx, "SELECT * FROM binary_view")
			}
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
				var rows *sql.Rows
				var err error
				if nonContext {
					rows, err = p.native.Query(q)
				} else {
					rows, err = p.native.QueryContext(p.ctx, q)
				}
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
			check("SELECT v FROM dst", []any{cat})
			check("SELECT v FROM dst_updated", []any{[]byte{65, 0, 128, 66}})
			metadata, err := held.ColumnTypes()
			if err != nil || len(metadata) != 7 {
				t.Fatal(metadata, err)
			}
			for i, typ := range metadata {
				name := "BYTEA"
				if i >= 1 && i <= 3 {
					name = "INT4"
				}
				if typ.DatabaseTypeName() != name {
					t.Fatal(i, typ)
				}
			}
			check("SELECT v,s FROM t", []any{[]byte{0, 255, 128, 65, 0, 128, 66, 255, 0}, []byte{255, 128, 0}})
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
			if nonContext {
				_, err = p.native.Exec(`INSERT INTO dst SELECT v||'\q' FROM t`)
			} else {
				_, err = p.native.ExecContext(p.ctx, `INSERT INTO dst SELECT v||'\q' FROM t`)
			}
			want := controlsReferenceDiagnostic("22P02", "invalid input syntax for type bytea")
			want.Position = "27"
			referenceAssertDiagnostic(t, err, &want)
			check("SELECT v FROM dst", []any{cat})
			canceled, cancel := context.WithCancel(p.ctx)
			cancel()
			canceledRows, err := p.native.QueryContext(canceled, "SELECT btrim(v,s) FROM t")
			if canceledRows != nil {
				canceledRows.Close()
				t.Fatal("canceled query exposed cursor")
			}
			if !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			var stmt *sql.Stmt
			if nonContext {
				stmt, err = p.native.Prepare("INSERT INTO dst SELECT v||s FROM t")
			} else {
				stmt, err = p.native.PrepareContext(p.ctx, "INSERT INTO dst SELECT v||s FROM t")
			}
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			if _, err := stmt.ExecContext(canceled); !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			check("SELECT v FROM dst", []any{cat})
			check("SELECT v FROM dst_updated", []any{[]byte{65, 0, 128, 66}})
			apply(common)
		})
	}
}
