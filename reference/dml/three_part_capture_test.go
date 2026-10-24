package reference_test

import (
	"bytes"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func threePartStoredCapture(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "qa")
	p := fixture.NewScenario(t)
	var dump bytes.Buffer
	for _, b := range p.Backends() {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
		for _, q := range []string{
			"CREATE SCHEMA qa", "CREATE TABLE qa.capture(v int)", "INSERT INTO qa.capture VALUES(10)",
			"CREATE VIEW qa.captured AS SELECT qa.capture.v FROM qa.capture",
			"CREATE FUNCTION qa.captured_value() RETURNS int LANGUAGE SQL BEGIN ATOMIC SELECT qa.capture.v FROM qa.capture; END",
			"ALTER TABLE qa.capture RENAME COLUMN v TO renamed", "ALTER TABLE qa.capture RENAME TO renamed_table",
		} {
			if _, err := b.DB.ExecContext(p.Ctx, q); err != nil {
				t.Fatal(err)
			}
		}
		check := func() {
			run(fixture.Select("SELECT v FROM qa.captured", []string{"v"}, []string{"INT4"}, [][]any{{int64(10)}}))
			run(fixture.Select("SELECT qa.captured_value() AS v", []string{"v"}, []string{"INT4"}, [][]any{{int64(10)}}))
		}
		check()
		conn, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		tx, err := conn.BeginTx(p.Ctx, nil)
		if err != nil {
			conn.Close()
			t.Fatal(err)
		}
		fixture.RunStep(t, p.Ctx, tx, b.Recorder, fixture.Exec("DECLARE three_cursor CURSOR FOR SELECT qa.renamed_table.renamed FROM qa.renamed_table", 0))
		fixture.RunStep(t, p.Ctx, tx, b.Recorder, fixture.Select("FETCH ALL FROM three_cursor", []string{"renamed"}, []string{"INT4"}, [][]any{{int64(10)}}))
		if err := tx.Rollback(); err != nil {
			conn.Close()
			t.Fatal(err)
		}
		conn.Close()
		if !b.PG {
			if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
			check()
		} else if dump.Len() > 0 {
			if _, err := b.DB.ExecContext(p.Ctx, "DROP SCHEMA qa CASCADE"); err != nil {
				t.Fatal(err)
			}
			if _, err := b.DB.ExecContext(p.Ctx, dump.String()); err != nil {
				t.Fatal(err)
			}
			check()
		}
	}
}

func threePartStoredPolicy(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"three_policy_actor"})
	threePartDML(t, func(p *fixture.Pair, b fixture.Backend) {
		threePartSeed(t, p, b)
		for _, q := range []string{
			"CREATE ROLE three_policy_actor", "GRANT USAGE ON SCHEMA qa TO three_policy_actor",
			"GRANT SELECT ON qa.items TO three_policy_actor", "ALTER TABLE qa.items ENABLE ROW LEVEL SECURITY",
			"CREATE POLICY three_policy ON qa.items USING(qa.items.id=1)",
		} {
			if _, err := b.DB.ExecContext(p.Ctx, q); err != nil {
				t.Fatalf("setup %q failed: %+v", q, fixture.ExtractDiagnostic(t, err))
			}
		}
		conn, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		if _, err := conn.ExecContext(p.Ctx, "SET SESSION AUTHORIZATION three_policy_actor"); err != nil {
			t.Fatal(err)
		}
		defer conn.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
		for _, prepared := range []bool{false, true} {
			s := fixture.Select("SELECT qa.items.id,qa.items.v FROM qa.items ORDER BY qa.items.id", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(10)}})
			s.Prepared = prepared
			fixture.RunStep(t, p.Ctx, conn, b.Recorder, s)
		}
	})
}
