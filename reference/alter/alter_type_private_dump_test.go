package reference_test

import (
	"bytes"
	"database/sql"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceAlterTypeOwnerRepeatableReadAndPrivateDump(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
			for _, q := range []string{"CREATE TABLE t(c TEXT DEFAULT '7')", "CREATE TABLE u(c TEXT)", "CREATE TABLE anchor(c INT)"} {
				run(fixture.Exec(q, 0))
			}
			for _, q := range []string{"INSERT INTO t VALUES('oops')", "INSERT INTO u VALUES('oops')", "INSERT INTO anchor VALUES(1)"} {
				run(fixture.Exec(q, 1))
			}
			tx, err := b.DB.BeginTx(p.Ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			tr := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, tx, b.Recorder, s) }
			tr(fixture.Select("SELECT c FROM anchor", []string{"c"}, []string{"INT4"}, [][]any{{int64(1)}}))
			run(fixture.Exec("UPDATE u SET c='7'", 1))
			tr(fixture.Exec("SAVEPOINT before_type", 0))
			tr(fixture.Exec("ALTER TABLE u ALTER c TYPE INT USING c::int", 0))
			tr(fixture.Select("SELECT c FROM u", []string{"c"}, []string{"INT4"}, [][]any{{int64(7)}}))
			if !b.PG {
				var dump bytes.Buffer
				if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
					t.Fatal(err)
				}
				connector, err := stuntgres.NewDriver().OpenConnector(t.Name() + "-restore")
				if err != nil {
					t.Fatal(err)
				}
				restored := sql.OpenDB(connector)
				defer restored.Close()
				if err := stuntgres.Restore(p.Ctx, restored, bytes.NewReader(dump.Bytes())); err != nil {
					t.Fatal(err)
				}
				rows, err := restored.QueryContext(p.Ctx, "SELECT c FROM u")
				if err != nil {
					t.Fatal(err)
				}
				defer rows.Close()
				metadata, err := rows.ColumnTypes()
				if err != nil || len(metadata) != 1 || metadata[0].DatabaseTypeName() != "TEXT" {
					t.Fatal(metadata, err)
				}
				if !rows.Next() {
					t.Fatal(rows.Err())
				}
				var value string
				if err := rows.Scan(&value); err != nil || value != "7" {
					t.Fatal(value, err)
				}
				if rows.Next() || rows.Err() != nil {
					t.Fatal(rows.Err())
				}
				if err := rows.Close(); err != nil {
					t.Fatal(err)
				}
			}
			tr(fixture.Exec("ROLLBACK TO before_type", 0))
			tr(fixture.Select("SELECT c FROM u", []string{"c"}, []string{"TEXT"}, [][]any{{"oops"}}))
			if err := tx.Rollback(); err != nil {
				t.Fatal(err)
			}
			run(fixture.Select("SELECT c FROM u", []string{"c"}, []string{"TEXT"}, [][]any{{"7"}}))
		})
	}
}
