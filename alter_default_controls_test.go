package stuntgres_test

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"stuntgres"
	"testing"
)

func TestAlterDefaultControls(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, populated := range []bool{false, true} {
			for _, drop := range []bool{false, true} {
				t.Run(fmt.Sprintf("prepared=%v/populated=%v/drop=%v", prepared, populated, drop), func(t *testing.T) {
					db := openLocal(t, stuntgres.NewDriver(), t.Name())
					ctx := context.Background()
					exec := func(q string, n int64) { tableCoreExec(t, db, prepared, q, n) }
					exec("CREATE TABLE d(id INT,i INT DEFAULT 11,b BOOL DEFAULT FALSE)", 0)
					var want [][]any
					if populated {
						exec("INSERT INTO d(id) VALUES(0)", 1)
						want = append(want, []any{int64(0), int64(11), false})
					}
					exec("ALTER TABLE d ALTER i SET DEFAULT 7", 0)
					exec("ALTER TABLE d ALTER b SET DEFAULT TRUE OR FALSE", 0)
					var future any = int64(7)
					if drop {
						exec("ALTER TABLE d ALTER i DROP DEFAULT", 0)
						future = nil
					}
					held, err := db.Query("SELECT * FROM d ORDER BY id")
					if err != nil {
						t.Fatal(err)
					}
					var dump bytes.Buffer
					if err := stuntgres.Dump(ctx, db, &dump); err != nil {
						t.Fatal(err)
					}
					for _, script := range []string{"CREATE TABLE bad(i INT DEFAULT 'oops')", "CREATE TABLE partial(i INT);ALTER TABLE d ALTER i SET DEFAULT 99"} {
						if err := stuntgres.Restore(ctx, db, strings.NewReader(script)); err == nil {
							t.Fatal("invalid Restore accepted")
						}
						var same bytes.Buffer
						if err := stuntgres.Dump(ctx, db, &same); err != nil || !bytes.Equal(same.Bytes(), dump.Bytes()) {
							t.Fatal("failed Restore changed catalog", err)
						}
					}
					if err := stuntgres.Reset(ctx, db); err != nil {
						t.Fatal(err)
					}
					if err := stuntgres.Restore(ctx, db, bytes.NewReader(dump.Bytes())); err != nil {
						t.Fatal(err)
					}
					timestampCheckRows(t, held, []string{"id", "i", "b"}, []string{"INT4", "INT4", "BOOL"}, want)
					held.Close()
					var again bytes.Buffer
					if err := stuntgres.Dump(ctx, db, &again); err != nil || !bytes.Equal(again.Bytes(), dump.Bytes()) {
						t.Fatal("redump", err)
					}
					defaultConsumerRows(t, db, prepared, []string{"id", "i", "b"}, []string{"INT4", "INT4", "BOOL"}, want)
					exec("INSERT INTO d(id) VALUES(1)", 1)
					exec("INSERT INTO d VALUES(2,DEFAULT,DEFAULT)", 1)
					exec("CREATE TABLE source(id INT)", 0)
					exec("INSERT INTO source VALUES(3)", 1)
					exec("INSERT INTO d(id) SELECT id FROM source", 1)
					for _, id := range []int64{1, 2, 3} {
						want = append(want, []any{id, future, true})
					}
					defaultConsumerRows(t, db, prepared, []string{"id", "i", "b"}, []string{"INT4", "INT4", "BOOL"}, want)
				})
			}
		}
	}
}

func TestAlterDefaultBooleanGrammar(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	execSQL(t, db, "CREATE TABLE d(id INT,b BOOL DEFAULT FALSE)")
	execSQL(t, db, "INSERT INTO d(id) VALUES(0)")
	for i, expr := range []string{"TRUE OR FALSE", "NOT FALSE", "NULL IS NULL"} {
		tableCoreExec(t, db, false, "ALTER TABLE d ALTER b SET DEFAULT "+expr, 0)
		tableCoreExec(t, db, false, fmt.Sprintf("INSERT INTO d(id) VALUES(%d)", i+1), 1)
	}
	defaultConsumerRows(t, db, false, []string{"id", "b"}, []string{"INT4", "BOOL"}, [][]any{{int64(0), false}, {int64(1), true}, {int64(2), true}, {int64(3), true}})
}
