package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

// Original public consumer routes run in fresh phases; the Context companions remain intact.
func TestReferenceAlterTypePreparedMetadataSourceConsumerRoutes(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			run := func(s fixture.Step) { s.NonContext = !s.Prepared; fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
			run(fixture.Exec("CREATE TABLE t(id INT,c INT)", 0))
			run(fixture.Exec("INSERT INTO t VALUES(1,7)", 1))
			conn, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			held, err := conn.PrepareContext(p.Ctx, "SELECT c FROM t")
			if err != nil {
				t.Fatal(err)
			}
			defer held.Close()
			stable, err := conn.PrepareContext(p.Ctx, "SELECT c::text FROM t")
			if err != nil {
				t.Fatal(err)
			}
			defer stable.Close()
			fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Exec("ALTER TABLE t ALTER c TYPE BIGINT USING c+1", 0))
			rows, err := held.QueryContext(p.Ctx)
			if rows != nil {
				rows.Close()
			}
			want := fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "0A000", Message: "cached plan must not change result type"}
			if got := fixture.ExtractDiagnostic(t, err); got != want {
				t.Fatal(got, want)
			}
			rows, err = stable.QueryContext(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			if !rows.Next() {
				t.Fatal("missing stable row")
			}
			var value string
			if err = rows.Scan(&value); err != nil || value != "8" {
				t.Fatal(value, err)
			}
			if rows.Next() || rows.Err() != nil {
				t.Fatal("extra row", rows.Err())
			}
			rows.Close()
			fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Select("SELECT c FROM t", []string{"c"}, []string{"INT8"}, [][]any{{int64(8)}}))
		})
	}
}

func TestReferenceAlterTypeTransactionsAndLiveRowsSourceConsumerRoutes(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			run := func(s fixture.Step) { s.NonContext = !s.Prepared; fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
			run(fixture.Exec("CREATE TABLE t(id INT,c TEXT)", 0))
			run(fixture.Exec("INSERT INTO t VALUES(1,'oops'),(2,'bad'),(3,'9')", 3))
			// Ended versions must not be converted, even when their payload is invalid.
			run(fixture.Exec("UPDATE t SET c='7' WHERE id=1", 1))
			run(fixture.Exec("DELETE FROM t WHERE id=2", 1))
			tx, err := b.DB.Begin()
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			tr := func(s fixture.Step) { s.NonContext = !s.Prepared; fixture.RunStep(t, p.Ctx, tx, b.Recorder, s) }
			tr(fixture.Exec("INSERT INTO t VALUES(4,'ownbad')", 1))
			tr(fixture.Exec("UPDATE t SET c='8' WHERE id=4", 1))
			tr(fixture.Exec("INSERT INTO t VALUES(5,'deletedbad')", 1))
			tr(fixture.Exec("DELETE FROM t WHERE id=5", 1))
			tr(fixture.Exec("SAVEPOINT s", 0))
			tr(fixture.Exec("ALTER TABLE t ALTER c TYPE INT USING c::int", 0))
			tr(fixture.Select("SELECT c FROM t ORDER BY id", []string{"c"}, []string{"INT4"}, [][]any{{int64(7)}, {int64(9)}, {int64(8)}}))
			tr(fixture.Exec("ROLLBACK TO s", 0))
			tr(fixture.Exec("ROLLBACK TO s", 0))
			tr(fixture.Select("SELECT c FROM t ORDER BY id", []string{"c"}, []string{"TEXT"}, [][]any{{"7"}, {"9"}, {"8"}}))
			tr(fixture.Exec("INSERT INTO t VALUES(6,'latebad')", 1))
			tr(fixture.ImplicitError("ALTER TABLE t ALTER c TYPE INT USING c::int", fixture.Diagnostic{Code: "22P02", Message: `invalid input syntax for type integer: "latebad"`}))
			tr(fixture.ImplicitError("SELECT * FROM t", fixture.Diagnostic{Code: "25P02", Message: "current transaction is aborted, commands ignored until end of transaction block"}))
			tr(fixture.Exec("ROLLBACK TO s", 0))
			tr(fixture.Exec("ALTER TABLE t ALTER c TYPE INT USING c::int", 0))
			if err = tx.Commit(); err != nil {
				t.Fatal(err)
			}
			run(fixture.Select("SELECT c FROM t ORDER BY id", []string{"c"}, []string{"INT4"}, [][]any{{int64(7)}, {int64(9)}, {int64(8)}}))
			ro, err := b.DB.BeginTx(p.Ctx, &sql.TxOptions{ReadOnly: true})
			if err != nil {
				t.Fatal(err)
			}
			fixture.RunStep(t, p.Ctx, ro, b.Recorder, fixture.ImplicitError("ALTER TABLE absent ALTER c TYPE BIGINT", fixture.Diagnostic{Code: "25006", Message: "cannot execute ALTER TABLE in a read-only transaction"}))
			if err = ro.Rollback(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestReferenceAlterTypeCommittedOwnUpdateAndDeleteSourceConsumerRoutes(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			run := func(s fixture.Step) { s.NonContext = !s.Prepared; fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
			run(fixture.Exec("CREATE TABLE t(id INT,c TEXT)", 0))
			run(fixture.Exec("INSERT INTO t VALUES(1,'bad'),(2,'deletedbad'),(3,'9')", 3))
			tx, err := b.DB.Begin()
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			tr := func(s fixture.Step) { s.NonContext = !s.Prepared; fixture.RunStep(t, p.Ctx, tx, b.Recorder, s) }
			tr(fixture.Exec("UPDATE t SET c='8' WHERE id=1", 1))
			tr(fixture.Exec("DELETE FROM t WHERE id=2", 1))
			tr(fixture.Exec("ALTER TABLE t ALTER c TYPE INT USING c::int", 0))
			want := [][]any{{int64(1), int64(8)}, {int64(3), int64(9)}}
			tr(fixture.Select("SELECT id,c FROM t ORDER BY id", []string{"id", "c"}, []string{"INT4", "INT4"}, want))
			if err = tx.Commit(); err != nil {
				t.Fatal(err)
			}
			run(fixture.Select("SELECT id,c FROM t ORDER BY id", []string{"id", "c"}, []string{"INT4", "INT4"}, want))
			run(fixture.Exec("UPDATE t SET c=c+1 WHERE id=1", 1))
			run(fixture.Exec("DELETE FROM t WHERE id=3", 1))
			run(fixture.Select("SELECT id,c FROM t", []string{"id", "c"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(9)}}))
			run(fixture.Exec("CREATE TABLE u(id INT,c TEXT)", 0))
			run(fixture.Exec("INSERT INTO u VALUES(1,'bad'),(2,'deletedbad'),(3,'9')", 3))
			rollback, err := b.DB.Begin()
			if err != nil {
				t.Fatal(err)
			}
			defer rollback.Rollback()
			rr := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, rollback, b.Recorder, s) }
			rr(fixture.Exec("UPDATE u SET c='7' WHERE id=1", 1))
			rr(fixture.Exec("DELETE FROM u WHERE id=2", 1))
			rr(fixture.Exec("ALTER TABLE u ALTER c TYPE INT USING c::int", 0))
			rr(fixture.Select("SELECT id,c FROM u ORDER BY id", []string{"id", "c"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(7)}, {int64(3), int64(9)}}))
			if err = rollback.Rollback(); err != nil {
				t.Fatal(err)
			}
			run(fixture.Select("SELECT id,c FROM u ORDER BY id", []string{"id", "c"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "bad"}, {int64(2), "deletedbad"}, {int64(3), "9"}}))
		})
	}
}

