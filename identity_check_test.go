package stuntgres_test

import (
	"bytes"
	"context"
	"stuntgres"
	"testing"
)

func TestIdentityOwnCheckControls(t *testing.T) {
	for _, scenario := range []struct{ name, create, table, sequence string }{
		{"automatic", `CREATE TABLE i(id INT GENERATED ALWAYS AS IDENTITY,CONSTRAINT c CHECK(nextval('i_id_seq')>0))`, "i", "i_id_seq"},
		{"explicit", `CREATE TABLE i(id INT GENERATED ALWAYS AS IDENTITY(SEQUENCE NAME owncheck),CONSTRAINT c CHECK(nextval('owncheck')>0))`, "i", "owncheck"},
		{"schema", `CREATE SCHEMA portable CREATE TABLE i(id INT GENERATED ALWAYS AS IDENTITY,CONSTRAINT c CHECK(nextval('portable.i_id_seq')>0))`, "portable.i", "portable.i_id_seq"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			db := openLocal(t, stuntgres.NewDriver(), t.Name())
			db.SetMaxOpenConns(1)
			execSQL(t, db, scenario.create)
			state := `SELECT last_value,is_called FROM ` + scenario.sequence
			wantTuples(t, db, state, [][]any{{int64(1), false}})
			var empty bytes.Buffer
			if err := stuntgres.Dump(context.Background(), db, &empty); err != nil {
				t.Fatal(err)
			}
			wantTuples(t, db, state, [][]any{{int64(1), false}})
			execSQL(t, db, `INSERT INTO `+scenario.table+` DEFAULT VALUES`)
			wantTuples(t, db, `SELECT id FROM `+scenario.table, [][]any{{int64(1)}})
			wantTuples(t, db, state, [][]any{{int64(2), true}})
			held, err := db.Prepare(`INSERT INTO ` + scenario.table + ` DEFAULT VALUES`)
			if err != nil {
				t.Fatal(err)
			}
			defer held.Close()
			var dump bytes.Buffer
			if err := stuntgres.Dump(context.Background(), db, &dump); err != nil {
				t.Fatal(err)
			}
			wantTuples(t, db, state, [][]any{{int64(2), true}})
			if err := stuntgres.Restore(context.Background(), db, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
			var again bytes.Buffer
			if err := stuntgres.Dump(context.Background(), db, &again); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(dump.Bytes(), again.Bytes()) {
				t.Fatal("own CHECK roundtrip changed", dump.String(), again.String())
			}
			wantTuples(t, db, state, [][]any{{int64(2), true}})
			if _, err := held.Exec(); err != nil {
				t.Fatal(err)
			}
			wantTuples(t, db, `SELECT id FROM `+scenario.table+` ORDER BY id`, [][]any{{int64(1)}, {int64(3)}})
			wantTuples(t, db, state, [][]any{{int64(4), true}})
			execSQL(t, db, `DROP TABLE `+scenario.table)
			rows, err := db.Query(`SELECT last_value FROM ` + scenario.sequence)
			if rows != nil {
				rows.Close()
			}
			identityState(t, err, "42P01")
		})
	}
}
