package reference_test

import (
	"context"
	"testing"
)

func TestReferenceTransactionIsolationModes(t *testing.T) {
	pair := newReferenceScenario(t)
	for _, backend := range pair.backends() {
		t.Run(backend.name, func(t *testing.T) {
			f := newControlsReferenceFixture(t, pair, backend.pg)
			ctx := pair.ctx
			held, err := f.a.PrepareContext(ctx, "SET TRANSACTION ISOLATION LEVEL REPEATABLE READ READ ONLY")
			if err != nil {
				t.Fatal(err)
			}
			defer held.Close()
			f.noticesWant(t, "", "")
			result, err := held.ExecContext(ctx)
			if err != nil {
				t.Fatal(err)
			}
			count, err := result.RowsAffected()
			if err != nil || count != 0 {
				t.Fatal(count, err)
			}
			f.noticesWant(t, "25P01", "SET TRANSACTION can only be used in transaction blocks")
			f.exec(t, f.a, "BEGIN", false, 0)
			referenceIsolationShow(t, f.a, "transaction_isolation", "read committed")
			referenceIsolationShow(t, f.a, "transaction_read_only", "off")
			f.exec(t, f.a, "SHOW application_name", false, 0)
			f.exec(t, f.a, "RESET application_name", false, 0)
			f.exec(t, f.a, "SET TRANSACTION ISOLATION LEVEL READ UNCOMMITTED READ ONLY, READ WRITE, ISOLATION LEVEL REPEATABLE READ", true, 0)
			referenceIsolationShow(t, f.a, "transaction_isolation", "repeatable read")
			referenceIsolationShow(t, f.a, "transaction_read_only", "off")
			f.exec(t, f.a, "ROLLBACK", false, 0)
			_, err = f.a.ExecContext(ctx, "BEGIN READ ONLY junk")
			referenceState(t, err, "42601")
			f.exec(t, f.a, "BEGIN READ ONLY,READ WRITE ISOLATION LEVEL READ UNCOMMITTED", false, 0)
			referenceIsolationShow(t, f.a, "transaction_isolation", "read uncommitted")
			referenceIsolationShow(t, f.a, "transaction_read_only", "off")
			f.exec(t, f.a, "ROLLBACK", false, 0)
		})
	}
}
func TestReferenceTransactionIsolationReadOnlyAnalysisPrecedence(t *testing.T) {
	for _, postgres := range []bool{false, true} {
		if postgres && !*referenceMode {
			continue
		}
		for _, query := range []string{"UPDATE t SET missing=2", "INSERT INTO missing VALUES(2)", "DELETE FROM t WHERE missing=2"} {
			t.Run(map[bool]string{false: "memory/", true: "postgres/"}[postgres]+query, func(t *testing.T) {
				_, a, b := isolationReferenceConnections(t, newReferenceScenario(t), postgres)
				ctx := context.Background()
				isolationReferenceAffected(t, ctx, b, "CREATE TABLE t(v INT)", false, 0)
				isolationReferenceAffected(t, ctx, a, "BEGIN READ ONLY", false, 0)
				_, err := a.ExecContext(ctx, query)
				want := isolationReferenceWant("42703", `column "missing" of relation "t" does not exist`)
				if query == "UPDATE t SET missing=2" {
					want.Position = "14"
				}
				if query == "INSERT INTO missing VALUES(2)" {
					want = isolationReferenceWant("42P01", `relation "missing" does not exist`)
					want.Position = "13"
				}
				if query == "DELETE FROM t WHERE missing=2" {
					want = isolationReferenceWant("42703", `column "missing" does not exist`)
					want.Position = "21"
				}
				referenceAssertDiagnostic(t, err, &want)
				isolationReferenceAffected(t, ctx, a, "ROLLBACK", false, 0)
			})
		}
	}
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name+"/implicit_and_lock", func(t *testing.T) {
			a, b := referenceLockConnections(t, p.ctx, backend.db)
			referenceLockExec(t, p.ctx, b, "CREATE TABLE t(v INT)", 0)
			referenceLockExec(t, p.ctx, a, "SET default_transaction_read_only=on", 0)
			for _, tc := range []struct{ query, tag string }{{"INSERT INTO t VALUES(1)", "INSERT"}, {"CREATE TABLE forbidden(v INT)", "CREATE TABLE"}} {
				s := referenceErrorDiagnostic(tc.query, controlsReferenceDiagnostic("25006", "cannot execute "+tc.tag+" in a read-only transaction"))
				referenceRunStep(t, p.ctx, a, backend.recorder, s)
			}
			referenceLockExec(t, p.ctx, a, "BEGIN READ ONLY", 0)
			referenceRunStep(t, p.ctx, a, backend.recorder, referenceStep{query: "LOCK TABLE t IN ACCESS EXCLUSIVE MODE", prepared: true, affected: 0})
			referenceLockExec(t, p.ctx, a, "ROLLBACK", 0)
			isolationReferenceSelect(t, p.ctx, b, backend.pg, "SELECT v FROM t", false)
		})
	}
}

func TestReferenceTransactionIsolationOwnerSettingReads(t *testing.T) {
	for _, postgres := range []bool{false, true} {
		if postgres && !*referenceMode {
			continue
		}
		t.Run(map[bool]string{false: "memory", true: "postgres"}[postgres], func(t *testing.T) {
			_, a, _ := isolationReferenceConnections(t, newReferenceScenario(t), postgres)
			ctx := context.Background()
			isolationReferenceAffected(t, ctx, a, "SET default_transaction_read_only TO on", false, 0)
			tx, err := a.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			var got string
			if err := tx.QueryRowContext(ctx, "SHOW transaction_read_only").Scan(&got); err != nil || got != "off" {
				t.Fatal(got, err)
			}
			if err := tx.Rollback(); err != nil {
				t.Fatal(err)
			}
			isolationReferenceAffected(t, ctx, a, "BEGIN ISOLATION LEVEL REPEATABLE READ READ ONLY", false, 0)
			for name, want := range map[string]string{"transaction_isolation": "repeatable read", "transaction_read_only": "on", "default_transaction_isolation": "read committed", "default_transaction_read_only": "on"} {
				if err := a.QueryRowContext(ctx, "SELECT current_setting('"+name+"')").Scan(&got); err != nil || got != want {
					t.Fatal(name, got, err)
				}
			}
			rows, err := a.QueryContext(ctx, "SHOW ALL")
			if err != nil {
				t.Fatal(err)
			}
			seen := map[string]string{}
			for rows.Next() {
				var name, value, description string
				if err := rows.Scan(&name, &value, &description); err != nil {
					t.Fatal(err)
				}
				seen[name] = value
			}
			if err := rows.Err(); err != nil {
				t.Fatal(err)
			}
			rows.Close()
			if seen["transaction_isolation"] != "repeatable read" || seen["transaction_read_only"] != "on" {
				t.Fatal(seen)
			}
			isolationReferenceAffected(t, ctx, a, "ROLLBACK", false, 0)
		})
	}
}
