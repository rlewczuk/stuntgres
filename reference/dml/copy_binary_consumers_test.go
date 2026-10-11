package reference_test

import (
	"bytes"
	"database/sql"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceCopyBinaryQueryAndColumns(t *testing.T) {
	copyBinaryScenario(t, func(t *testing.T, c copyTextConnection) {
		c.exec(t, "CREATE TABLE copy_binary_columns(id int4 GENERATED ALWAYS AS IDENTITY,v text DEFAULT 'fallback',g int4 GENERATED ALWAYS AS(id+1) STORED)")
		result, err := c.from("COPY copy_binary_columns(v) FROM STDIN WITH(FORMAT binary)", bytes.NewReader(copyBinaryStream(copyBinaryRow([]byte{}), copyBinaryRow(nil))))
		copyTextSuccess(t, result, err, 2)
		result, err = c.from("COPY copy_binary_columns(v,id) FROM STDIN WITH(FORMAT binary)", bytes.NewReader(copyBinaryStream(copyBinaryRow([]byte("chosen"), copyBinary32(42)))))
		copyTextSuccess(t, result, err, 1)
		result, err = c.from("COPY copy_binary_columns(id) FROM STDIN WITH(FORMAT binary)", bytes.NewReader(copyBinaryStream(copyBinaryRow(copyBinary32(99)))))
		copyTextSuccess(t, result, err, 1)
		c.selectRows(t, "SELECT * FROM copy_binary_columns ORDER BY id", []string{"id", "v", "g"}, []string{"INT4", "TEXT", "INT4"}, [][]any{{int64(1), "", int64(2)}, {int64(2), nil, int64(3)}, {int64(42), "chosen", int64(43)}, {int64(99), "fallback", int64(100)}})
		c.selectRows(t, "SELECT v AS label,id AS number,g AS computed FROM copy_binary_columns ORDER BY id", []string{"label", "number", "computed"}, []string{"TEXT", "INT4", "INT4"}, [][]any{{"", int64(1), int64(2)}, {nil, int64(2), int64(3)}, {"chosen", int64(42), int64(43)}, {"fallback", int64(99), int64(100)}})
		want := copyBinaryStream(copyBinaryRow([]byte{}, copyBinary32(1), copyBinary32(2)), copyBinaryRow(nil, copyBinary32(2), copyBinary32(3)), copyBinaryRow([]byte("chosen"), copyBinary32(42), copyBinary32(43)), copyBinaryRow([]byte("fallback"), copyBinary32(99), copyBinary32(100)))
		c.output(t, "COPY (SELECT v AS label,id AS number,g AS computed FROM copy_binary_columns ORDER BY id) TO STDOUT WITH(FORMAT binary)", string(want), 4)
		c.output(t, "COPY copy_binary_columns(v,id) TO STDOUT WITH(FORMAT binary)", string(copyBinaryStream(copyBinaryRow([]byte{}, copyBinary32(1)), copyBinaryRow(nil, copyBinary32(2)), copyBinaryRow([]byte("chosen"), copyBinary32(42)), copyBinaryRow([]byte("fallback"), copyBinary32(99)))), 4)
		c.selectRows(t, "SELECT 42::int4 AS number,'hi'::text AS label,true AS enabled,NULL::int4 AS absent", []string{"number", "label", "enabled", "absent"}, []string{"INT4", "TEXT", "BOOL", "INT4"}, [][]any{{int64(42), "hi", true, nil}})
		c.output(t, "COPY (SELECT 42::int4 AS number,'hi'::text AS label,true AS enabled,NULL::int4 AS absent) TO STDOUT WITH(FORMAT binary)", string(copyBinaryStream(copyBinaryRow(copyBinary32(42), []byte("hi"), []byte{1}, nil))), 1)
		c.exec(t, "CREATE TABLE copy_binary_partition(i int4,v text) PARTITION BY RANGE(i)")
		c.exec(t, "CREATE TABLE copy_binary_low PARTITION OF copy_binary_partition FOR VALUES FROM(0) TO(10)")
		c.exec(t, "CREATE TABLE copy_binary_high PARTITION OF copy_binary_partition FOR VALUES FROM(10) TO(20)")
		result, err = c.from("COPY copy_binary_partition FROM STDIN WITH(FORMAT binary)", bytes.NewReader(copyBinaryStream(copyBinaryRow(copyBinary32(1), []byte("low")), copyBinaryRow(copyBinary32(11), []byte("high")))))
		copyTextSuccess(t, result, err, 2)
		c.selectRows(t, "SELECT * FROM copy_binary_low", []string{"i", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "low"}})
		c.selectRows(t, "SELECT * FROM copy_binary_high", []string{"i", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(11), "high"}})
		c.output(t, "COPY (SELECT * FROM copy_binary_partition ORDER BY i) TO STDOUT WITH(FORMAT binary)", string(copyBinaryStream(copyBinaryRow(copyBinary32(1), []byte("low")), copyBinaryRow(copyBinary32(11), []byte("high")))), 2)
	})
}

