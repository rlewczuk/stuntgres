package reference_test

import (
	"bytes"
	"database/sql"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func copyBinaryControlsWire() []byte {
	return copyBinaryStream(copyBinaryRow(copyBinary32(42), []byte("hi"), []byte{1}), copyBinaryRow(copyBinary32(99), []byte("sentinel"), []byte{1}), copyBinaryRow(nil, []byte{}, []byte{0}))
}
func copyBinaryControlsRows() [][]any {
	return [][]any{{int64(42), "hi", true}, {int64(99), "sentinel", true}, {nil, "", false}}
}

func TestReferenceCopyBinaryControls(t *testing.T) {
	copyTextScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c copyTextConnection) {
		ddl := "CREATE TABLE copy_binary_controls(i int4,t text,b bool)"
		c.exec(t, ddl)
		c.exec(t, "INSERT INTO copy_binary_controls VALUES(99,'sentinel',true)")
		retained, err := b.DB.QueryContext(c.ctx, "SELECT * FROM copy_binary_controls ORDER BY i NULLS LAST")
		if err != nil {
			t.Fatal(err)
		}
		defer retained.Close()
		input := copyBinaryStream(copyBinaryRow(copyBinary32(42), []byte("hi"), []byte{1}), copyBinaryRow(nil, []byte{}, []byte{0}))
		result, err := c.from("COPY copy_binary_controls FROM STDIN WITH(FORMAT binary)", bytes.NewReader(input))
		copyTextSuccess(t, result, err, 2)
		names, types := []string{"i", "t", "b"}, []string{"INT4", "TEXT", "BOOL"}
		c.selectRows(t, "SELECT * FROM copy_binary_controls ORDER BY i NULLS LAST", names, types, copyBinaryControlsRows())
		c.output(t, "COPY (SELECT * FROM copy_binary_controls ORDER BY i NULLS LAST) TO STDOUT WITH(FORMAT binary)", string(copyBinaryControlsWire()), 3)
		native := c
		if b.PG {
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			conn, err := p.Native.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			native = copyTextConnection{conn: conn, ctx: p.Ctx}
			native.exec(t, ddl)
			native.exec(t, "INSERT INTO copy_binary_controls VALUES($1,$2,$3)", 99, "sentinel", true)
			result, err := native.from("COPY copy_binary_controls FROM STDIN WITH(FORMAT binary)", bytes.NewReader(input))
			copyTextSuccess(t, result, err, 2)
		}
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		native.selectRows(t, "SELECT * FROM copy_binary_controls ORDER BY i NULLS LAST", names, types, copyBinaryControlsRows())
		native.output(t, "COPY (SELECT * FROM copy_binary_controls ORDER BY i NULLS LAST) TO STDOUT WITH(FORMAT binary)", string(copyBinaryControlsWire()), 3)
		freshDriver := stuntgres.NewDriver()
		connector, err := freshDriver.OpenConnector("reference/binary-restored/" + t.Name())
		if err != nil {
			t.Fatal(err)
		}
		freshDB := sql.OpenDB(connector)
		defer freshDB.Close()
		if err := stuntgres.Restore(p.Ctx, freshDB, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		freshConn, err := freshDB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer freshConn.Close()
		fresh := copyTextConnection{conn: freshConn, ctx: p.Ctx}
		fresh.selectRows(t, "SELECT * FROM copy_binary_controls ORDER BY i NULLS LAST", names, types, copyBinaryControlsRows())
		fresh.output(t, "COPY (SELECT * FROM copy_binary_controls ORDER BY i NULLS LAST) TO STDOUT WITH(FORMAT binary)", string(copyBinaryControlsWire()), 3)
		if b.PG {
			c.exec(t, "DROP TABLE copy_binary_controls")
			// Replay the actual dump, never handwritten equivalent SQL.
			c.exec(t, dump.String())
		}
		c.selectRows(t, "SELECT * FROM copy_binary_controls ORDER BY i NULLS LAST", names, types, copyBinaryControlsRows())
		c.output(t, "COPY (SELECT * FROM copy_binary_controls ORDER BY i NULLS LAST) TO STDOUT WITH(FORMAT binary)", string(copyBinaryControlsWire()), 3)
		copyBinaryConsumerRows(t, retained, [][]any{{int64(99), "sentinel", true}})
	})
}

