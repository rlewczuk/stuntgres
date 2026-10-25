package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func uuidOsspSaved(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder) []string {
	t.Helper()
	rows, err := c.QueryContext(p.Ctx, "SELECT id,u FROM uuid_ossp_saved ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	cols, err := rows.ColumnTypes()
	if err != nil || len(cols) != 2 || cols[1].DatabaseTypeName() != "UUID" {
		t.Fatal("saved metadata", cols, err)
	}
	values := []string{}
	for rows.Next() {
		var id int
		var value string
		if err := rows.Scan(&id, &value); err != nil {
			t.Fatal(err)
		}
		if id != len(values)+1 {
			t.Fatalf("id=%d", id)
		}
		uuidOsspDecode(t, value)
		values = append(values, value)
	}
	if rows.Err() != nil {
		t.Fatal(rows.Err())
	}
	if len(values) < 2 || values[0] == values[1] {
		t.Fatal("defaults not fresh", values)
	}
	return values
}

func uuidOsspConsumers(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool) []string {
	t.Helper()
	run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
	run(fixture.Exec("CREATE SCHEMA uuid_ossp_part", 0))
	run(fixture.Exec("CREATE EXTENSION \"uuid-ossp\" SCHEMA uuid_ossp_part", 0))
	run(fixture.Exec("CREATE TABLE uuid_ossp_saved(id integer,u uuid DEFAULT uuid_ossp_part.uuid_generate_v4())", 0))
	run(fixture.Exec("INSERT INTO uuid_ossp_saved(id) VALUES(1),(2)", 2))
	run(fixture.Exec("CREATE VIEW uuid_ossp_view AS SELECT id,uuid_ossp_part.uuid_generate_v4() AS u FROM uuid_ossp_saved", 0))
	run(fixture.Exec("CREATE VIEW uuid_ossp_nested AS SELECT id,(SELECT uuid_ossp_part.uuid_generate_v4()) AS u FROM uuid_ossp_saved", 0))
	saved := uuidOsspSaved(t, p, c, r)
	for _, q := range []string{"SELECT u FROM uuid_ossp_view ORDER BY id", "SELECT u FROM uuid_ossp_nested ORDER BY id"} {
		a := uuidOsspRead(t, p, c, r, pg, q, 2, 1, true)
		s, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		b := uuidOsspRead(t, p, fixture.HeldStatement{Executor: c, Statement: s, SQL: q}, r, pg, q, 2, 1, true)
		s.Close()
		if a[0] == b[0] {
			t.Fatal("view cached volatile result")
		}
	}
	run(fixture.ImplicitError("CREATE TABLE uuid_ossp_generated(u uuid GENERATED ALWAYS AS (uuid_ossp_part.uuid_generate_v4()) STORED)", fixture.Diagnostic{Code: "42P17", Message: "generation expression is not immutable"}))
	run(fixture.ImplicitError("CREATE INDEX uuid_ossp_volatile_idx ON uuid_ossp_saved ((uuid_ossp_part.uuid_generate_v4()))", fixture.Diagnostic{Code: "42P17", Message: "functions in index expression must be marked IMMUTABLE"}))
	run(fixture.Select("SELECT relname FROM pg_class WHERE relname IN ('uuid_ossp_generated','uuid_ossp_volatile_idx')", []string{"relname"}, []string{"NAME"}, [][]any{}))
	return saved
}

func TestReferenceUUIDOsspConsumers(t *testing.T) {
	uuidOsspCase(t, false, func(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool) {
		uuidOsspConsumers(t, p, c, r, pg)
	})
}
