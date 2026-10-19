package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func fdwWriteFailedExec(t *testing.T, ctx context.Context, c *sql.Conn, q string, want error) {
	t.Helper()
	result, err := c.ExecContext(ctx, q)
	if err == nil || result != nil || want != nil && !errors.Is(err, want) {
		t.Fatalf("%s result=%v err=%v want=%v", q, result, err, want)
	}
}
func fdwWriteNativeReturning(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fdwSQLReader, run func(fixture.Step)) {
	t.Helper()
	w := r.writer
	w.rewrite = func(row stuntgres.ForeignRow) (stuntgres.ForeignRow, bool) {
		if row[0] == int64(41) {
			return nil, false
		}
		row[1] = "actually-stored"
		return row, true
	}
	run(fdwRows("INSERT INTO fdw_data VALUES(40,'proposal',true) RETURNING id,label,flag", [][]any{{int64(40), "actually-stored", true}}))
	run(fdwRows("INSERT INTO fdw_data VALUES(41,'suppressed',true) RETURNING id,label,flag", nil))
	run(fixture.Exec("INSERT INTO fdw_data VALUES(41,'suppressed',true)", 0))
	run(fdwRows("UPDATE fdw_data SET label='proposal-again' WHERE id=40 RETURNING id,label,flag", [][]any{{int64(40), "actually-stored", true}}))
	w.rewrite = nil
	buffer := []byte("owned")
	w.resultHook = func(row stuntgres.ForeignRow) stuntgres.ForeignRow { row[1] = buffer; return row }
	w.writeHook = func(ctx context.Context, name string) error {
		if name == "EndForeignModify" {
			for i := range buffer {
				buffer[i] = 'X'
			}
		}
		return nil
	}
	run(fdwRows("INSERT INTO fdw_data VALUES(42,'source-value',false) RETURNING id,label,flag", [][]any{{int64(42), "owned", false}}))
	w.writeHook = nil
	for _, bad := range []func(stuntgres.ForeignRow) stuntgres.ForeignRow{
		func(row stuntgres.ForeignRow) stuntgres.ForeignRow { return row[:1] },
		func(row stuntgres.ForeignRow) stuntgres.ForeignRow { row[0] = "not-an-integer"; return row },
	} {
		w.resultHook = bad
		fdwWriteFailedExec(t, p.Ctx, c, "INSERT INTO fdw_data VALUES(43,'bad-result',true)", nil)
		fdwWriteSourceRows(t, p, b, r, fdwRows("SELECT id,label,flag FROM fdw_source.data WHERE id=43", nil))
	}
	w.resultHook = nil
	fdwWriteFailedExec(t, p.Ctx, c, "INSERT INTO fdw_data VALUES(44,'projection-fails',true) RETURNING id/(id-id)", nil)
	fdwWriteSourceRows(t, p, b, r, fdwRows("SELECT id,label,flag FROM fdw_source.data WHERE id=44 OR id=41 ORDER BY id", nil))
	fdwWriteSourceRows(t, p, b, r, fdwRows("SELECT id,label,flag FROM fdw_source.data WHERE id=40", [][]any{{int64(40), "actually-stored", true}}))
}

