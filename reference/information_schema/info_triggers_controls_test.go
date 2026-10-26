package reference_test

import (
	"bytes"
	"database/sql"
	"reflect"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceInfoTriggersNativeControls(t *testing.T) {
	itrScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fixture.Recorder, database string) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
		for _, q := range []string{
			"CREATE SCHEMA it_scope", "CREATE TABLE it_scope.it_data(id integer,v integer)",
			"CREATE FUNCTION public.it_add() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN NEW.v := NEW.v + TG_ARGV[0]::integer; RETURN NEW; END$$",
			"CREATE TRIGGER actual BEFORE UPDATE OF v ON it_scope.it_data FOR EACH ROW WHEN (NEW.v>0) EXECUTE FUNCTION public.it_add('3')",
		} {
			run(fixture.Exec(q, 0))
		}
		want := [][]any{itrRow(database, "it_scope", "it_data", "actual", "UPDATE", 1, "(new.v > 0)", "EXECUTE FUNCTION it_add('3')")}
		where := "trigger_schema='it_scope'"
		check := func() { s := itProjection(b.PG, where, want); run(s); run(fixture.Prepared(s)) }
		check()
		run(fixture.Exec("INSERT INTO it_scope.it_data VALUES(1,2)", 1))
		run(fixture.Select("UPDATE it_scope.it_data SET v=4 RETURNING id,v", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(7)}}))
		run(fixture.Select("UPDATE it_scope.it_data SET v=0 RETURNING v", []string{"v"}, []string{"INT4"}, [][]any{{int64(0)}}))
		if b.PG {
			return
		} // Public native controls have no PostgreSQL backup analogue.
		rows, err := c.QueryContext(p.Ctx, itProjection(false, where, want).Query)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		columns, err := rows.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		itrMetadata(t, columns, false)
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
			t.Fatal(err)
		}
		for _, forbidden := range []string{"CREATE VIEW information_schema.triggers", `CREATE VIEW "information_schema"."triggers"`, "INSERT INTO information_schema.triggers"} {
			if strings.Contains(dump.String(), forbidden) {
				t.Fatal("materialized provider", forbidden)
			}
		}
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		run(itProjection(false, where, nil))
		if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		check()
		var retained [][]any
		for rows.Next() {
			cells := make([]any, 17)
			dest := make([]any, 17)
			for i := range cells {
				dest[i] = &cells[i]
			}
			if err := rows.Scan(dest...); err != nil {
				t.Fatal(err)
			}
			retained = append(retained, cells)
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		if err := rows.Close(); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(retained, want) {
			t.Fatal("retained rows", retained, want)
		}
		itrMetadata(t, columns, false)
		connector, err := stuntgres.NewDriver().OpenConnector("it-independent-controls")
		if err != nil {
			t.Fatal(err)
		}
		destination := sql.OpenDB(connector)
		defer destination.Close()
		if err := stuntgres.Restore(p.Ctx, destination, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		wrapped, record := fixture.RecordedNativePool(t, connector)
		var targetDatabase string
		if err := destination.QueryRowContext(p.Ctx, "SELECT current_database()").Scan(&targetDatabase); err != nil {
			t.Fatal(err)
		}
		targetWant := [][]any{itrRow(targetDatabase, "it_scope", "it_data", "actual", "UPDATE", 1, "(new.v > 0)", "EXECUTE FUNCTION it_add('3')")}
		fixture.RunStep(t, p.Ctx, wrapped, record, itProjection(false, where, targetWant))
		fixture.RunStep(t, p.Ctx, wrapped, record, fixture.Select("UPDATE it_scope.it_data SET v=5 RETURNING v", []string{"v"}, []string{"INT4"}, [][]any{{int64(8)}}))
		// The source remains independent of the destination's remapped payloads.
		run(fixture.Select("SELECT v FROM it_scope.it_data", []string{"v"}, []string{"INT4"}, [][]any{{int64(0)}}))
		check()
	})
}
