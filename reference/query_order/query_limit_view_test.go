package reference_test

import (
	"bytes"
	"context"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceQueryLimitViewBounds(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
			p := referenceLimitFixture(t)
			p.Step(t, fixture.Exec("DROP SCHEMA IF EXISTS bounds_schema CASCADE", 0))
			p.Step(t, fixture.Exec("DROP SCHEMA IF EXISTS renamed_bounds CASCADE", 0))
			for _, step := range []fixture.Step{
				fixture.Exec("CREATE SCHEMA bounds_schema", 0), fixture.Exec("CREATE TABLE bounds_schema.src(id INT)", 0),
				fixture.Exec("INSERT INTO bounds_schema.src VALUES(1),(2),(3),(4)", 4), fixture.Exec("CREATE SEQUENCE bounds_schema.s START WITH 1", 0),
				fixture.Exec("CREATE VIEW bounds_schema.dynamic AS SELECT id FROM bounds_schema.src ORDER BY id OFFSET NULL LIMIT (nextval('bounds_schema.s')+1)", 0),
				fixture.Exec("CREATE VIEW bounds_schema.unlimited AS SELECT id FROM bounds_schema.src LIMIT ALL OFFSET NULL", 0),
				fixture.Exec("CREATE VIEW bounds_schema.empty AS SELECT id FROM bounds_schema.src LIMIT (1-1)", 0),
				fixture.Exec("ALTER SCHEMA bounds_schema RENAME TO renamed_bounds", 0), fixture.Exec("ALTER TABLE renamed_bounds.src RENAME COLUMN id TO renamed", 0),
				fixture.Exec("ALTER TABLE renamed_bounds.src RENAME TO renamed_src", 0),
				fixture.Exec("ALTER SEQUENCE renamed_bounds.s RENAME TO renamed_s", 0),
			} {
				p.Step(t, step)
			}
			execute := func(step fixture.Step) {
				t.Helper()
				if step.SelectRows && step.Query != "SELECT id FROM renamed_bounds.dynamic ORDER BY id" {
					p.Step(t, step)
					p.Step(t, fixture.Prepared(step))
					return
				}
				if prepared {
					step = fixture.Prepared(step)
				}
				p.Step(t, step)
			}
			execute(fixture.Select("SELECT id FROM renamed_bounds.dynamic ORDER BY id", []string{"id"}, []string{"INT4"}, referenceLimitIDs(1, 2)))
			execute(fixture.Select("SELECT last_value FROM renamed_bounds.renamed_s", []string{"last_value"}, []string{"INT8"}, referenceLimitIDs(1)))
			execute(fixture.Select("SELECT id FROM renamed_bounds.unlimited ORDER BY id", []string{"id"}, []string{"INT4"}, referenceLimitIDs(1, 2, 3, 4)))
			execute(fixture.Select("SELECT id FROM renamed_bounds.empty", []string{"id"}, []string{"INT4"}, referenceLimitIDs()))
			for _, backend := range p.Backends() {
				want := fixture.Diagnostic{Code: "2BP01", Message: "cannot drop sequence renamed_bounds.renamed_s because other objects depend on it", Detail: "view renamed_bounds.dynamic depends on sequence renamed_bounds.renamed_s", Hint: "Use DROP ... CASCADE to drop the dependent objects too."}
				fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.ImplicitError("DROP SEQUENCE renamed_bounds.renamed_s RESTRICT", want))
			}
			held, err := p.Memory.QueryContext(context.WithValue(p.Ctx, fixture.RecorderKey{}, p.MemoryRecord), "SELECT id FROM renamed_bounds.unlimited ORDER BY id LIMIT 2")
			if err != nil {
				t.Fatal(err)
			}
			defer held.Close()
			p.MemoryRecord.Mu.Lock()
			heldRecord := p.MemoryRecord.Operations[len(p.MemoryRecord.Operations)-1]
			p.MemoryRecord.Mu.Unlock()
			var dump bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump.String()+"\nSELECT id FROM missing LIMIT 1;")); err == nil {
				t.Fatal("invalid restore accepted")
			}
			unlimited := fixture.Select("SELECT id FROM renamed_bounds.unlimited ORDER BY id", []string{"id"}, []string{"INT4"}, referenceLimitIDs(1, 2, 3, 4))
			fixture.RunStep(t, p.Ctx, p.Memory, p.MemoryRecord, unlimited)
			fixture.RunStep(t, p.Ctx, p.Memory, p.MemoryRecord, fixture.Prepared(unlimited))
			if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
			queryLimitHeldBounds(t, held)
			p.MemoryRecord.Mu.Lock()
			recorded := heldRecord.Closed && heldRecord.CloseErr == nil && len(heldRecord.Columns) == 1 && heldRecord.Columns[0] == "id" && len(heldRecord.Rows) == 2 && len(heldRecord.Rows[0]) == 1 && len(heldRecord.Rows[1]) == 1 && heldRecord.Rows[0][0] == int64(1) && heldRecord.Rows[1][0] == int64(2)
			p.MemoryRecord.Mu.Unlock()
			if !recorded {
				t.Fatal("held bounds consumed output/closure not recorded")
			}
			execute(fixture.Select("SELECT id FROM renamed_bounds.dynamic ORDER BY id", []string{"id"}, []string{"INT4"}, referenceLimitIDs(1, 2, 3)))
			// Public-schema object names make the existing dependency diagnostic
			// convention independently comparable, without widening qualification.
			p.Step(t, fixture.Exec("CREATE SEQUENCE dep_s", 0))
			p.Step(t, fixture.Exec("CREATE VIEW dependent AS SELECT id FROM t LIMIT nextval('dep_s')", 0))
			p.Step(t, fixture.ImplicitError("DROP SEQUENCE dep_s RESTRICT", fixture.Diagnostic{Code: "2BP01", Message: "cannot drop sequence dep_s because other objects depend on it", Detail: "view dependent depends on sequence dep_s", Hint: "Use DROP ... CASCADE to drop the dependent objects too."}))
			p.Step(t, fixture.Exec("DROP SCHEMA renamed_bounds CASCADE", 0))
		})
	}
}