func TestReferenceCopyBinaryFeatureSmoke(t *testing.T) {
	copyTextScenario(t, func(t *testing.T, _ *fixture.Pair, _ fixture.Backend, c copyTextConnection) {
		c.exec(t, "CREATE TABLE copy_binary_smoke(i int4,t text,b bool)")
		prepared, err := c.conn.PrepareContext(c.ctx, "SELECT * FROM copy_binary_smoke ORDER BY i NULLS LAST")
		if err != nil {
			t.Fatal(err)
		}
		defer prepared.Close()
		input := copyBinaryStream(copyBinaryRow(copyBinary32(42), []byte("hi"), []byte{1}), copyBinaryRow(nil, []byte{}, []byte{0}))
		result, err := c.from("COPY copy_binary_smoke FROM STDIN WITH(FORMAT binary)", bytes.NewReader(input))
		copyTextSuccess(t, result, err, 2)
		c.selectRows(t, "SELECT * FROM copy_binary_smoke ORDER BY i NULLS LAST", []string{"i", "t", "b"}, []string{"INT4", "TEXT", "BOOL"}, [][]any{{int64(42), "hi", true}, {nil, "", false}})
		rows, err := prepared.QueryContext(c.ctx)
		if err != nil {
			t.Fatal(err)
		}
		// Nullable metadata/rows are explicitly asserted without TEXT casts.
		columns, err := rows.ColumnTypes()
		if err != nil {
			rows.Close()
			t.Fatal(err)
		}
		for i, typ := range []string{"INT4", "TEXT", "BOOL"} {
			if columns[i].DatabaseTypeName() != typ {
				rows.Close()
				t.Fatalf("prepared smoke type=%s want=%s", columns[i].DatabaseTypeName(), typ)
			}
		}
		expected := []struct {
			i       sql.NullInt64
			text    string
			enabled bool
		}{{sql.NullInt64{Int64: 42, Valid: true}, "hi", true}, {sql.NullInt64{}, "", false}}
		for _, want := range expected {
			if !rows.Next() {
				rows.Close()
				t.Fatal("missing prepared smoke row", rows.Err())
			}
			var i sql.NullInt64
			var text string
			var enabled bool
			if err := rows.Scan(&i, &text, &enabled); err != nil {
				rows.Close()
				t.Fatal(err)
			}
			if i != want.i || text != want.text || enabled != want.enabled {
				rows.Close()
				t.Fatal("prepared smoke values", i, text, enabled)
			}
		}
		if rows.Next() || rows.Err() != nil {
			rows.Close()
			t.Fatal("prepared smoke row end", rows.Err())
		}
		rows.Close()
		c.output(t, "COPY (SELECT * FROM copy_binary_smoke ORDER BY i NULLS LAST) TO STDOUT WITH(FORMAT binary)", string(input), 2)
		c.exec(t, "INSERT INTO copy_binary_smoke VALUES(99,'sentinel',true)")
		// Existing pinned framing contract, with independently named relation.
		bad := copyBinaryStream(copyBinaryRow(copyBinary32(1), []byte("staged"), []byte{1}), copyBinary16(2))
		result, err = c.from("COPY copy_binary_smoke FROM STDIN WITH(FORMAT binary)", bytes.NewReader(bad))
		copyTextDiagnostic(t, result, err, fixture.Diagnostic{Code: "22P04", Message: "row field count is 2, expected 3", Context: "COPY copy_binary_smoke, line 2"})
		c.selectRows(t, "SELECT * FROM copy_binary_smoke ORDER BY i NULLS LAST", []string{"i", "t", "b"}, []string{"INT4", "TEXT", "BOOL"}, copyBinaryControlsRows())
		c.output(t, "COPY (SELECT * FROM copy_binary_smoke ORDER BY i NULLS LAST) TO STDOUT WITH(FORMAT binary)", string(copyBinaryControlsWire()), 3)
	})
}
