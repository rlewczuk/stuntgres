package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

// Without wal_level=logical (this package's default replica level) every
// logical slot operation that needs decoding fails first; drop and advance
// need no decoding and report the absent slot.
func TestReferenceLogicalSlotWALLevel(t *testing.T) {
	p := fixture.NewScenario(t)
	required := fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "55000", Message: `logical decoding requires "wal_level" >= "logical"`}
	absent := fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42704", Message: `replication slot "wal_level_slot" does not exist`}
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			slotsRun(t, p, b, c, fixture.Select("SHOW wal_level", []string{"wal_level"}, []string{"TEXT"}, [][]any{{"replica"}}))
			for _, prepared := range []bool{false, true} {
				for _, tc := range []struct {
					query string
					want  fixture.Diagnostic
				}{
					{"SELECT * FROM pg_create_logical_replication_slot('wal_level_slot','test_decoding')", required},
					{"SELECT * FROM pg_create_logical_replication_slot('BAD','unlisted',true,false,true)", required},
					{"SELECT * FROM pg_copy_logical_replication_slot('wal_level_slot','wal_level_copy')", required},
					{"SELECT * FROM pg_logical_slot_get_changes('wal_level_slot',NULL,NULL)", required},
					{"SELECT * FROM pg_logical_slot_peek_changes(NULL,NULL,NULL)", required},
					{"SELECT * FROM pg_logical_slot_get_binary_changes('wal_level_slot',NULL,NULL)", required},
					{"SELECT * FROM pg_logical_slot_peek_binary_changes('wal_level_slot',NULL,NULL,'x')", required},
					{"SELECT pg_drop_replication_slot('wal_level_slot')", absent},
					{"SELECT * FROM pg_replication_slot_advance('wal_level_slot','0/1')", absent},
				} {
					step := fixture.ErrorDiagnostic(tc.query, tc.want)
					step.SelectRows, step.ErrorStage, step.Prepared = true, "execute", prepared
					slotsRun(t, p, b, c, step)
				}
			}
		})
	}
}
