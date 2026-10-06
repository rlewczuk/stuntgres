package stuntgres_test

import (
	"context"
	"errors"
	"reflect"
	"stuntgres"
	"testing"
)

func TestByteaPublicInputCompareAndPrepare(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	execSQL(t, db, `CREATE TABLE t(id INTEGER,v "bytea")`)
	for _, prepared := range []bool{false, true} {
		for i, tc := range []struct {
			literal string
			want    []byte
		}{
			{`''`, []byte{}}, {`'\x'`, []byte{}}, {`'\x00aAfF'`, []byte{0, 170, 255}},
			{"'\\x 00\tAA\nff\r'", []byte{0, 170, 255}}, {`'\000\177\200\377'`, []byte{0, 127, 128, 255}},
			{`'\\'`, []byte{92}}, {`''''`, []byte{39}}, {`'é'`, []byte{195, 169}},
			{`$$\x0080ff$$`, []byte{0, 128, 255}}, {`E'\\x0080ff'`, []byte{0, 128, 255}},
			{`U&'\005Cx41'`, []byte{65}},
		} {
			q := "INSERT INTO t VALUES($1," + tc.literal + ")"
			var err error
			if prepared {
				s, e := db.Prepare(q)
				if e != nil {
					t.Fatal(e)
				}
				_, err = s.Exec(i)
				s.Close()
			} else {
				_, err = db.Exec(q, i)
			}
			if err != nil {
				t.Fatal(q, err)
			}
			rows, err := db.Query("SELECT v FROM t WHERE id=$1", i)
			if err != nil {
				t.Fatal(err)
			}
			for rows.Next() {
				var got any
				if err := rows.Scan(&got); err != nil || !reflect.DeepEqual(got, tc.want) {
					t.Fatal(got, tc.want, err)
				}
			}
			if err := rows.Err(); err != nil {
				t.Fatal(err)
			}
			rows.Close()
		}
	}
	execSQL(t, db, "CREATE TABLE ordered(v BYTEA)")
	execSQL(t, db, `INSERT INTO ordered VALUES('\xff'),('\x80'),('\x7f'),('\x0000'),('\x00'),(''),(NULL)`)
	wantTuples(t, db, "SELECT v FROM ordered ORDER BY v", [][]any{{[]byte{}}, {[]byte{0}}, {[]byte{0, 0}}, {[]byte{127}}, {[]byte{128}}, {[]byte{255}}, {nil}})
	wantTuples(t, db, "SELECT v FROM ordered ORDER BY v DESC", [][]any{{nil}, {[]byte{255}}, {[]byte{128}}, {[]byte{127}}, {[]byte{0, 0}}, {[]byte{0}}, {[]byte{}}})
	wantTuples(t, db, "SELECT v=v FROM ordered ORDER BY v", [][]any{{true}, {true}, {true}, {true}, {true}, {true}, {nil}})
	wantTuples(t, db, `SELECT v FROM ordered WHERE v='\x00'`, [][]any{{[]byte{0}}})
	wantTuples(t, db, "SELECT v FROM ordered WHERE v=NULL", [][]any{})
	query, err := db.Prepare("SELECT v FROM ordered WHERE v=$1")
	if err != nil {
		t.Fatal(err)
	}
	defer query.Close()
	for _, arg := range []any{[]byte{0}, []byte{255}, nil} {
		rows, err := query.Query(arg)
		if err != nil {
			t.Fatal(err)
		}
		n := 0
		for rows.Next() {
			var got any
			if err := rows.Scan(&got); err != nil || !reflect.DeepEqual(got, arg) {
				t.Fatal(got, arg, err)
			}
			n++
		}
		rows.Close()
		want := 1
		if arg == nil {
			want = 0
		}
		if n != want {
			t.Fatal(n, want)
		}
	}
	execSQL(t, db, "CREATE TABLE repeats(a BYTEA,b BYTEA)")
	execSQL(t, db, "INSERT INTO repeats VALUES($1,$1)", []byte{0, 255})
	wantTuples(t, db, "SELECT * FROM repeats", [][]any{{[]byte{0, 255}, []byte{0, 255}}})
	_, err = db.Exec(`CREATE TABLE bad(v "BYTEA")`)
	var se *stuntgres.Error
	if !errors.As(err, &se) || se.Code != "42704" {
		t.Fatal(err)
	}
}
func TestByteaSettingsAndAtomicity(t *testing.T) {
	ctx := context.Background()
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	exec := func(q string) {
		t.Helper()
		if _, err := conn.ExecContext(ctx, q); err != nil {
			t.Fatal(q, err)
		}
	}
	show := func(want string) {
		t.Helper()
		var got string
		if err := conn.QueryRowContext(ctx, "SHOW bytea_output").Scan(&got); err != nil || got != want {
			t.Fatal(got, want, err)
		}
	}
	show("hex")
	exec("SET bytea_output=ESCAPE")
	show("escape")
	exec("SET bytea_output=DEFAULT")
	show("hex")
	exec("SET bytea_output=escape")
	exec("RESET bytea_output")
	show("hex")
	exec("CREATE TABLE t(v BYTEA,n INTEGER NOT NULL)")
	exec(`INSERT INTO t VALUES('\x41',1)`)
	exec("BEGIN")
	exec("SET LOCAL bytea_output=escape")
	show("escape")
	_, err = conn.ExecContext(ctx, `INSERT INTO t VALUES('\x00',NULL)`)
	var se *stuntgres.Error
	if !errors.As(err, &se) || se.Code != "23502" || se.Detail != `Failing row contains (\000, null).` {
		t.Fatal(err)
	}
	exec("ROLLBACK")
	show("hex")
	exec("SET standard_conforming_strings=off")
	exec(`INSERT INTO t VALUES('\\x42',2)`)
	exec("SET standard_conforming_strings=on")
	for _, q := range []string{`INSERT INTO t VALUES('\x43',3),('\q',4)`, `INSERT INTO t VALUES('\x43',3),('\x44',NULL)`} {
		if _, err := conn.ExecContext(ctx, q); err == nil {
			t.Fatal("atomic failure accepted")
		}
	}
	rows, err := conn.QueryContext(ctx, "SELECT v FROM t ORDER BY n")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []byte{65, 66} {
		if !rows.Next() {
			t.Fatal("missing row")
		}
		var got []byte
		if err := rows.Scan(&got); err != nil || !reflect.DeepEqual(got, []byte{want}) {
			t.Fatal(got, err)
		}
	}
	if rows.Next() {
		t.Fatal("published prefix")
	}
	rows.Close()
}
