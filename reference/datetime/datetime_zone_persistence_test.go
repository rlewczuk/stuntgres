package reference_test

import (
	"bytes"
	"database/sql"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceDatetimeZoneConvertedTimetzPersistence(t *testing.T) {
	datetimeZoneScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE dz_wide(id INTEGER,q TIMETZ(3))", 0))
		for _, query := range []string{
			"INSERT INTO dz_wide VALUES(1,timezone(INTERVAL '2147483647 seconds',TIMETZ '12:00+00'))",
			"INSERT INTO dz_wide VALUES(2,timezone(INTERVAL '2147483648 seconds',TIMETZ '12:00+00'))",
		} {
			run(fixture.Exec(query, 1))
		}
		want := [][]any{{int64(1), "15:14:07+596523:14:07"}, {int64(2), "08:45:52+4294370773:4294967282:4294967288"}}
		check := fixture.Select("SELECT id,q::TEXT AS q FROM dz_wide ORDER BY id", []string{"id", "q"}, []string{"INT4", "TEXT"}, want)
		run(check)
		// Existing TIMETZ arithmetic must preserve provider-created displacement,
		// without relaxing literal input or SET TIME ZONE's narrower valid domain.
		run(fixture.Exec("UPDATE dz_wide SET q=q+INTERVAL '0 seconds'", 2))
		run(fixture.Prepared(check))
		if b.PG {
			return
		}
		var original bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &original); err != nil {
			t.Fatal(err)
		}
		target, wrapped, record := timezoneDestination(t)
		if err := stuntgres.Restore(p.Ctx, target, bytes.NewReader(original.Bytes())); err != nil {
			t.Fatal(err)
		}
		fixture.RunStep(t, p.Ctx, wrapped, record, check)
		var again bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, target, &again); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(original.Bytes(), again.Bytes()) {
			t.Fatalf("converted TIMETZ redump changed\noriginal=%s\nagain=%s", original.Bytes(), again.Bytes())
		}
	})
}
