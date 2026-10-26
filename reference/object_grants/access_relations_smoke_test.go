package reference_test

import (
	"bytes"
	"database/sql"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceAccessRelationsSmoke(t *testing.T) {
	accessRelationsScenario(t, func(p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step)) {
		accessRelationsColumns(t, p, b, c, run)
		run(fixture.Exec("REVOKE SELECT ON ar_t FROM ar_reader", 0))
		run(fixture.Exec("GRANT SELECT ON SEQUENCE ar_s TO ar_reader", 0))
		accessRelationsRoutes(t, p, b, c, fixture.Select("SELECT has_sequence_privilege('ar_reader','ar_s','USAGE') AS u,has_sequence_privilege('ar_reader','ar_s','SELECT') AS s,has_sequence_privilege('ar_reader','ar_s','UPDATE') AS w", []string{"u", "s", "w"}, []string{"BOOL", "BOOL", "BOOL"}, [][]any{{false, true, false}}))
		run(fixture.Select("SELECT last_value,is_called FROM ar_s", []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, [][]any{{int64(17), false}}))
		run(objectGrantAccessValue("SELECT has_table_privilege(NULL::name,'ar_missing','bad') AS v", nil))
		run(accessRelationsError("SELECT has_table_privilege('ar_missing','ar_t','SELECT')", "42704", `role "ar_missing" does not exist`))
		diag := fixture.ImplicitError("SELECT has_column_privilege('ar_t',1,'SELECT')", fixture.Diagnostic{Code: "42883", Message: "function has_column_privilege(unknown, integer, unknown) does not exist", Hint: "No function matches the given name and argument types. You might need to add explicit type casts.", Position: "8"})
		diag.SelectRows = true
		run(diag)
		target := "pg_catalog.has_table_privilege(name,text,text)"
		defer accessRelationsRoutineCleanup(t, p, c, target)
		query := "SELECT has_table_privilege('ar_reader'::name,'ar_t'::text,'SELECT'::text) AS v"
		held := accessExecutionHeld(t, p, c, query)
		run(fixture.Exec("REVOKE EXECUTE ON FUNCTION "+target+" FROM PUBLIC", 0))
		fixture.RoleGrantActor(run, "ar_reader", func() {
			fixture.RunStep(t, p.Ctx, held, b.Recorder, objectGrantAccessDenied(query, "has_table_privilege", true))
			run(objectGrantAccessValue("SELECT has_table_privilege('ar_t','SELECT') AS v", false))
		})
		run(fixture.Exec("GRANT EXECUTE ON FUNCTION "+target+" TO ar_reader", 0))
		fixture.RoleGrantActor(run, "ar_reader", func() { fixture.RunStep(t, p.Ctx, held, b.Recorder, objectGrantAccessValue(query, false)) })
		// Restore routine defaults before capturing feature-created object state.
		accessRelationsRoutineCleanup(t, p, c, target)
		accessRelationsPersistSetup(run)
		accessRelationsAttributeSetup(run, false)
		accessRelationsPersistCheck(run)
		accessRelationsAttributeCheck(run, false)
		if b.PG {
			return
		}
		var saved bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &saved); err != nil {
			t.Fatal(err)
		}
		fresh, recorded, recorder := accessRelationsFresh(t, p)
		accessRelationsRestoreFailures(t, p, fresh, recorded, recorder, saved.String())
		if err := stuntgres.Restore(p.Ctx, fresh, bytes.NewReader(saved.Bytes())); err != nil {
			t.Fatal(err)
		}
		rr := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, recorded, recorder, s) }
		accessRelationsPersistCheck(rr)
		accessRelationsAttributeCheck(rr, false)
		var redump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, fresh, &redump); err != nil {
			t.Fatal(err)
		}
		if redump.String() != saved.String() {
			t.Fatal("smoke redump changed")
		}
	})
}
