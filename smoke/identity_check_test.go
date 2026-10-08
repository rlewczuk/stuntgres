package smoke_test

import (
	"bytes"
	"context"
	"stuntgres"
	"testing"
)

func TestIdentityCheckControlsSmoke(t *testing.T) {
	for _, scenario := range []struct{ name, create, table, sequence string }{
		{"automatic", `CREATE TABLE i(id INT GENERATED ALWAYS AS IDENTITY,CHECK(nextval('i_id_seq')>0))`, "i", "i_id_seq"},
		{"explicit", `CREATE TABLE i(id INT GENERATED ALWAYS AS IDENTITY(SEQUENCE NAME owncheck),CHECK(nextval('owncheck')>0))`, "i", "owncheck"},
		{"schema", `CREATE SCHEMA portable CREATE TABLE i(id INT GENERATED ALWAYS AS IDENTITY,CHECK(nextval('portable.i_id_seq')>0))`, "portable.i", "portable.i_id_seq"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			db := identitySmokeDB(t)
			db.SetMaxOpenConns(1)
			identitySmokeExec(t, db, scenario.create, 0)
			state := `SELECT last_value,is_called FROM ` + scenario.sequence
			identitySmokeRows(t, db, state, [][]any{{int64(1), false}})
			var empty bytes.Buffer
			if err := stuntgres.Dump(context.Background(), db, &empty); err != nil {
				t.Fatal(err)
			}
			identitySmokeRows(t, db, state, [][]any{{int64(1), false}})
			if err := stuntgres.Restore(context.Background(), db, bytes.NewReader(empty.Bytes())); err != nil {
				t.Fatal(err)
			}
			identitySmokeExec(t, db, `INSERT INTO `+scenario.table+` DEFAULT VALUES`, 1)
			identitySmokeRows(t, db, `SELECT id FROM `+scenario.table, [][]any{{int64(1)}})
			identitySmokeRows(t, db, state, [][]any{{int64(2), true}})
			identitySmokeRows(t, db, `SELECT currval('`+scenario.sequence+`'),lastval()`, [][]any{{int64(2), int64(2)}})
			held, err := db.Prepare(`INSERT INTO ` + scenario.table + ` DEFAULT VALUES`)
			if err != nil {
				t.Fatal(err)
			}
			defer held.Close()
			identitySmokeRows(t, db, state, [][]any{{int64(2), true}})
			var dump, again bytes.Buffer
			if err := stuntgres.Dump(context.Background(), db, &dump); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(context.Background(), db, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Dump(context.Background(), db, &again); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(context.Background(), db, bytes.NewReader(again.Bytes())); err != nil {
				t.Fatal(err)
			}
			if _, err := held.Exec(); err != nil {
				t.Fatal(err)
			}
			identitySmokeRows(t, db, `SELECT id FROM `+scenario.table+` ORDER BY id`, [][]any{{int64(1)}, {int64(3)}})
			identitySmokeRows(t, db, state, [][]any{{int64(4), true}})
			identitySmokeExec(t, db, `DROP TABLE `+scenario.table, 0)
			rows, missingErr := db.Query(`SELECT last_value FROM ` + scenario.sequence)
			if rows != nil {
				rows.Close()
			}
			identitySmokeState(t, missingErr, "42P01")
			_, err = db.Exec(`CREATE TABLE bad(id INT GENERATED ALWAYS AS IDENTITY,CHECK(nextval('bad_id_seq')>0),CHECK(missing>0))`)
			identitySmokeState(t, err, "42703")
			identitySmokeRows(t, db, `SELECT count(*) FROM pg_catalog.pg_class WHERE relname='bad' OR relname='bad_id_seq'`, [][]any{{int64(0)}})
			identitySmokeExec(t, db, `CREATE TABLE bad(id INT GENERATED ALWAYS AS IDENTITY)`, 0)
			identitySmokeRows(t, db, `SELECT last_value,is_called FROM bad_id_seq`, [][]any{{int64(1), false}})
		})
	}
	t.Run("owner collision", func(t *testing.T) {
		source, dest := identitySmokeDB(t), identitySmokeDB(t)
		// Distinct drivers above own distinct catalogs despite their equal DSNs.
		for _, q := range []string{`CREATE SCHEMA portable`, `CREATE TABLE portable.i(id INT GENERATED ALWAYS AS IDENTITY)`, `CREATE VIEW portable.v AS SELECT last_value FROM portable.i_id_seq`, `CREATE TABLE portable.c(p INT CHECK(nextval('portable.i_id_seq')>0))`} {
			identitySmokeExec(t, source, q, 0)
		}
		identitySmokeExec(t, source, `INSERT INTO portable.i DEFAULT VALUES`, 1)
		ctx := context.Background()
		kept, err := dest.Conn(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer kept.Close()
		if _, err := kept.ExecContext(ctx, `CREATE TEMP SEQUENCE kept START 30 CACHE 10`); err != nil {
			t.Fatal(err)
		}
		var value int64
		if err := kept.QueryRowContext(ctx, `SELECT nextval('kept')`).Scan(&value); err != nil || value != 30 {
			t.Fatal(value, err)
		}
		identitySmokeExec(t, dest, `CREATE TABLE displaced(v INT)`, 0)
		identitySmokeExec(t, dest, `CREATE TABLE displaced2(v INT)`, 0)
		var initial, dump, again bytes.Buffer
		if err := stuntgres.Dump(ctx, source, &initial); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Restore(ctx, dest, bytes.NewReader(initial.Bytes())); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Dump(ctx, dest, &dump); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Restore(ctx, dest, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Dump(ctx, dest, &again); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(dump.Bytes(), again.Bytes()) {
			t.Fatal("owner collision redump changed")
		}
		identitySmokeRows(t, dest, `SELECT id FROM portable.i`, [][]any{{int64(1)}})
		identitySmokeRows(t, dest, `SELECT last_value FROM portable.v`, [][]any{{int64(1)}})
		held, err := dest.Prepare(`INSERT INTO portable.c VALUES(7)`)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := held.Exec(); err != nil {
			t.Fatal(err)
		}
		held.Close()
		identitySmokeExec(t, dest, `INSERT INTO portable.i DEFAULT VALUES`, 1)
		identitySmokeRows(t, dest, `SELECT id FROM portable.i ORDER BY id`, [][]any{{int64(1)}, {int64(3)}})
		identitySmokeRows(t, dest, `SELECT last_value FROM portable.v`, [][]any{{int64(3)}})
		_, err = dest.Exec(`ALTER TABLE portable.i ALTER id DROP IDENTITY`)
		identitySmokeState(t, err, "2BP01")
		identitySmokeExec(t, dest, `DROP TABLE portable.i CASCADE`, 0)
		identitySmokeExec(t, dest, `INSERT INTO portable.c VALUES(9)`, 1)
		identitySmokeRows(t, dest, `SELECT p FROM portable.c ORDER BY p`, [][]any{{int64(7)}, {int64(9)}})
		rows, missingErr := dest.Query(`SELECT last_value FROM portable.i_id_seq`)
		if rows != nil {
			rows.Close()
		}
		identitySmokeState(t, missingErr, "42P01")
		if err := kept.QueryRowContext(ctx, `SELECT nextval('kept')`).Scan(&value); err != nil || value != 31 {
			t.Fatal(value, err)
		}
		// Ordinary sequence providers remain prefix-created, not graph vertices.
		identitySmokeExec(t, dest, `CREATE SEQUENCE ordinary`, 0)
		identitySmokeExec(t, dest, `CREATE TABLE ordinary_c(p INT CHECK(nextval('ordinary')>0))`, 0)
		identitySmokeExec(t, dest, `CREATE VIEW ordinary_v AS SELECT last_value FROM ordinary`, 0)
		var ordinary bytes.Buffer
		if err := stuntgres.Dump(ctx, dest, &ordinary); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Restore(ctx, dest, bytes.NewReader(ordinary.Bytes())); err != nil {
			t.Fatal(err)
		}
		identitySmokeExec(t, dest, `INSERT INTO ordinary_c VALUES(4)`, 1)
		identitySmokeRows(t, dest, `SELECT last_value FROM ordinary_v`, [][]any{{int64(1)}})
	})
}
