package reference_test

import (
	"bytes"
	"database/sql"
	"reflect"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceCopyTextControls(t *testing.T) {
	copyTextScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c copyTextConnection) {
		c.exec(t, "CREATE TABLE copy_controls(id int,v text DEFAULT 'fallback',g int GENERATED ALWAYS AS(id+1) STORED)")
		c.exec(t, "INSERT INTO copy_controls(id,v) VALUES(99,'sentinel')")
		retained, err := b.DB.QueryContext(c.ctx, "SELECT * FROM copy_controls ORDER BY id")
		if err != nil {
			t.Fatal(err)
		}
		defer retained.Close()
		result, err := c.from("COPY copy_controls FROM STDIN WITH(DEFAULT 'DEF')", strings.NewReader("1\tDEF\n2\t\\N\n"))
		copyTextSuccess(t, result, err, 2)
		want := [][]any{{int64(1), "fallback", int64(2)}, {int64(2), nil, int64(3)}, {int64(99), "sentinel", int64(100)}}
		c.selectRows(t, "SELECT * FROM copy_controls ORDER BY id", []string{"id", "v", "g"}, []string{"INT4", "TEXT", "INT4"}, want)
		native := c
		if b.PG {
			if err = stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			n, err := p.Native.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer n.Close()
			native = copyTextConnection{conn: n, ctx: p.Ctx}
			native.exec(t, "CREATE TABLE copy_controls(id int,v text DEFAULT 'fallback',g int GENERATED ALWAYS AS(id+1) STORED)")
			native.exec(t, "INSERT INTO copy_controls(id,v) VALUES(99,'sentinel')")
			result, err = native.from("COPY copy_controls FROM STDIN WITH(DEFAULT 'DEF')", strings.NewReader("1\tDEF\n2\t\\N\n"))
			copyTextSuccess(t, result, err, 2)
		}
		var dump bytes.Buffer
		if err = stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
			t.Fatal(err)
		}
		if err = stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		if err = stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		if b.PG {
			c.exec(t, "DROP TABLE copy_controls")
			c.exec(t, dump.String())
		}
		c.selectRows(t, "SELECT * FROM copy_controls ORDER BY id", []string{"id", "v", "g"}, []string{"INT4", "TEXT", "INT4"}, want)
		var got [][]any
		for retained.Next() {
			var id, g int64
			var v sql.NullString
			if err = retained.Scan(&id, &v, &g); err != nil {
				t.Fatal(err)
			}
			var text any
			if v.Valid {
				text = v.String
			}
			got = append(got, []any{id, text, g})
		}
		if err = retained.Err(); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, [][]any{{int64(99), "sentinel", int64(100)}}) {
			t.Fatalf("retained pre-COPY rows=%#v", got)
		}
	})
}