func TestReferenceCopyBinaryConsumers(t *testing.T) {
	copyTextScenario(t, func(t *testing.T, _ *fixture.Pair, b fixture.Backend, c copyTextConnection) {
		c.exec(t, "CREATE TABLE copy_binary_consumers(i int4,t text,b bool)")
		c.exec(t, "INSERT INTO copy_binary_consumers VALUES(99,'sentinel',true)")
		held, err := c.conn.PrepareContext(c.ctx, "SELECT * FROM copy_binary_consumers WHERE i=$1")
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		insert, err := c.conn.PrepareContext(c.ctx, "INSERT INTO copy_binary_consumers VALUES($1,$2,$3)")
		if err != nil {
			t.Fatal(err)
		}
		defer insert.Close()
		if _, err = insert.ExecContext(c.ctx, 7, "before", false); err != nil {
			t.Fatal(err)
		}
		before, err := held.QueryContext(c.ctx, 7)
		if err != nil {
			t.Fatal(err)
		}
		copyBinaryConsumerRows(t, before, [][]any{{int64(7), "before", false}})
		retained, err := b.DB.QueryContext(c.ctx, "SELECT * FROM copy_binary_consumers ORDER BY i")
		if err != nil {
			t.Fatal(err)
		}
		defer retained.Close()
		result, err := c.from("COPY copy_binary_consumers FROM STDIN WITH(FORMAT binary)", bytes.NewReader(copyBinaryStream(copyBinaryRow(copyBinary32(42), []byte("hi"), []byte{1}))))
		copyTextSuccess(t, result, err, 1)
		rows, err := held.QueryContext(c.ctx, 42)
		if err != nil {
			t.Fatal(err)
		}
		copyBinaryConsumerRows(t, rows, [][]any{{int64(42), "hi", true}})
		if _, err = insert.ExecContext(c.ctx, 8, "after", true); err != nil {
			t.Fatal(err)
		}
		c.selectRows(t, "SELECT * FROM copy_binary_consumers ORDER BY i", []string{"i", "t", "b"}, []string{"INT4", "TEXT", "BOOL"}, [][]any{{int64(7), "before", false}, {int64(8), "after", true}, {int64(42), "hi", true}, {int64(99), "sentinel", true}})
		copyBinaryConsumerRows(t, retained, [][]any{{int64(7), "before", false}, {int64(99), "sentinel", true}})
		c.output(t, "COPY (SELECT * FROM copy_binary_consumers ORDER BY i) TO STDOUT WITH(FORMAT binary)", string(copyBinaryStream(copyBinaryRow(copyBinary32(7), []byte("before"), []byte{0}), copyBinaryRow(copyBinary32(8), []byte("after"), []byte{1}), copyBinaryRow(copyBinary32(42), []byte("hi"), []byte{1}), copyBinaryRow(copyBinary32(99), []byte("sentinel"), []byte{1}))), 4)
	})
}

func copyBinaryConsumerRows(t *testing.T, rows *sql.Rows, want [][]any) {
	t.Helper()
	defer rows.Close()
	names, err := rows.Columns()
	if err != nil || !reflect.DeepEqual(names, []string{"i", "t", "b"}) {
		t.Fatal("consumer columns", names, err)
	}
	columns, err := rows.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	for i, typ := range []string{"INT4", "TEXT", "BOOL"} {
		if columns[i].DatabaseTypeName() != typ {
			t.Fatalf("consumer type %d=%s want=%s", i, columns[i].DatabaseTypeName(), typ)
		}
	}
	actual := make([][]any, 0)
	for rows.Next() {
		var i int64
		var text string
		var enabled bool
		if err := rows.Scan(&i, &text, &enabled); err != nil {
			t.Fatal(err)
		}
		actual = append(actual, []any{i, text, enabled})
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(actual, want) {
		t.Errorf("consumer rows=%#v want=%#v", actual, want)
	}
}