func fdwWriteNativeBatch(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fdwSQLReader, run func(fixture.Step)) {
	t.Helper()
	w := r.writer
	if !reflect.DeepEqual(w.batches[:3], []int{2, 2, 1}) {
		t.Fatal("batch schedule", w.batches)
	}
	before := len(w.batches)
	run(fdwRows("INSERT INTO fdw_data VALUES(20,'returning-single',true),(21,'returning-single',false) RETURNING id,label,flag", [][]any{{int64(20), "returning-single", true}, {int64(21), "returning-single", false}}))
	if len(w.batches) != before {
		t.Fatal("RETURNING batched")
	}
	for _, handler := range []string{"fdw_single_writer_handler", "fdw_batch_without_size_handler", "fdw_size_without_batch_handler"} {
		run(fixture.Exec("ALTER FOREIGN DATA WRAPPER fdw_native HANDLER "+handler, 0))
		before := len(w.batches)
		run(fixture.Exec("INSERT INTO fdw_data VALUES(30,'single',true),(31,'single',false)", 2))
		if len(w.batches) != before {
			t.Fatal("missing-half invoked batch", handler)
		}
		fdwWriteSourceRows(t, p, b, r, fdwRows("SELECT id,label,flag FROM fdw_source.data WHERE id=30 OR id=31 ORDER BY id", [][]any{{int64(30), "single", true}, {int64(31), "single", false}}))
		fdwSourceExec(t, p, b, r, run, "DELETE FROM fdw_source.data WHERE id=30 OR id=31", 2)
	}
	run(fixture.Exec("ALTER FOREIGN DATA WRAPPER fdw_native HANDLER fdw_sql_handler", 0))
	for _, size := range []int{0, -1} {
		w.batchSize = size
		fdwWriteFailedExec(t, p.Ctx, c, "INSERT INTO fdw_data VALUES(32,'invalid-size',true)", nil)
	}
	w.batchSize = 1
	before = len(w.batches)
	run(fixture.Exec("INSERT INTO fdw_data VALUES(33,'size-one',true)", 1))
	if len(w.batches) != before {
		t.Fatal("size1 batched")
	}
	w.batchSize = 2
	w.rewrite = func(row stuntgres.ForeignRow) (stuntgres.ForeignRow, bool) { return row, row[0] != int64(35) }
	run(fixture.Exec("INSERT INTO fdw_data VALUES(34,'kept',true),(35,'suppressed',true),(36,'kept',false)", 2))
	w.rewrite = nil
	fdwWriteSourceRows(t, p, b, r, fdwRows("SELECT id,label,flag FROM fdw_source.data WHERE id>=34 ORDER BY id", [][]any{{int64(34), "kept", true}, {int64(36), "kept", false}}))
	w.batchHook = func(rows []stuntgres.ForeignRow) []stuntgres.ForeignRow { return append(rows, rows[0]) }
	fdwWriteFailedExec(t, p.Ctx, c, "INSERT INTO fdw_data VALUES(37,'oversized',true),(38,'oversized',true)", nil)
	w.batchHook = nil
	fdwWriteSourceRows(t, p, b, r, fdwRows("SELECT id,label,flag FROM fdw_source.data WHERE id>=37 ORDER BY id", nil))
}

func fdwWriteNativeTransactions(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fdwSQLReader, run func(fixture.Step)) {
	t.Helper()
	w := r.writer
	failure := errors.New("real writer teardown failed")
	for _, method := range []string{"BeginForeignModify", "EndForeignModify", "ExecForeignInsert", "EndForeignScan", "EndStatement"} {
		q := "INSERT INTO fdw_data VALUES(50,'must-rollback',true)"
		if method == "EndForeignScan" {
			q = "INSERT INTO fdw_data SELECT id+49,label,flag FROM fdw_data WHERE id=1"
		}
		begins, ends := w.modifies, w.modifyEnds
		w.writeHook = func(ctx context.Context, name string) error {
			if name == method {
				return failure
			}
			return nil
		}
		fdwWriteFailedExec(t, p.Ctx, c, q, failure)
		w.writeHook = nil
		if w.modifies-begins != 1 || w.modifyEnds-ends != 1 {
			t.Fatal("modify lifecycle", method, w.modifies-begins, w.modifyEnds-ends)
		}
		fdwWriteSourceRows(t, p, b, r, fdwRows("SELECT id,label,flag FROM fdw_source.data WHERE id=50", nil))
	}
	ctx, cancel := context.WithCancel(p.Ctx)
	w.writeHook = func(ctx context.Context, name string) error {
		if name == "ExecForeignInsert" {
			cancel()
			return context.Canceled
		}
		return nil
	}
	fdwWriteFailedExec(t, ctx, c, "INSERT INTO fdw_data VALUES(50,'canceled',true)", context.Canceled)
	w.writeHook = nil
	cancel()
	fdwWriteSourceRows(t, p, b, r, fdwRows("SELECT id,label,flag FROM fdw_source.data WHERE id=50", nil))
	// A source constraint aborts the subtransaction, not a successful prefix.
	run(fixture.Exec("BEGIN", 0))
	run(fixture.Exec("SAVEPOINT recovery", 0))
	fdwSourceExec(t, p, b, r, run, "ALTER TABLE fdw_source.data ADD CONSTRAINT writer_positive CHECK(id>0)", 0)
	fdwWriteFailedExec(t, p.Ctx, c, "INSERT INTO fdw_data VALUES(50,'prefix',true),(-1,'bad',true)", nil)
	run(fixture.Exec("ROLLBACK TO recovery", 0))
	run(fixture.Exec("INSERT INTO fdw_data VALUES(51,'recovered',true)", 1))
	run(fixture.Exec("RELEASE recovery", 0))
	run(fixture.Exec("COMMIT", 0))
	fdwWriteSourceRows(t, p, b, r, fdwRows("SELECT id,label,flag FROM fdw_source.data WHERE id>=50 ORDER BY id", [][]any{{int64(51), "recovered", true}}))
	for _, handler := range []string{"fdw_uncontracted_writer_handler", "fdw_no_savepoint_handler"} {
		run(fixture.Exec("ALTER FOREIGN DATA WRAPPER fdw_native HANDLER "+handler, 0))
		run(fixture.Exec("BEGIN", 0))
		run(fixture.Exec("SAVEPOINT forbidden", 0))
		message := "foreign transaction does not support savepoints"
		if handler == "fdw_uncontracted_writer_handler" {
			message = "foreign writes require statement and transaction rollback support"
		}
		run(fixture.ErrorDiagnostic("INSERT INTO fdw_data VALUES(52,'rejected',true)", fdwDiagnostic("0A000", message)))
		run(fixture.Exec("ROLLBACK", 0))
	}
	run(fixture.Exec("ALTER FOREIGN DATA WRAPPER fdw_native HANDLER fdw_no_savepoint_handler", 0))
	run(fixture.Exec("BEGIN", 0))
	run(fixture.Exec("INSERT INTO fdw_data VALUES(52,'staged',true)", 1))
	run(fixture.ErrorDiagnostic("SAVEPOINT forbidden", fdwDiagnostic("0A000", "foreign transaction does not support savepoints")))
	run(fixture.Exec("ROLLBACK", 0))
	run(fixture.Exec("ALTER FOREIGN DATA WRAPPER fdw_native HANDLER fdw_sql_handler", 0))
	fdwWriteSourceRows(t, p, b, r, fdwRows("SELECT id,label,flag FROM fdw_source.data WHERE id=52", nil))
	run(fixture.Exec("BEGIN", 0))
	run(fixture.Exec("INSERT INTO fdw_data VALUES(54,'aborted-commit',true)", 1))
	fdwWriteFailedExec(t, p.Ctx, c, "INSERT INTO fdw_data VALUES(-2,'abort',true)", nil)
	run(fixture.Exec("COMMIT", 0))
	fdwWriteSourceRows(t, p, b, r, fdwRows("SELECT id,label,flag FROM fdw_source.data WHERE id=54", nil))
	attempts := 0
	w.writeHook = func(ctx context.Context, name string) error {
		if name == "Commit" {
			attempts++
			return failure
		}
		return nil
	}
	fdwWriteFailedExec(t, p.Ctx, c, "INSERT INTO fdw_data VALUES(53,'commit-fails',true)", failure)
	w.writeHook = nil
	if attempts != 1 {
		t.Fatal("uncertain commit retried", attempts)
	}
	fdwWriteSourceRows(t, p, b, r, fdwRows("SELECT id,label,flag FROM fdw_source.data WHERE id=53", nil))
	fdwWritePartialCommit(t, p, b, c, r, run)
}

