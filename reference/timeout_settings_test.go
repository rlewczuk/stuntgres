package reference_test

import (
	"testing"
)

func TestReferenceTimeoutSettings(t *testing.T) {
	timeoutReferenceScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
		f := newTimeoutReferenceFixture(t, p, b)
		definitions := []struct{ name, description string }{
			{"statement_timeout", "Sets the maximum allowed duration of any statement."},
			{"transaction_timeout", "Sets the maximum allowed duration of any transaction within a session (not a prepared transaction)."},
			{"lock_timeout", "Sets the maximum allowed duration of any wait for a lock."},
			{"idle_in_transaction_session_timeout", "Sets the maximum allowed idle time between queries, when in a transaction."},
			{"idle_session_timeout", "Sets the maximum allowed idle time between queries, when not in a transaction."},
		}
		rows, err := f.owner.QueryContext(p.ctx, "SHOW ALL")
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		cols, err := rows.Columns()
		if err != nil || len(cols) != 3 || cols[0] != "name" || cols[1] != "setting" || cols[2] != "description" {
			t.Fatal(cols, err)
		}
		types, err := rows.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		for _, typ := range types {
			if typ.DatabaseTypeName() != "TEXT" {
				t.Fatal(typ.DatabaseTypeName())
			}
		}
		found := map[string][2]string{}
		for rows.Next() {
			var name, value, description string
			if err := rows.Scan(&name, &value, &description); err != nil {
				t.Fatal(err)
			}
			if _, duplicate := found[name]; duplicate {
				t.Fatal("duplicate setting", name)
			}
			found[name] = [2]string{value, description}
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		if err := rows.Close(); err != nil {
			t.Fatal(err)
		}
		for _, def := range definitions {
			if got := found[def.name]; got != [2]string{"0", def.description} {
				t.Fatalf("%s = %#v", def.name, got)
			}
			for _, prepared := range []bool{false, true} {
				step := referenceExec("SET "+def.name+"='1.5min'", 0)
				if prepared {
					step = referencePrepared(step)
				}
				referenceRunStep(t, p.ctx, f.owner, b.recorder, step)
				f.show(def.name, "90s")
				referenceRunStep(t, p.ctx, f.owner, b.recorder, referencePrepared(localReferenceSelect(b.pg, "SELECT current_setting('"+def.name+"')", "current_setting", "90s")))
				referenceRunStep(t, p.ctx, f.owner, b.recorder, localReferenceSelect(b.pg, "SELECT set_config('"+def.name+"','120000',false)", "set_config", "2min"))
				f.show(def.name, "2min")
				f.exec(f.owner, "RESET "+def.name, 0)
				f.show(def.name, "0")
			}
			f.set(def.name, "0.5ms")
			f.show(def.name, "0")
			f.set(def.name, "2147483647ms")
			f.show(def.name, "2147483647ms")
			f.exec(f.owner, "RESET "+def.name, 0)
			for _, tc := range []struct{ value, message, hint string }{
				{"-1", "-1 ms is outside the valid range for parameter \"" + def.name + "\" (0 ms .. 2147483647 ms)", ""},
				{"2147483648", "invalid value for parameter \"" + def.name + "\": \"2147483648\"", "Value exceeds integer range."},
				{"1 MS", "invalid value for parameter \"" + def.name + "\": \"1 MS\"", "Valid units for this parameter are \"us\", \"ms\", \"s\", \"min\", \"h\", and \"d\"."},
			} {
				_, err := f.owner.ExecContext(p.ctx, "SET "+def.name+"='"+tc.value+"'")
				want := controlsReferenceDiagnostic("22023", tc.message)
				want.Hint = tc.hint
				referenceAssertDiagnostic(t, err, &want)
				f.show(def.name, "0")
			}
			f.exec(f.owner, "BEGIN", 0)
			f.exec(f.owner, "SET SESSION "+def.name+"='90s'", 0)
			f.exec(f.owner, "SAVEPOINT timeout_setting", 0)
			f.exec(f.owner, "SET LOCAL "+def.name+"='2min'", 0)
			f.show(def.name, "2min")
			f.exec(f.owner, "ROLLBACK TO timeout_setting", 0)
			f.show(def.name, "90s")
			f.exec(f.owner, "COMMIT", 0)
			f.show(def.name, "90s")
			f.exec(f.owner, "BEGIN", 0)
			f.exec(f.owner, "SET SESSION "+def.name+"='2min'", 0)
			f.exec(f.owner, "ROLLBACK", 0)
			f.show(def.name, "90s")
			f.exec(f.owner, "RESET ALL", 0)
			f.show(def.name, "0")
			referenceRunStep(t, p.ctx, f.peer, b.recorder, localReferenceSelect(b.pg, "SHOW "+def.name, def.name, "0"))
		}
		f.rows(f.peer, 7)
	})
}
