package reference_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func TestReferenceCollationLegacyAlterType(t *testing.T) {
	fixture.CollationScenario(t, func(t *testing.T, p *fixture.Pair, backend fixture.Backend) {
		db := backend.DB
		fixture.UpdateReturningReferenceExec(t, db, "CREATE TABLE t(id INT,c TEXT)")
		fixture.UpdateReturningReferenceExec(t, db, "INSERT INTO t VALUES(1,'41'),(2,NULL),(3,'7')")
		held, err := db.Query("SELECT c FROM t ORDER BY id")
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		old, err := db.Prepare("SELECT c FROM t ORDER BY id")
		if err != nil {
			t.Fatal(err)
		}
		defer old.Close()
		utility, err := db.Prepare("ALTER TABLE t ALTER c TYPE INT USING c::integer")
		if err != nil {
			t.Fatal(err)
		}
		result, err := utility.Exec()
		utility.Close()
		if err != nil {
			t.Fatal(err)
		}
		if n, e := result.RowsAffected(); e != nil || n != 0 {
			t.Fatal(n, e)
		}
		rows, err := db.Query("SELECT c FROM t ORDER BY id")
		if err != nil {
			t.Fatal(err)
		}
		collationLegacyRows(t, backend.PG, rows, []string{"c"}, []string{"INT4"}, [][]any{{int64(41)}, {nil}, {int64(7)}})
		_, err = old.Query()
		collationLegacyError(t, err, "0A000", "cached plan must not change result type", "")
		collationLegacyRows(t, backend.PG, held, []string{"c"}, []string{"TEXT"}, [][]any{{"41"}, {nil}, {"7"}})
		fixture.UpdateReturningReferenceExec(t, db, "CREATE TABLE bad(c TEXT)")
		fixture.UpdateReturningReferenceExec(t, db, "INSERT INTO bad VALUES('41'),('oops')")
		_, err = db.Exec("ALTER TABLE bad ALTER c TYPE INT USING c::int")
		collationLegacyError(t, err, "22P02", `invalid input syntax for type integer: "oops"`, "")
		rows, err = db.Query("SELECT c FROM bad ORDER BY c")
		if err != nil {
			t.Fatal(err)
		}
		collationLegacyRows(t, backend.PG, rows, []string{"c"}, []string{"TEXT"}, [][]any{{"41"}, {"oops"}})
		tx, err := db.Begin()
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		retainedInsert, err := tx.Prepare("INSERT INTO t VALUES(4,$1)")
		if err != nil {
			t.Fatal(err)
		}
		defer retainedInsert.Close()
		if _, err = tx.Exec("ALTER TABLE t ALTER c TYPE BIGINT"); err != nil {
			t.Fatal(err)
		}
		rows, err = tx.Query("SELECT c FROM t ORDER BY id")
		if err != nil {
			t.Fatal(err)
		}
		collationLegacyRows(t, backend.PG, rows, []string{"c"}, []string{"INT8"}, [][]any{{int64(41)}, {nil}, {int64(7)}})
		if _, err = retainedInsert.Exec(int64(11)); err != nil {
			t.Fatal("retained input contract", err)
		}
		rows, err = tx.Query("SELECT c FROM t WHERE id=4")
		if err != nil {
			t.Fatal(err)
		}
		collationLegacyRows(t, backend.PG, rows, []string{"c"}, []string{"INT8"}, [][]any{{int64(11)}})
		if err = tx.Rollback(); err != nil {
			t.Fatal(err)
		}
		rows, err = db.Query("SELECT c FROM t ORDER BY id")
		if err != nil {
			t.Fatal(err)
		}
		collationLegacyRows(t, backend.PG, rows, []string{"c"}, []string{"INT4"}, [][]any{{int64(41)}, {nil}, {int64(7)}})
		fixture.UpdateReturningReferenceExec(t, db, "CREATE TABLE original(a INT,b INT,c INT)")
		fixture.UpdateReturningReferenceExec(t, db, "INSERT INTO original VALUES(2,7,9)")
		fixture.UpdateReturningReferenceExec(t, db, "ALTER TABLE original ALTER a TYPE TEXT USING b, ALTER b TYPE BIGINT USING a*10, DROP c")
		rows, err = db.Query("SELECT * FROM original")
		if err != nil {
			t.Fatal(err)
		}
		collationLegacyRows(t, backend.PG, rows, []string{"a", "b"}, []string{"TEXT", "INT8"}, [][]any{{"7", int64(20)}})
		for _, tc := range []struct {
			table, declaration, seed, action, state, message string
			names, types                                     []string
			values                                           [][]any
		}{
			{"ordered_dup", "c INT", "(7)", "ALTER c TYPE INT USING 10/(c-c), ALTER c TYPE BIGINT USING c", "22012", "division by zero", []string{"c"}, []string{"INT4"}, [][]any{{int64(7)}}},
			{"ordered_collation", "c TEXT", "('7')", `ALTER c TYPE TEXT COLLATE "C" USING (10/(c::int-c::int))::text, ALTER c TYPE VARCHAR(5) COLLATE "POSIX" USING c`, "22012", "division by zero", []string{"c"}, []string{"TEXT"}, [][]any{{"7"}}},
			{"ordered_columns", "a TEXT,b TEXT", "('badA','badB')", "ALTER b TYPE INT USING b::int, ALTER a TYPE INT USING a::int", "22P02", `invalid input syntax for type integer: "badB"`, []string{"a", "b"}, []string{"TEXT", "TEXT"}, [][]any{{"badA", "badB"}}},
		} {
			fixture.UpdateReturningReferenceExec(t, db, "CREATE TABLE "+tc.table+"("+tc.declaration+")")
			fixture.UpdateReturningReferenceExec(t, db, "INSERT INTO "+tc.table+" VALUES"+tc.seed)
			for _, prepared := range []bool{false, true} {
				q := "ALTER TABLE " + tc.table + " " + tc.action
				if prepared {
					stmt, prepareErr := db.Prepare(q)
					if prepareErr != nil {
						t.Fatal("effect-free Prepare", prepareErr)
					}
					_, err = stmt.Exec()
					stmt.Close()
				} else {
					_, err = db.Exec(q)
				}
				collationLegacyError(t, err, tc.state, tc.message, "")
				rows, err = db.Query("SELECT * FROM " + tc.table)
				if err != nil {
					t.Fatal(err)
				}
				collationLegacyRows(t, backend.PG, rows, tc.names, tc.types, tc.values)
			}
		}
		fixture.UpdateReturningReferenceExec(t, db, "CREATE TABLE checked(c INT CONSTRAINT ck CHECK(c>0))")
		fixture.UpdateReturningReferenceExec(t, db, "INSERT INTO checked VALUES(1),(2),(NULL)")
		fixture.UpdateReturningReferenceExec(t, db, "ALTER TABLE checked ALTER c TYPE BIGINT")
		for _, prepared := range []bool{false, true} {
			for _, tc := range []struct {
				action string
				want   stuntgres.Error
			}{
				{"TYPE BIGINT USING -c", stuntgres.Error{Code: "23514", Message: `check constraint "ck" of relation "checked" is violated by some row`, Schema: "public", Table: "checked", Constraint: "ck"}},
				{"TYPE TEXT USING c::text", stuntgres.Error{Code: "42883", Message: "operator does not exist: text > integer", Hint: "No operator matches the given name and argument types. You might need to add explicit type casts."}},
			} {
				q := "ALTER TABLE checked ALTER c " + tc.action
				if prepared {
					stmt, e := db.Prepare(q)
					if e != nil {
						t.Fatal(e)
					}
					_, err = stmt.Exec()
					stmt.Close()
				} else {
					_, err = db.Exec(q)
				}
				tc.want.Severity, tc.want.SeverityUnlocalized = "ERROR", "ERROR"
				collationLegacyDiagnostic(t, err, tc.want)
				rows, err = db.Query("SELECT c FROM checked ORDER BY c")
				if err != nil {
					t.Fatal(err)
				}
				collationLegacyRows(t, backend.PG, rows, []string{"c"}, []string{"INT8"}, [][]any{{int64(1)}, {int64(2)}, {nil}})
			}
		}
		fixture.UpdateReturningReferenceExec(t, db, "INSERT INTO checked VALUES(3)")
		_, err = db.Exec("INSERT INTO checked VALUES(-1)")
		checkWant := stuntgres.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "23514", Message: `new row for relation "checked" violates check constraint "ck"`, Detail: "Failing row contains (-1).", Schema: "public", Table: "checked", Constraint: "ck"}
		collationLegacyDiagnostic(t, err, checkWant)
		fixture.UpdateReturningReferenceExec(t, db, "CREATE TABLE checked_tuple(a INT,b INT,CONSTRAINT ck CHECK(a<b))")
		fixture.UpdateReturningReferenceExec(t, db, "INSERT INTO checked_tuple VALUES(2,7)")
		fixture.UpdateReturningReferenceExec(t, db, "ALTER TABLE checked_tuple ALTER a TYPE BIGINT USING b,ALTER b TYPE BIGINT USING a*10")
		rows, err = db.Query("SELECT * FROM checked_tuple")
		if err != nil {
			t.Fatal(err)
		}
		collationLegacyRows(t, backend.PG, rows, []string{"a", "b"}, []string{"INT8", "INT8"}, [][]any{{int64(7), int64(20)}})
	})
}

