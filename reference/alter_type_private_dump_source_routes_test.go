package reference_test

import (
	"bytes"
	"database/sql"
	"stuntgres"
	"testing"
)

// Original public consumer routes run in fresh phases; the Context companions remain intact.
func TestReferenceAlterTypeOwnerRepeatableReadAndPrivateDumpSourceConsumerRoutes(t *testing.T) {
	p := newReferenceScenario(t)
	for _, b := range p.backends() {
		t.Run(b.name, func(t *testing.T) {
			run := func(s referenceStep) { s.nonContext = !s.prepared; referenceRunStep(t, p.ctx, b.db, b.recorder, s) }
			for _, q := range []string{"CREATE TABLE t(c TEXT DEFAULT '7')", "CREATE TABLE u(c TEXT)", "CREATE TABLE anchor(c INT)"} {
				run(referenceExec(q, 0))
			}
			for _, q := range []string{"INSERT INTO t VALUES('oops')", "INSERT INTO u VALUES('oops')", "INSERT INTO anchor VALUES(1)"} {
				run(referenceExec(q, 1))
			}
			tx, err := b.db.BeginTx(p.ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			tr := func(s referenceStep) { s.nonContext = !s.prepared; referenceRunStep(t, p.ctx, tx, b.recorder, s) }
			tr(referenceSelect("SELECT c FROM anchor", []string{"c"}, []string{"INT4"}, [][]any{{int64(1)}}))
			run(referenceExec("UPDATE u SET c='7'", 1))
			tr(referenceExec("SAVEPOINT before_type", 0))
			tr(referenceExec("ALTER TABLE u ALTER c TYPE INT USING c::int", 0))
			tr(referenceSelect("SELECT c FROM u", []string{"c"}, []string{"INT4"}, [][]any{{int64(7)}}))
			if !b.pg {
				var dump bytes.Buffer
				if err := stuntgres.Dump(p.ctx, p.native, &dump); err != nil {
					t.Fatal(err)
				}
				connector, err := stuntgres.NewDriver().OpenConnector(t.Name() + "-restore")
				if err != nil {
					t.Fatal(err)
				}
				restored := sql.OpenDB(connector)
				defer restored.Close()
				if err := stuntgres.Restore(p.ctx, restored, bytes.NewReader(dump.Bytes())); err != nil {
					t.Fatal(err)
				}
				rows, err := restored.Query("SELECT c FROM u")
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
			tr(referenceExec("ROLLBACK TO before_type", 0))
			tr(referenceSelect("SELECT c FROM u", []string{"c"}, []string{"TEXT"}, [][]any{{"oops"}}))
			if err := tx.Rollback(); err != nil {
				t.Fatal(err)
			}
			run(referenceSelect("SELECT c FROM u", []string{"c"}, []string{"TEXT"}, [][]any{{"7"}}))
		})
	}
}
