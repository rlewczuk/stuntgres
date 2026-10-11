package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"reflect"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func TestReferenceUpdateCoreMigratedSmoke(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			ctx := p.Ctx
			db := backend.DB
			a, err := db.Conn(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer a.Close()
			b, err := db.Conn(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := b.Close(); err != nil && !(backend.PG && (errors.Is(err, driver.ErrBadConn) || errors.Is(err, sql.ErrConnDone))) {
					t.Error(err)
				}
			}()
			defer a.ExecContext(context.Background(), "ROLLBACK")
			exec := func(x fixture.Executor, q string, prepared bool, n int64, args ...any) {
				t.Helper()
				fixture.UpdateCoreExec(t, ctx, x, q, prepared, n, args...)
			}
			check := func(x fixture.Executor, want [][]int64) {
				t.Helper()
				rows, err := x.QueryContext(ctx, "SELECT id,a,b,n FROM t ORDER BY id")
				if err != nil {
					t.Fatal(err)
				}
				defer rows.Close()
				got := [][]int64{}
				for rows.Next() {
					var id, a, b, n int64
					if err := rows.Scan(&id, &a, &b, &n); err != nil {
						t.Fatal(err)
					}
					got = append(got, []int64{id, a, b, n})
				}
				if err := rows.Err(); err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(got, want) {
					t.Fatalf("got %v want %v", got, want)
				}
			}
			exec(b, "CREATE TABLE t(id INT,a INT,b INT,n SMALLINT NOT NULL)", false, 0)
			exec(b, "INSERT INTO t VALUES(1,10,20,1),(2,30,40,1),(3,30,40,1)", false, 3)
			saved, err := db.QueryContext(ctx, "SELECT a FROM t ORDER BY id")
			if err != nil {
				t.Fatal(err)
			}
			defer saved.Close()
			exec(b, "UPDATE t SET a=b,b=a", false, 3)
			check(b, [][]int64{{1, 20, 10, 1}, {2, 40, 30, 1}, {3, 40, 30, 1}})
			exec(b, "UPDATE t SET a=a", false, 3)
			exec(b, "UPDATE t SET a=9 WHERE id=99", true, 0)
			exec(b, "UPDATE t SET a=$1,b=a WHERE id=$2", true, 1, int64(40000), int64(3))
			want := [][]int64{{1, 20, 10, 1}, {2, 40, 30, 1}, {3, 40000, 40, 1}}
			_, err = b.ExecContext(ctx, "UPDATE t SET n=a")
			fixture.AssertDiagnostic(t, err, &fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22003", Message: "smallint out of range"})
			check(b, want)
			_, err = b.ExecContext(ctx, "UPDATE t SET n=NULL WHERE id=3")
			fixture.AssertDiagnostic(t, err, &fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "23502", Message: `null value in column "n" of relation "t" violates not-null constraint`, Detail: "Failing row contains (3, 40000, 40, null).", Schema: "public", Table: "t", Column: "n"})
			check(b, want)
			exec(a, "BEGIN", false, 0)
			exec(a, "UPDATE t SET a=7 WHERE id=1", true, 1)
			check(a, [][]int64{{1, 7, 10, 1}, {2, 40, 30, 1}, {3, 40000, 40, 1}})
			check(b, want)
			exec(a, "COMMIT", false, 0)
			want[0][1] = 7
			check(b, want)
			exec(a, "BEGIN", false, 0)
			exec(a, "UPDATE t SET a=8 WHERE id=1", false, 1)
			var tuple string
			if backend.PG {
				if err = b.QueryRowContext(ctx, "SELECT ctid::text FROM t WHERE id=1").Scan(&tuple); err != nil {
					t.Fatal(err)
				}
			}
			waitCtx, stop := context.WithTimeout(ctx, 25*time.Millisecond)
			_, err = b.ExecContext(waitCtx, "UPDATE t SET b=a WHERE id=1")
			stop()
			if backend.PG {
				fixture.AssertDiagnostic(t, err, &fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "57014", Message: "canceling statement due to user request", Context: "while updating tuple " + tuple + ` in relation "t"`})
			} else if !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("held-row cancellation: %v", err)
			}

			exec(a, "ROLLBACK", false, 0)
			if backend.PG {
				// lib/pq retires the physical connection after context cancellation.
				// Assert atomic committed heads through an independent borrower,
				// then continue the SQL scenario on a fresh connection.
				check(db, want)
				if err := b.Close(); err != nil && !errors.Is(err, driver.ErrBadConn) && !errors.Is(err, sql.ErrConnDone) {
					t.Fatal(err)
				}
				b, err = db.Conn(ctx)
				if err != nil {
					t.Fatal(err)
				}
			}
			check(b, want)
			exec(b, "UPDATE t SET b=a WHERE id=1", false, 1)
			want[0][2] = 7
			check(b, want)
			var dump bytes.Buffer
			if !backend.PG {
				if err := stuntgres.Dump(ctx, p.Native, &dump); err != nil {
					t.Fatal(err)
				}
				if err := stuntgres.Reset(ctx, p.Native); err != nil {
					t.Fatal(err)
				}
				if err := stuntgres.Restore(ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
					t.Fatal(err)
				}
			}
			check(b, want)
			old := []int64{}
			for saved.Next() {
				var v int64
				if err := saved.Scan(&v); err != nil {
					t.Fatal(err)
				}
				old = append(old, v)
			}
			if err := saved.Err(); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(old, []int64{10, 30, 30}) {
				t.Fatal("retained pre-update result", old)
			}
			if !backend.PG {
				var again bytes.Buffer
				if err := stuntgres.Dump(ctx, p.Native, &again); err != nil {
					t.Fatal(err)
				}
				if dump.String() != again.String() {
					t.Fatal("current-row dump changed after round trip")
				}
			}
			// INSERT's query source must consume the prior own UPDATE head, not its
			// predecessor, while peer visibility and rollback remain transaction-owned.
			exec(b, "CREATE TABLE consumed(id INT,a INT,b INT,n SMALLINT)", false, 0)
			exec(a, "BEGIN", false, 0)
			exec(a, "UPDATE t SET a=12 WHERE id=1", false, 1)
			exec(a, "INSERT INTO consumed SELECT id,a,b,n FROM t WHERE id=1", true, 1)
			var consumed int64
			if err := a.QueryRowContext(ctx, "SELECT a FROM consumed").Scan(&consumed); err != nil || consumed != 12 {
				t.Fatal(consumed, err)
			}
			if err := b.QueryRowContext(ctx, "SELECT a FROM consumed").Scan(&consumed); !errors.Is(err, sql.ErrNoRows) {
				t.Fatal("peer saw private INSERT query result", err)
			}
			exec(a, "ROLLBACK", false, 0)
			if err := b.QueryRowContext(ctx, "SELECT a FROM consumed").Scan(&consumed); !errors.Is(err, sql.ErrNoRows) {
				t.Fatal("rollback retained INSERT query result", err)
			}
			// Retained public composition of the integrated DATE/BYTEA and baseline
			// cast/null/truth/distinct surfaces, not a throwaway interoperability probe.
			exec(b, "CREATE TABLE composed(id INT,d DATE,b BYTEA,n INT NOT NULL,f BOOL)", false, 0)
			exec(b, `INSERT INTO composed VALUES(1,'2000-01-01','\x01',1,TRUE)`, false, 1)
			composed, err := b.PrepareContext(ctx, "UPDATE composed SET d=$1,b=$2,n=CAST(n AS BIGINT)::INTEGER,f=(b IS NULL) IS FALSE WHERE id=1 AND d IS DISTINCT FROM $1")
			if err != nil {
				t.Fatal(err)
			}
			defer composed.Close()
			for _, args := range [][]any{{"2024-02-29", []byte{0, 92, 255}}, {nil, nil}, {"2030-01-02", []byte{65}}} {
				r, err := composed.ExecContext(ctx, args...)
				if err != nil {
					t.Fatal(err)
				}
				n, err := r.RowsAffected()
				if err != nil || n != 1 {
					t.Fatal(n, err)
				}
			}
			var date string
			var blob []byte
			var number int64
			var flag bool
			if err = b.QueryRowContext(ctx, "SELECT d::TEXT,b,n,f FROM composed").Scan(&date, &blob, &number, &flag); err != nil || date != "2030-01-02" || !bytes.Equal(blob, []byte{65}) || number != 1 || flag {
				t.Fatal(date, blob, number, flag, err)
			}
			exec(b, "UPDATE composed SET d=d,b=b,n=CAST(n AS BIGINT)::INTEGER WHERE f IS FALSE", false, 1)
			failed, err := b.PrepareContext(ctx, "UPDATE composed SET b=$1,n=NULL WHERE id=1")
			if err != nil {
				t.Fatal(err)
			}
			defer failed.Close()
			for _, output := range []struct{ name, detail string }{{"hex", `Failing row contains (1, 2030-01-02, \x005cff, null, f).`}, {"escape", `Failing row contains (1, 2030-01-02, \000\\\377, null, f).`}} {
				exec(b, "SET bytea_output="+output.name, false, 0)
				for _, prepared := range []bool{false, true} {
					if prepared {
						_, err = failed.ExecContext(ctx, []byte{0, 92, 255})
					} else {
						_, err = b.ExecContext(ctx, "UPDATE composed SET b=$1,n=NULL WHERE id=1", []byte{0, 92, 255})
					}
					fixture.AssertDiagnostic(t, err, &fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "23502", Message: `null value in column "n" of relation "composed" violates not-null constraint`, Detail: output.detail, Schema: "public", Table: "composed", Column: "n"})
				}
			}
			if !backend.PG {
				var compositionDump bytes.Buffer
				if err = stuntgres.Dump(ctx, p.Native, &compositionDump); err != nil {
					t.Fatal(err)
				}
				if err = stuntgres.Reset(ctx, p.Native); err != nil {
					t.Fatal(err)
				}
				if err = stuntgres.Restore(ctx, p.Native, bytes.NewReader(compositionDump.Bytes())); err != nil {
					t.Fatal(err)
				}
			}
			if err = b.QueryRowContext(ctx, "SELECT d::TEXT,b,n,f FROM composed").Scan(&date, &blob, &number, &flag); err != nil || date != "2030-01-02" || !bytes.Equal(blob, []byte{65}) || number != 1 || flag {
				t.Fatal("composition roundtrip", date, blob, number, flag, err)
			}
			// One canonical own-head/live-source path through UPDATE, self INSERT SELECT
			// and DELETE, with independent peer visibility and no resurrection.
			exec(b, "CREATE TABLE mixed(id INT,v INT)", false, 0)
			exec(b, "INSERT INTO mixed VALUES(1,10),(2,20)", false, 2)
			mixedOld, err := db.QueryContext(ctx, "SELECT id,v FROM mixed ORDER BY id")
			if err != nil {
				t.Fatal(err)
			}
			defer mixedOld.Close()
			mixedRows := func(x fixture.Executor, want [][]int64) {
				t.Helper()
				rows, err := x.QueryContext(ctx, "SELECT id,v FROM mixed ORDER BY id")
				if err != nil {
					t.Fatal(err)
				}
				defer rows.Close()
				got := [][]int64{}
				for rows.Next() {
					var id, v int64
					if err := rows.Scan(&id, &v); err != nil {
						t.Fatal(err)
					}
					got = append(got, []int64{id, v})
				}
				if err := rows.Err(); err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(got, want) {
					t.Fatal(got, want)
				}
			}
			exec(a, "BEGIN", false, 0)
			exec(a, "UPDATE mixed SET v=30 WHERE id=1", false, 1)
			exec(a, "INSERT INTO mixed SELECT id,v FROM mixed WHERE id=1", true, 1)
			exec(a, "DELETE FROM mixed WHERE id=1", true, 2)
			mixedRows(a, [][]int64{{2, 20}})
			mixedRows(b, [][]int64{{1, 10}, {2, 20}})
			exec(a, "UPDATE mixed SET v=40 WHERE id=1", true, 0)
			exec(a, "ROLLBACK", false, 0)
			mixedRows(b, [][]int64{{1, 10}, {2, 20}})
			exec(a, "BEGIN", false, 0)
			exec(a, "UPDATE mixed SET v=30 WHERE id=1", false, 1)
			exec(a, "DELETE FROM mixed WHERE id=1", true, 1)
			exec(a, "COMMIT", false, 0)
			mixedRows(b, [][]int64{{2, 20}})
			if !backend.PG {
				var mixedDump bytes.Buffer
				if err = stuntgres.Dump(ctx, p.Native, &mixedDump); err != nil {
					t.Fatal(err)
				}
				if err = stuntgres.Reset(ctx, p.Native); err != nil {
					t.Fatal(err)
				}
				if err = stuntgres.Restore(ctx, p.Native, bytes.NewReader(mixedDump.Bytes())); err != nil {
					t.Fatal(err)
				}
			}
			mixedRows(b, [][]int64{{2, 20}})
			oldMixed := [][]int64{}
			for mixedOld.Next() {
				var id, v int64
				if err := mixedOld.Scan(&id, &v); err != nil {
					t.Fatal(err)
				}
				oldMixed = append(oldMixed, []int64{id, v})
			}
			if err = mixedOld.Err(); err != nil || !reflect.DeepEqual(oldMixed, [][]int64{{1, 10}, {2, 20}}) {
				t.Fatal("retained mixed source", oldMixed, err)
			}
			// Consume the incumbent integer arithmetic provider through UPDATE, with
			// simultaneous old-row RHSs and a retained parameterized statement.
			exec(b, "CREATE TABLE arithmetic_update(id INT,a INT,b INT)", false, 0)
			exec(b, "INSERT INTO arithmetic_update VALUES(1,7,2),(1,7,2),(2,NULL,4)", false, 3)
			arithmetic, err := b.PrepareContext(ctx, "UPDATE arithmetic_update SET a=a+b,b=a-b WHERE id+$1=$2")
			if err != nil {
				t.Fatal(err)
			}
			defer arithmetic.Close()
			for _, frame := range []struct {
				arg any
				n   int64
			}{
				{1, 2},
				{nil, 0},
				{1, 2},
			} {
				r, err := arithmetic.ExecContext(ctx, frame.arg, 2)
				if err != nil {
					t.Fatal(err)
				}
				if n, err := r.RowsAffected(); err != nil || n != frame.n {
					t.Fatal(n, err)
				}
			}
			exec(b, "UPDATE arithmetic_update SET a=a+b,b=a-b WHERE id+0=2", false, 1)
			arithmeticRows, err := b.QueryContext(ctx, "SELECT id,a,b FROM arithmetic_update ORDER BY id")
			if err != nil {
				t.Fatal(err)
			}
			defer arithmeticRows.Close()
			arithmeticBag := [][]any{}
			for arithmeticRows.Next() {
				var id, a, b any
				if err := arithmeticRows.Scan(&id, &a, &b); err != nil {
					t.Fatal(err)
				}
				arithmeticBag = append(arithmeticBag, []any{id, a, b})
			}
			if err := arithmeticRows.Err(); err != nil || !reflect.DeepEqual(arithmeticBag, [][]any{{int64(1), int64(14), int64(4)}, {int64(1), int64(14), int64(4)}, {int64(2), nil, nil}}) {
				t.Fatal("arithmetic UPDATE simultaneous/null bag", arithmeticBag, err)
			}
			exec(b, "CREATE TABLE u(f BOOL)", false, 0)
			exec(b, "INSERT INTO u VALUES(TRUE),(FALSE)", false, 2)
			for _, tc := range []struct {
				q, position string
			}{
				{"UPDATE u SET f=1+2", "16"},
				{"/*é*/ UPDATE u SET f=(1+2)", "23"},
			} {
				want := fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42804", Message: `column "f" is of type boolean but expression is of type integer`, Hint: "You will need to rewrite or cast the expression.", Position: tc.position}
				result, err := b.ExecContext(ctx, tc.q)
				if result != nil {
					t.Fatal("contextual mismatch returned a result")
				}
				fixture.AssertDiagnostic(t, err, &want)
				stmt, err := b.PrepareContext(ctx, tc.q)
				if stmt != nil {
					stmt.Close()
					t.Fatal("contextual mismatch prepared")
				}
				fixture.AssertDiagnostic(t, err, &want)
				rows, err := b.QueryContext(ctx, "SELECT f FROM u ORDER BY f")
				if err != nil {
					t.Fatal(err)
				}
				flags := []bool{}
				for rows.Next() {
					var flag bool
					if err := rows.Scan(&flag); err != nil {
						rows.Close()
						t.Fatal(err)
					}
					flags = append(flags, flag)
				}
				rowErr := rows.Err()
				closeErr := rows.Close()
				if rowErr != nil || closeErr != nil || !reflect.DeepEqual(flags, []bool{false, true}) {
					t.Fatal("contextual mismatch changed prior values", flags, rowErr, closeErr)
				}
			}
		})
	}
}
