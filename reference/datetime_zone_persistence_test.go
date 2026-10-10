package reference_test

import (
	"bytes"
	"database/sql"
	"stuntgres"
	"testing"
)

func TestReferenceDatetimeZoneConvertedTimetzPersistence(t *testing.T) {
	datetimeZoneScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		run := func(s referenceStep) { referenceRunStep(t, p.ctx, c, b.recorder, s) }
		run(referenceExec("CREATE TABLE dz_wide(id INTEGER,q TIMETZ(3))", 0))
		for _, query := range []string{
			"INSERT INTO dz_wide VALUES(1,timezone(INTERVAL '2147483647 seconds',TIMETZ '12:00+00'))",
			"INSERT INTO dz_wide VALUES(2,timezone(INTERVAL '2147483648 seconds',TIMETZ '12:00+00'))",
		} {
			run(referenceExec(query, 1))
		}
		want := [][]any{{int64(1), "15:14:07+596523:14:07"}, {int64(2), "08:45:52+4294370773:4294967282:4294967288"}}
		check := referenceSelect("SELECT id,q::TEXT AS q FROM dz_wide ORDER BY id", []string{"id", "q"}, []string{"INT4", "TEXT"}, want)
		run(check)
		// Existing TIMETZ arithmetic must preserve provider-created displacement,
		// without relaxing literal input or SET TIME ZONE's narrower valid domain.
		run(referenceExec("UPDATE dz_wide SET q=q+INTERVAL '0 seconds'", 2))
		run(referencePrepared(check))
		if b.pg {
			return
		}
		var original bytes.Buffer
		if err := stuntgres.Dump(p.ctx, p.native, &original); err != nil {
			t.Fatal(err)
		}
		target, wrapped, record := timezoneDestination(t)
		if err := stuntgres.Restore(p.ctx, target, bytes.NewReader(original.Bytes())); err != nil {
			t.Fatal(err)
		}
		referenceRunStep(t, p.ctx, wrapped, record, check)
		var again bytes.Buffer
		if err := stuntgres.Dump(p.ctx, target, &again); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(original.Bytes(), again.Bytes()) {
			t.Fatalf("converted TIMETZ redump changed\noriginal=%s\nagain=%s", original.Bytes(), again.Bytes())
		}
	})
}