func TestReferenceCollationLegacyAlterTypeControls(t *testing.T) {
	fixture.CollationScenario(t, func(t *testing.T, p *fixture.Pair, backend fixture.Backend) {
		db := backend.DB
		ctx := context.Background()
		fixture.UpdateReturningReferenceExec(t, db, "CREATE TABLE t(id INT,c VARCHAR(5),n INT DEFAULT 7 CONSTRAINT n_nn NOT NULL CONSTRAINT n_positive CHECK(n>0))")
		fixture.UpdateReturningReferenceExec(t, db, "INSERT INTO t VALUES(1,'a',7),(2,'Z',8)")
		fixture.UpdateReturningReferenceExec(t, db, "COMMENT ON COLUMN t.n IS 'retained'")
		fixture.UpdateReturningReferenceExec(t, db, `ALTER TABLE t ALTER c TYPE VARCHAR(2) COLLATE pg_catalog."POSIX", ALTER n TYPE BIGINT USING n+1`)
		fixture.UpdateReturningReferenceExec(t, db, "INSERT INTO t(id,c) VALUES(3,'b')")
		fixture.UpdateReturningReferenceExec(t, db, "CREATE TABLE numeric_control(c NUMERIC(8,3) CONSTRAINT ck CHECK(c>0) NO INHERIT)")
		fixture.UpdateReturningReferenceExec(t, db, "INSERT INTO numeric_control VALUES(1.235)")
		fixture.UpdateReturningReferenceExec(t, db, "ALTER TABLE numeric_control ALTER c TYPE NUMERIC(5,2)")
		var dump bytes.Buffer
		if !backend.PG {
			if err := stuntgres.Dump(ctx, p.Native, &dump); err != nil {
				t.Fatal(err)
			}
			for _, literal := range []string{"BIGINT", `COLLATE "pg_catalog"."POSIX"`, "n_nn", "n_positive", "ck", "NO INHERIT", "retained"} {
				if !strings.Contains(dump.String(), literal) {
					t.Fatal(literal, dump.String())
				}
			}
			if strings.Contains(dump.String(), "ALTER TABLE") {
				t.Fatal("dumped action history", dump.String())
			}
		}
		held, err := db.Query("SELECT n FROM t ORDER BY id")
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		if !backend.PG {
			if err = stuntgres.Reset(ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			if err = stuntgres.Restore(ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
		}
		numeric, err := db.Query("SELECT c FROM numeric_control")
		if err != nil {
			t.Fatal(err)
		}
		metadata, err := numeric.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		if precision, scale, ok := metadata[0].DecimalSize(); !ok || precision != 5 || scale != 2 {
			t.Fatal(precision, scale, ok)
		}
		collationLegacyRows(t, backend.PG, numeric, []string{"c"}, []string{"NUMERIC"}, [][]any{{"1.24"}})
		if !backend.PG {
			cancelCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()
			canceled, stop := context.WithCancel(cancelCtx)
			defer stop()
			reader, writer := io.Pipe()
			defer reader.Close()
			defer writer.Close()
			entered := make(chan struct{})
			done := make(chan error, 1)
			go func() {
				done <- stuntgres.Restore(canceled, p.Native, &collationLegacyReader{Reader: reader, entered: entered})
			}()
			joined := false
			defer func() {
				writer.Close()
				if !joined {
					<-done
				}
			}()
			select {
			case <-entered:
			case <-cancelCtx.Done():
				t.Fatal(cancelCtx.Err())
			}
			stop()
			writer.Close()
			if err = <-done; !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			joined = true
			var again bytes.Buffer
			if err = stuntgres.Dump(ctx, p.Native, &again); err != nil || again.String() != dump.String() {
				t.Fatal(err, again.String(), dump.String())
			}
		}
		fixture.UpdateReturningReferenceExec(t, db, "INSERT INTO numeric_control VALUES(1.235)")
		numeric, err = db.Query("SELECT c FROM numeric_control")
		if err != nil {
			t.Fatal(err)
		}
		collationLegacyRows(t, backend.PG, numeric, []string{"c"}, []string{"NUMERIC"}, [][]any{{"1.24"}, {"1.24"}})
		_, err = db.Exec("INSERT INTO numeric_control VALUES(-1)")
		numericWant := stuntgres.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "23514", Message: `new row for relation "numeric_control" violates check constraint "ck"`, Detail: "Failing row contains (-1.00).", Schema: "public", Table: "numeric_control", Constraint: "ck"}
		collationLegacyDiagnostic(t, err, numericWant)
		rows, err := db.Query("SELECT c,n FROM t ORDER BY id")
		if err != nil {
			t.Fatal(err)
		}
		collationLegacyRows(t, backend.PG, rows, []string{"c", "n"}, []string{"VARCHAR", "INT8"}, [][]any{{"a", int64(8)}, {"Z", int64(9)}, {"b", int64(7)}})
		collationLegacyRows(t, backend.PG, held, []string{"n"}, []string{"INT8"}, [][]any{{int64(8)}, {int64(9)}, {int64(7)}})
		fixture.UpdateReturningReferenceExec(t, db, "INSERT INTO t(id,c) VALUES(4,'ok')")
		var n int64
		if err = db.QueryRow("SELECT n FROM t WHERE id=4").Scan(&n); err != nil || n != 7 {
			t.Fatal(n, err)
		}
		_, err = db.Exec("INSERT INTO t VALUES(5,'x',NULL)")
		want := stuntgres.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "23502", Message: `null value in column "n" of relation "t" violates not-null constraint`, Detail: "Failing row contains (5, x, null).", Schema: "public", Table: "t", Column: "n"}
		collationLegacyDiagnostic(t, err, want)
		if !backend.PG {
			if err = stuntgres.Restore(ctx, p.Native, strings.NewReader("CREATE TABLE broken(")); err == nil {
				t.Fatal("accepted invalid Restore")
			}
		}
		rows, err = db.Query("SELECT n FROM t ORDER BY id")
		if err != nil {
			t.Fatal(err)
		}
		collationLegacyRows(t, backend.PG, rows, []string{"n"}, []string{"INT8"}, [][]any{{int64(8)}, {int64(9)}, {int64(7)}, {int64(7)}})
		fixture.UpdateReturningReferenceExec(t, db, "ALTER TABLE t ALTER c TYPE VARCHAR(2)")
		fixture.UpdateReturningReferenceExec(t, db, "ALTER TABLE t ALTER n SET DEFAULT 9")
		fixture.UpdateReturningReferenceExec(t, db, "INSERT INTO t(id,c) VALUES(5,'ok')")
		if err = db.QueryRow("SELECT n FROM t WHERE id=5").Scan(&n); err != nil || n != 9 {
			t.Fatal(n, err)
		}
		if !backend.PG {
			var final bytes.Buffer
			if err = stuntgres.Dump(ctx, p.Native, &final); err != nil || !strings.Contains(final.String(), "DEFAULT 9") || strings.Contains(final.String(), "ALTER TABLE") {
				t.Fatal(err, final.String())
			}
		}

	})
}
