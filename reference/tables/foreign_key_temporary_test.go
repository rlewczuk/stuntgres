package reference_test

import (
	"bytes"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceForeignKeyNativeControlsTemporary(t *testing.T) {
	foreignKeyScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, run func(fixture.Step)) {
		conn, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		step := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, b.Recorder, s) }
		for _, q := range []string{"CREATE TABLE persistent(id INT PRIMARY KEY)", "CREATE TEMP TABLE tp(id INT PRIMARY KEY)", "CREATE TEMP TABLE tc(pid INT CONSTRAINT retained_fk REFERENCES tp)"} {
			step(fixture.Exec(q, 0))
		}
		step(fixture.Exec("INSERT INTO tp VALUES(1)", 1))
		step(fixture.Exec("INSERT INTO tc VALUES(1)", 1))
		step(fixture.Exec("INSERT INTO persistent VALUES(7)", 1))
		ids := make(map[string]int64)
		for _, q := range []string{"SELECT 'tp'::regclass::oid", "SELECT 'tc'::regclass::oid", "SELECT oid::bigint FROM pg_constraint WHERE conname='retained_fk'"} {
			var id int64
			if err := conn.QueryRowContext(p.Ctx, q).Scan(&id); err != nil || id <= 0 {
				t.Fatal(q, id, err)
			}
			ids[q] = id
		}
		stmt, err := conn.PrepareContext(p.Ctx, "INSERT INTO tc VALUES($1)")
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held, err := conn.QueryContext(p.Ctx, "SELECT pid FROM tc")
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		if !b.PG {
			var dump bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
				t.Fatal(err)
			}
			for _, replace := range []func() error{func() error { return stuntgres.Reset(p.Ctx, p.Native) }, func() error { return stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())) }} {
				if err := replace(); err != nil {
					t.Fatal(err)
				}
				for q, id := range ids {
					var got int64
					if err := conn.QueryRowContext(p.Ctx, q).Scan(&got); err != nil || got != id {
						t.Fatal("retained TEMP FK provenance changed", q, id, got, err)
					}
				}
			}
			var imported bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &imported); err != nil || !bytes.Equal(imported.Bytes(), dump.Bytes()) {
				t.Fatal("TEMP appeared in persistent dump", err, imported.String())
			}
		}
		columns, err := held.Columns()
		if err != nil || len(columns) != 1 || columns[0] != "pid" {
			t.Fatal("retained TEMP columns", columns, err)
		}
		types, err := held.ColumnTypes()
		if err != nil || len(types) != 1 || types[0].DatabaseTypeName() != "INT4" {
			t.Fatal("retained TEMP metadata", types, err)
		}
		fixture.TransactionRowValues(t, held, 1)
		held.Close()
		r, err := stmt.ExecContext(p.Ctx, 1)
		if err != nil {
			t.Fatal(err)
		}
		fixture.LockAffected(t, r, 1)
		_, err = stmt.ExecContext(p.Ctx, 9)
		d := fixture.ExtractDiagnostic(t, err)
		d.Schema = "public"
		want := foreignKeyChild("tc", "retained_fk", `Key (pid)=(9) is not present in table "tp".`)
		if d != want {
			t.Fatal(d, want)
		}
		step(foreignKeyIntRows("SELECT pid FROM tc", "pid", 1, 1))
		step(fixture.Exec("DROP TABLE tc,tp", 0))
	})
}
