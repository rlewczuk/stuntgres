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

func TestReferenceSegIntervalAuthorizationControls(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{segIntervalMemberOwner, segIntervalAllowed, segIntervalDenied})
	segScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		segIntervalAuthorizationSetup(t, p, b, c)
		segIntervalAuthorizationVerify(t, p, b, c)
		if b.PG {
			return
		} // Shared SQL establishes authorization; controls are native.
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
			t.Fatal(err)
		}
		rows, err := c.QueryContext(p.Ctx, segIntervalAuthorizationQuery)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		columns, err := rows.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		segIntervalMetadata(t, false, columns, []string{"id", "via_function", "via_operator"}, []string{"INT4", "BOOL", "BOOL"}, false)
		metadata := segIntervalMetadataSnapshot(columns)
		typedQuery := "SELECT seg_overlap($1::seg,$2::seg) AS v"
		typedStmt, err := c.PrepareContext(p.Ctx, typedQuery)
		if err != nil {
			t.Fatal(err)
		}
		defer typedStmt.Close()
		typedHeld := fixture.HeldStatement{Executor: c, Statement: typedStmt, SQL: typedQuery}
		typedCheck := segIntervalSelect(b, typedQuery, []string{"v"}, []string{"BOOL"}, [][]any{{true}}, false, "0 .. 1", "1 .. 2")
		fixture.RunStep(t, p.Ctx, typedHeld, b.Recorder, typedCheck)
		textQuery := "SELECT seg_overlap($1::text::seg,$2::text::seg) AS v"
		textStmt, err := c.PrepareContext(p.Ctx, textQuery)
		if err != nil {
			t.Fatal(err)
		}
		defer textStmt.Close()
		textHeld := fixture.HeldStatement{Executor: c, Statement: textStmt, SQL: textQuery}
		fixture.RunStep(t, p.Ctx, textHeld, b.Recorder, segIntervalSelect(b, textQuery, []string{"v"}, []string{"BOOL"}, [][]any{{true}}, false, "0 .. 1", "1 .. 2"))
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		segIntervalAuthorizationVerify(t, p, b, c)
		stale := fixture.ErrorDiagnostic(typedQuery, fixture.ControlsReferenceDiagnostic("0A000", "cached plan must not change parameter type"), "0 .. 1", "1 .. 2")
		stale.SelectRows, stale.ErrorStage = true, "execute"
		fixture.RunStep(t, p.Ctx, typedHeld, b.Recorder, stale)
		for _, input := range []struct{ a, b, want any }{{"0 .. 1", "1 .. 2", true}, {"0 .. 1", "2 .. 3", false}, {nil, "1", nil}, {"1", "1", true}} {
			fixture.RunStep(t, p.Ctx, textHeld, b.Recorder, segIntervalSelect(b, textQuery, []string{"v"}, []string{"BOOL"}, [][]any{{input.want}}, false, input.a, input.b))
		}
		segIntervalBoth(t, p, b, c, typedCheck)
		segIntervalRetained(t, rows, segIntervalAuthorizationRows())
		if !reflect.DeepEqual(segIntervalMetadataSnapshot(columns), metadata) {
			t.Fatal("authorized control replacement changed retained BOOL metadata")
		}
		original := segIntervalAuthorizationIDs(t, p, c)
		fresh := fixture.NewFixture(t, false, true)
		owner, err := fresh.Memory.Conn(fresh.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer owner.Close()
		segIntervalRetainCollisions(t, fresh, owner, original)
		// Database dumps refer to named principals; provision each destination's
		// actual roles before replay, independently of source role identifiers.
		for _, role := range []string{segIntervalMemberOwner, segIntervalAllowed, segIntervalDenied} {
			fixture.RunStep(t, fresh.Ctx, owner, fresh.MemoryRecord, fixture.Exec("CREATE ROLE "+role, 0))
		}
		if err := stuntgres.Restore(fresh.Ctx, fresh.Native, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		destination := fixture.Backend{DB: fresh.Memory, Recorder: fresh.MemoryRecord}
		remapped := segIntervalAuthorizationIDs(t, fresh, owner)
		for i, id := range remapped {
			if id == original[i] {
				t.Fatal("custom-owner provider identity not reminted", original, remapped)
			}
		}
		segIntervalAuthorizationVerify(t, fresh, destination, owner)
		retained, err := owner.QueryContext(fresh.Ctx, segIntervalAuthorizationQuery)
		if err != nil {
			t.Fatal(err)
		}
		defer retained.Close()
		oldColumns, err := retained.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		oldMetadata := segIntervalMetadataSnapshot(oldColumns)
		bad := dump.String() + "INSERT INTO seg_interval_authorization VALUES (99,'ABC','1');"
		if err := stuntgres.Restore(fresh.Ctx, fresh.Native, strings.NewReader(bad)); err == nil {
			t.Fatal("bad replacement of custom authorization accepted")
		}
		segIntervalAuthorizationVerify(t, fresh, destination, owner)
		segIntervalRetained(t, retained, segIntervalAuthorizationRows())
		if !reflect.DeepEqual(segIntervalMetadataSnapshot(oldColumns), oldMetadata) {
			t.Fatal("failed custom-owner replacement changed retained metadata")
		}
		var again bytes.Buffer
		if err := stuntgres.Dump(fresh.Ctx, fresh.Native, &again); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(dump.Bytes(), again.Bytes()) {
			t.Fatal("custom owner/ACL changed through remint or failed replacement")
		}
		// Restoring another Driver never replaces the source lifetime or grants.
		fixture.RunStep(t, p.Ctx, textHeld, b.Recorder, segIntervalSelect(b, textQuery, []string{"v"}, []string{"BOOL"}, [][]any{{true}}, false, "1", "1"))
		segIntervalAuthorizationVerify(t, p, b, c)
	})
}