func fdwWritePartialCommit(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fdwSQLReader, run func(fixture.Step)) {
	t.Helper()
	fdwSourceExec(t, p, b, r, run, "CREATE TABLE fdw_source.second(id integer,label text,flag boolean)", 0)
	run(fixture.Exec("CREATE SERVER fdw_second FOREIGN DATA WRAPPER fdw_native", 0))
	run(fixture.Exec("CREATE USER MAPPING FOR CURRENT_USER SERVER fdw_second", 0))
	run(fixture.Exec("CREATE FOREIGN TABLE fdw_second_data(id integer,label text,flag boolean) SERVER fdw_second OPTIONS(schema_name 'fdw_source',table_name 'second')", 0))
	run(fixture.Exec("CREATE TABLE fdw_local_commit(id integer)", 0))
	run(fixture.Exec("BEGIN", 0))
	run(fixture.Exec("INSERT INTO fdw_data VALUES(60,'first-remote',true)", 1))
	run(fixture.Exec("INSERT INTO fdw_second_data VALUES(61,'second-remote',false)", 1))
	run(fixture.Exec("INSERT INTO fdw_local_commit VALUES(62)", 1))
	attempts := 0
	failure := errors.New("second remote commit failed")
	r.writer.writeHook = func(ctx context.Context, name string) error {
		if name == "Commit" {
			attempts++
			if attempts == 2 {
				return failure
			}
		}
		return nil
	}
	fdwWriteFailedExec(t, p.Ctx, c, "COMMIT", failure)
	r.writer.writeHook = nil
	if attempts != 2 {
		t.Fatal("remote commit order/retry", attempts)
	}
	fdwWriteSourceRows(t, p, b, r, fdwRows("SELECT id,label,flag FROM fdw_source.data WHERE id=60", [][]any{{int64(60), "first-remote", true}}))
	fdwWriteSourceRows(t, p, b, r, fdwRows("SELECT id,label,flag FROM fdw_source.second", nil))
	run(fixture.Select("SELECT id FROM fdw_local_commit", []string{"id"}, []string{"INT4"}, nil))
}
