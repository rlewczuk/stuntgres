package reference_test

import (
	"context"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceTransactionIsolationModes(t *testing.T) {
	pair := fixture.NewScenario(t)
	for _, backend := range pair.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			f := fixture.NewControlsReferenceFixture(t, pair, backend.PG)
			ctx := pair.Ctx
			held, err := f.A.PrepareContext(ctx, "SET TRANSACTION ISOLATION LEVEL REPEATABLE READ READ ONLY")
			if err != nil {
				t.Fatal(err)
			}
			defer held.Close()
			f.NoticesWant(t, "", "")
			result, err := held.ExecContext(ctx)
			if err != nil {
				t.Fatal(err)
			}
			count, err := result.RowsAffected()
			if err != nil || count != 0 {
				t.Fatal(count, err)
			}
			f.NoticesWant(t, "25P01", "SET TRANSACTION can only be used in transaction blocks")
			f.Exec(t, f.A, "BEGIN", false, 0)
			referenceIsolationShow(t, f.A, "transaction_isolation", "read committed")
			referenceIsolationShow(t, f.A, "transaction_read_only", "off")
			f.Exec(t, f.A, "SHOW application_name", false, 0)
			f.Exec(t, f.A, "RESET application_name", false, 0)
			f.Exec(t, f.A, "SET TRANSACTION ISOLATION LEVEL READ UNCOMMITTED READ ONLY, READ WRITE, ISOLATION LEVEL REPEATABLE READ", true, 0)
			referenceIsolationShow(t, f.A, "transaction_isolation", "repeatable read")
			referenceIsolationShow(t, f.A, "transaction_read_only", "off")
			f.Exec(t, f.A, "ROLLBACK", false, 0)
			_, err = f.A.ExecContext(ctx, "BEGIN READ ONLY junk")
			fixture.State(t, err, "42601")
			f.Exec(t, f.A, "BEGIN READ ONLY,READ WRITE ISOLATION LEVEL READ UNCOMMITTED", false, 0)
			referenceIsolationShow(t, f.A, "transaction_isolation", "read uncommitted")
			referenceIsolationShow(t, f.A, "transaction_read_only", "off")
			f.Exec(t, f.A, "ROLLBACK", false, 0)
		})
	}
}
func TestReferenceTransactionIsolationReadOnlyAnalysisPrecedence(t *testing.T) {
	for _, postgres := range []bool{false, true} {
		if postgres && !*fixture.Mode {
			continue
		}
		for _, query := range []string{"UPDATE t SET missing=2", "INSERT INTO missing VALUES(2)", "DELETE FROM t WHERE missing=2"} {
			t.Run(map[bool]string{false: "memory/", true: "postgres/"}[postgres]+query, func(t *testing.T) {
				_, a, b := fixture.IsolationReferenceConnections(t, fixture.NewScenario(t), postgres)
				ctx := context.Background()
				fixture.IsolationReferenceAffected(t, ctx, b, "CREATE TABLE t(v INT)", false, 0)
				fixture.IsolationReferenceAffected(t, ctx, a, "BEGIN READ ONLY", false, 0)
				_, err := a.ExecContext(ctx, query)
				want := fixture.IsolationReferenceWant("42703", `column "missing" of relation "t" does not exist`)
				if query == "UPDATE t SET missing=2" {
					want.Position = "14"
				}
				if query == "INSERT INTO missing VALUES(2)" {
					want = fixture.IsolationReferenceWant("42P01", `relation "missing" does not exist`)
					want.Position = "13"
				}
				if query == "DELETE FROM t WHERE missing=2" {
					want = fixture.IsolationReferenceWant("42703", `column "missing" does not exist`)
					want.Position = "21"
				}
				fixture.AssertDiagnostic(t, err, &want)
				fixture.IsolationReferenceAffected(t, ctx, a, "ROLLBACK", false, 0)
			})
		}
	}
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name+"/implicit_and_lock", func(t *testing.T) {
			a, b := fixture.LockConnections(t, p.Ctx, backend.DB)
			fixture.LockExec(t, p.Ctx, b, "CREATE TABLE t(v INT)", 0)
			fixture.LockExec(t, p.Ctx, a, "SET default_transaction_read_only=on", 0)
			for _, tc := range []struct{ query, tag string }{{"INSERT INTO t VALUES(1)", "INSERT"}, {"CREATE TABLE forbidden(v INT)", "CREATE TABLE"}} {
				s := fixture.ErrorDiagnostic(tc.query, fixture.ControlsReferenceDiagnostic("25006", "cannot execute "+tc.tag+" in a read-only transaction"))
				fixture.RunStep(t, p.Ctx, a, backend.Recorder, s)
			}
			fixture.LockExec(t, p.Ctx, a, "BEGIN READ ONLY", 0)
			fixture.RunStep(t, p.Ctx, a, backend.Recorder, fixture.Step{Query: "LOCK TABLE t IN ACCESS EXCLUSIVE MODE", Prepared: true, Affected: 0})
			fixture.LockExec(t, p.Ctx, a, "ROLLBACK", 0)
			fixture.IsolationReferenceSelect(t, p.Ctx, b, backend.PG, "SELECT v FROM t", false)
		})
	}
}

func TestReferenceTransactionIsolationOwnerSettingReads(t *testing.T) {
	for _, postgres := range []bool{false, true} {
		if postgres && !*fixture.Mode {
			continue
		}
		t.Run(map[bool]string{false: "memory", true: "postgres"}[postgres], func(t *testing.T) {
			_, a, _ := fixture.IsolationReferenceConnections(t, fixture.NewScenario(t), postgres)
			ctx := context.Background()
			fixture.IsolationReferenceAffected(t, ctx, a, "SET default_transaction_read_only TO on", false, 0)
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
			fixture.IsolationReferenceAffected(t, ctx, a, "BEGIN ISOLATION LEVEL REPEATABLE READ READ ONLY", false, 0)
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
			fixture.IsolationReferenceAffected(t, ctx, a, "ROLLBACK", false, 0)
		})
	}
}
