package reference_test

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"stuntgres"
)

func TestReferenceQueryLimitViewBounds(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
			p := referenceLimitFixture(t)
			p.step(t, referenceExec("DROP SCHEMA IF EXISTS bounds_schema CASCADE", 0))
			p.step(t, referenceExec("DROP SCHEMA IF EXISTS renamed_bounds CASCADE", 0))
			for _, step := range []referenceStep{
				referenceExec("CREATE SCHEMA bounds_schema", 0), referenceExec("CREATE TABLE bounds_schema.src(id INT)", 0),
				referenceExec("INSERT INTO bounds_schema.src VALUES(1),(2),(3),(4)", 4), referenceExec("CREATE SEQUENCE bounds_schema.s START WITH 1", 0),
				referenceExec("CREATE VIEW bounds_schema.dynamic AS SELECT id FROM bounds_schema.src ORDER BY id OFFSET NULL LIMIT (nextval('bounds_schema.s')+1)", 0),
				referenceExec("CREATE VIEW bounds_schema.unlimited AS SELECT id FROM bounds_schema.src LIMIT ALL OFFSET NULL", 0),
				referenceExec("CREATE VIEW bounds_schema.empty AS SELECT id FROM bounds_schema.src LIMIT (1-1)", 0),
				referenceExec("ALTER SCHEMA bounds_schema RENAME TO renamed_bounds", 0), referenceExec("ALTER TABLE renamed_bounds.src RENAME COLUMN id TO renamed", 0),
				referenceExec("ALTER TABLE renamed_bounds.src RENAME TO renamed_src", 0),
				referenceExec("ALTER SEQUENCE renamed_bounds.s RENAME TO renamed_s", 0),
			} {
				p.step(t, step)
			}
			execute := func(step referenceStep) {
				t.Helper()
				if step.selectRows && step.query != "SELECT id FROM renamed_bounds.dynamic ORDER BY id" {
					p.step(t, step)
					p.step(t, referencePrepared(step))
					return
				}
				if prepared {
					step = referencePrepared(step)
				}
				p.step(t, step)
			}
			execute(referenceSelect("SELECT id FROM renamed_bounds.dynamic ORDER BY id", []string{"id"}, []string{"INT4"}, referenceLimitIDs(1, 2)))
			execute(referenceSelect("SELECT last_value FROM renamed_bounds.renamed_s", []string{"last_value"}, []string{"INT8"}, referenceLimitIDs(1)))
			execute(referenceSelect("SELECT id FROM renamed_bounds.unlimited ORDER BY id", []string{"id"}, []string{"INT4"}, referenceLimitIDs(1, 2, 3, 4)))
			execute(referenceSelect("SELECT id FROM renamed_bounds.empty", []string{"id"}, []string{"INT4"}, referenceLimitIDs()))
			for _, backend := range p.backends() {
				want := referenceDiagnostic{Code: "2BP01", Message: "cannot drop sequence renamed_s because other objects depend on it", Detail: "view dynamic depends on sequence renamed_s", Hint: "Use DROP ... CASCADE to drop the dependent objects too."}
				if backend.pg {
					want.Message = "cannot drop sequence renamed_bounds.renamed_s because other objects depend on it"
					want.Detail = "view renamed_bounds.dynamic depends on sequence renamed_bounds.renamed_s"
				}
				referenceRunStep(t, p.ctx, backend.db, backend.recorder, referenceImplicitError("DROP SEQUENCE renamed_bounds.renamed_s RESTRICT", want))
			}
			held, err := p.memory.QueryContext(context.WithValue(p.ctx, referenceRecorderKey{}, p.memoryRecord), "SELECT id FROM renamed_bounds.unlimited ORDER BY id LIMIT 2")
			if err != nil {
				t.Fatal(err)
			}
			defer held.Close()
			p.memoryRecord.mu.Lock()
			heldRecord := p.memoryRecord.operations[len(p.memoryRecord.operations)-1]
			p.memoryRecord.mu.Unlock()
			var dump bytes.Buffer
			if err := stuntgres.Dump(p.ctx, p.native, &dump); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.ctx, p.native, strings.NewReader(dump.String()+"\nSELECT id FROM missing LIMIT 1;")); err == nil {
				t.Fatal("invalid restore accepted")
			}
			unlimited := referenceSelect("SELECT id FROM renamed_bounds.unlimited ORDER BY id", []string{"id"}, []string{"INT4"}, referenceLimitIDs(1, 2, 3, 4))
			referenceRunStep(t, p.ctx, p.memory, p.memoryRecord, unlimited)
			referenceRunStep(t, p.ctx, p.memory, p.memoryRecord, referencePrepared(unlimited))
			if err := stuntgres.Restore(p.ctx, p.native, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
			queryLimitHeldBounds(t, held)
			p.memoryRecord.mu.Lock()
			recorded := heldRecord.Closed && heldRecord.CloseErr == nil && len(heldRecord.Columns) == 1 && heldRecord.Columns[0] == "id" && len(heldRecord.Rows) == 2 && len(heldRecord.Rows[0]) == 1 && len(heldRecord.Rows[1]) == 1 && heldRecord.Rows[0][0] == int64(1) && heldRecord.Rows[1][0] == int64(2)
			p.memoryRecord.mu.Unlock()
			if !recorded {
				t.Fatal("held bounds consumed output/closure not recorded")
			}
			execute(referenceSelect("SELECT id FROM renamed_bounds.dynamic ORDER BY id", []string{"id"}, []string{"INT4"}, referenceLimitIDs(1, 2, 3)))
			// Public-schema object names make the existing dependency diagnostic
			// convention independently comparable, without widening qualification.
			p.step(t, referenceExec("CREATE SEQUENCE dep_s", 0))
			p.step(t, referenceExec("CREATE VIEW dependent AS SELECT id FROM t LIMIT nextval('dep_s')", 0))
			p.step(t, referenceImplicitError("DROP SEQUENCE dep_s RESTRICT", referenceDiagnostic{Code: "2BP01", Message: "cannot drop sequence dep_s because other objects depend on it", Detail: "view dependent depends on sequence dep_s", Hint: "Use DROP ... CASCADE to drop the dependent objects too."}))
			p.step(t, referenceExec("DROP SCHEMA renamed_bounds CASCADE", 0))
		})
	}
}