func TestReferenceAlterTypeRepeatableReadBeforeSnapshotAndPostTypeMarkSourceConsumerRoutes(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			run := func(s fixture.Step) { s.NonContext = !s.Prepared; fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
			run(fixture.Exec("CREATE TABLE t(c INT,discard INT)", 0))
			run(fixture.Exec("INSERT INTO t VALUES(7,1)", 1))
			run(fixture.Exec("CREATE TABLE anchor(c INT)", 0))
			run(fixture.Exec("INSERT INTO anchor VALUES(1)", 1))
			tx, err := b.DB.BeginTx(p.Ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			tr := func(s fixture.Step) { s.NonContext = !s.Prepared; fixture.RunStep(t, p.Ctx, tx, b.Recorder, s) }
			tr(fixture.Exec("ALTER TABLE t ALTER c TYPE BIGINT USING c+1", 0))
			tr(fixture.Exec("SAVEPOINT after_type", 0))
			tr(fixture.Select("SELECT c FROM anchor", []string{"c"}, []string{"INT4"}, [][]any{{int64(1)}}))
			run(fixture.Exec("UPDATE anchor SET c=2", 1))
			tr(fixture.Exec("ALTER TABLE t ALTER c TYPE INT USING c+10, DROP discard", 0))
			tr(fixture.Exec("UPDATE t SET c=c+100", 1))
			tr(fixture.Exec("ROLLBACK TO after_type", 0))
			tr(fixture.Exec("ROLLBACK TO after_type", 0))
			tr(fixture.Select("SELECT * FROM t", []string{"c", "discard"}, []string{"INT8", "INT4"}, [][]any{{int64(8), int64(1)}}))
			tr(fixture.Select("SELECT c FROM anchor", []string{"c"}, []string{"INT4"}, [][]any{{int64(1)}}))
			tr(fixture.Exec("ALTER TABLE t ALTER c TYPE INT USING c+1, DROP discard", 0))
			tr(fixture.Exec("ALTER TABLE t ALTER c TYPE BIGINT USING c+1", 0))
			tr(fixture.Select("SELECT * FROM t", []string{"c"}, []string{"INT8"}, [][]any{{int64(10)}}))
			tr(fixture.Select("SELECT c FROM anchor", []string{"c"}, []string{"INT4"}, [][]any{{int64(1)}}))
			if err = tx.Commit(); err != nil {
				t.Fatal(err)
			}
			run(fixture.Select("SELECT * FROM t", []string{"c"}, []string{"INT8"}, [][]any{{int64(10)}}))
			run(fixture.Select("SELECT c FROM anchor", []string{"c"}, []string{"INT4"}, [][]any{{int64(2)}}))
		})
	}
}
