package smoke_test

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"stuntgres"
	"testing"
)

func TestAlterDefaultSmoke(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			connector, err := stuntgres.NewDriver().OpenConnector(t.Name())
			if err != nil {
				t.Fatal(err)
			}
			db := sql.OpenDB(connector)
			defer db.Close()
			exec := func(q string, n int64) {
				t.Helper()
				var r sql.Result
				var err error
				if prepared {
					s, e := db.Prepare(q)
					if e != nil {
						t.Fatal(e)
					}
					r, err = s.Exec()
					s.Close()
				} else {
					r, err = db.Exec(q)
				}
				if err != nil {
					t.Fatal(q, err)
				}
				if count, e := r.RowsAffected(); e != nil || count != n {
					t.Fatal(q, count, e)
				}
			}
			check := func(want [][]any) {
				t.Helper()
				rows, err := db.Query("SELECT * FROM d ORDER BY id")
				if err != nil {
					t.Fatal(err)
				}
				alterDropSmokeRows(t, rows, []string{"id", "i", "b"}, []string{"INT4", "INT4", "BOOL"}, want)
			}
			exec("CREATE TABLE d(id INT,i INT DEFAULT 11,b BOOL DEFAULT FALSE)", 0)
			exec("INSERT INTO d(id) VALUES(0)", 1)
			held, err := db.Prepare("INSERT INTO d(id) VALUES($1)")
			if err != nil {
				t.Fatal(err)
			}
			defer held.Close()
			exec("ALTER TABLE d ALTER i SET DEFAULT 7", 0)
			r, err := held.Exec(1)
			if err != nil {
				t.Fatal(err)
			}
			if n, e := r.RowsAffected(); e != nil || n != 1 {
				t.Fatal(n, e)
			}
			exec("INSERT INTO d VALUES(2,DEFAULT,DEFAULT)", 1)
			q := "ALTER TABLE d ALTER i SET DEFAULT TRUE"
			if prepared {
				s, e := db.Prepare(q)
				if e != nil {
					t.Fatal(e)
				}
				_, err = s.Exec()
				s.Close()
			} else {
				_, err = db.Exec(q)
			}
			if e := typeCoreDiagnostic(err, stuntgres.Error{Code: "42804", Message: `column "i" is of type integer but default expression is of type boolean`, Hint: "You will need to rewrite or cast the expression."}); e != nil {
				t.Fatal(e)
			}
			exec("INSERT INTO d(id) VALUES(3)", 1)
			exec("ALTER TABLE d ALTER b SET DEFAULT TRUE OR FALSE", 0)
			exec("INSERT INTO d(id) VALUES(4)", 1)
			want := [][]any{{int64(0), int64(11), false}, {int64(1), int64(7), false}, {int64(2), int64(7), false}, {int64(3), int64(7), false}, {int64(4), int64(7), true}}
			check(want)
			for _, drop := range []bool{false, true} {
				if drop {
					exec("ALTER TABLE d ALTER i DROP DEFAULT", 0)
					exec("ALTER TABLE d ALTER i DROP DEFAULT", 0)
				}
				var dump bytes.Buffer
				ctx := context.Background()
				if err := stuntgres.Dump(ctx, db, &dump); err != nil {
					t.Fatal(err)
				}
				if err := stuntgres.Reset(ctx, db); err != nil {
					t.Fatal(err)
				}
				if err := stuntgres.Restore(ctx, db, bytes.NewReader(dump.Bytes())); err != nil {
					t.Fatal(err)
				}
				var again bytes.Buffer
				if err := stuntgres.Dump(ctx, db, &again); err != nil || !bytes.Equal(again.Bytes(), dump.Bytes()) {
					t.Fatal("redump", err)
				}
				check(want)
				id := 5
				if drop {
					id = 7
				}
				exec(fmt.Sprintf("INSERT INTO d(id) VALUES(%d)", id), 1)
				exec(fmt.Sprintf("INSERT INTO d VALUES(%d,DEFAULT,DEFAULT)", id+1), 1)
				var future any = int64(7)
				if drop {
					future = nil
				}
				want = append(want, []any{int64(id), future, true}, []any{int64(id + 1), future, true})
				check(want)
			}
		})
	}
}
