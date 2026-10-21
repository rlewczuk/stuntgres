package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func uuidOsspTimeSaved(t *testing.T, p *fixture.Pair, c *sql.Conn, name string) []string {
	t.Helper()
	rows, err := c.QueryContext(p.Ctx, "SELECT id,u FROM "+name+"_saved ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	cols, err := rows.ColumnTypes()
	if err != nil || len(cols) != 2 || cols[1].DatabaseTypeName() != "UUID" {
		t.Fatal("saved metadata", cols, err)
	}
	var saved []string
	seen := map[string]bool{}
	for rows.Next() {
		var id int
		var value string
		if err := rows.Scan(&id, &value); err != nil {
			t.Fatal(err)
		}
		if id != len(saved)+1 {
			t.Fatal("saved id", id)
		}
		uuidOsspTimeDecode(t, value, name == "uuid_generate_v1mc")
		uuidOsspTimeUnique(t, seen, []string{value})
		saved = append(saved, value)
	}
	if rows.Err() != nil || len(saved) < 2 {
		t.Fatal("saved rows", saved, rows.Err())
	}
	return saved
}

func uuidOsspTimeConsumers(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool) map[string][]string {
	t.Helper()
	run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
	run(fixture.Exec("CREATE SCHEMA uuid_ossp_part", 0))
	run(fixture.Exec("CREATE EXTENSION \"uuid-ossp\" SCHEMA uuid_ossp_part", 0))
	saved := map[string][]string{}
	for _, name := range uuidOsspTimeNames {
		call := "uuid_ossp_part." + name + "()"
		run(fixture.Exec("CREATE TABLE "+name+"_saved(id integer,u uuid DEFAULT "+call+")", 0))
		run(fixture.Exec("INSERT INTO "+name+"_saved(id) VALUES(1),(2)", 2))
		saved[name] = uuidOsspTimeSaved(t, p, c, name)
		for _, value := range saved[name] {
			uuidOsspTimeExtract(t, p, c, r, value, name == "uuid_generate_v1mc")
		}
		run(fixture.Exec("CREATE VIEW "+name+"_view AS SELECT id,"+call+" AS u FROM "+name+"_saved", 0))
		run(fixture.Exec("CREATE VIEW "+name+"_nested AS SELECT id,(SELECT "+call+") AS u FROM "+name+"_saved", 0))
		for _, view := range []string{name + "_view", name + "_nested"} {
			q := "SELECT u FROM " + view + " ORDER BY id"
			a := uuidOsspTimeRead(t, p, c, r, pg, q, 2, 1, name == "uuid_generate_v1mc", true)
			s, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			held := fixture.HeldStatement{Executor: c, Statement: s, SQL: q}
			for range 2 {
				b := uuidOsspTimeRead(t, p, held, r, pg, q, 2, 1, name == "uuid_generate_v1mc", true)
				prior := map[string]bool{}
				for _, v := range a {
					prior[v] = true
				}
				for _, v := range b {
					if prior[v] {
						t.Fatal("view cached value", v)
					}
				}
				if view == name+"_view" && b[0] == b[1] {
					t.Fatal("row-wise view reused value")
				}
				a = b
			}
			s.Close()
		}
		// Actual stored expressions must depend on the genuine installed member.
		var member, dependencies int64
		if err := c.QueryRowContext(p.Ctx, "SELECT p.oid FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace WHERE n.nspname='uuid_ossp_part' AND p.proname=$1", name).Scan(&member); err != nil {
			t.Fatal(err)
		}
		for _, source := range []struct{ catalog, class, predicate string }{
			{"pg_attrdef", "2604", "adrelid='" + name + "_saved'::regclass::oid AND adnum=2"},
			{"pg_rewrite", "2618", "ev_class='" + name + "_view'::regclass::oid AND rulename='_RETURN'"},
			{"pg_rewrite", "2618", "ev_class='" + name + "_nested'::regclass::oid AND rulename='_RETURN'"},
		} {
			query := "SELECT count(*) FROM pg_depend WHERE classid=" + source.class + " AND objid=(SELECT oid FROM " + source.catalog + " WHERE " + source.predicate + ") AND objsubid=0 AND refclassid=1255 AND refobjid=$1 AND refobjsubid=0 AND deptype='n'"
			if err := c.QueryRowContext(p.Ctx, query, member).Scan(&dependencies); err != nil || dependencies != 1 {
				t.Fatal("genuine consumer dependency", source, dependencies, err)
			}
		}
		for _, prepared := range []bool{false, true} {
			for _, tc := range []struct{ q, message string }{
				{"CREATE TABLE " + name + "_generated(u uuid GENERATED ALWAYS AS (" + call + ") STORED)", "generation expression is not immutable"},
				{"CREATE INDEX " + name + "_volatile_idx ON " + name + "_saved ((" + call + "))", "functions in index expression must be marked IMMUTABLE"},
			} {
				step := fixture.ImplicitError(tc.q, fixture.ControlsReferenceDiagnostic("42P17", tc.message))
				step.Prepared = prepared
				run(step)
			}
		}
		run(fixture.Select("SELECT relname FROM pg_class WHERE relnamespace='public'::regnamespace AND relname IN ('"+name+"_generated','"+name+"_volatile_idx')", []string{"relname"}, []string{"NAME"}, [][]any{}))
	}
	return saved
}

func TestReferenceUUIDOsspTimeConsumers(t *testing.T) {
	uuidOsspCase(t, false, func(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool) {
		uuidOsspTimeConsumers(t, p, c, r, pg)
	})
}
