package reference_test

import (
	"bytes"
	"context"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceViewReplaceCollationReplay(t *testing.T) {
	ctx := context.Background()
	p := fixture.NewScenario(t)
	fixture.SchemaReferenceCleanup(t, "s")
	for _, backend := range p.Backends() {
		db := backend.DB
		native := p.Native
		for _, q := range []string{
			"CREATE SCHEMA s", "CREATE TABLE t(txt TEXT)", "INSERT INTO t VALUES('a')",
			`CREATE VIEW cv AS SELECT txt COLLATE "C" AS x,'é'::VARCHAR(3) COLLATE "C" AS label FROM t WHERE txt COLLATE "C"='a' ORDER BY txt COLLATE "C"`,
			`CREATE OR REPLACE VIEW cv AS SELECT txt COLLATE "C" AS x,'é'::VARCHAR(3) COLLATE "C" AS label,TRUE AS extra FROM t WHERE txt COLLATE "C"='a' ORDER BY txt COLLATE "C"`,
			"ALTER VIEW cv RENAME COLUMN x TO key", "ALTER VIEW cv SET SCHEMA s",
		} {
			fixture.UpdateReturningReferenceExec(t, db, q)
		}
		for range 2 {
			if !backend.PG {
				var dump bytes.Buffer
				if err := stuntgres.Dump(ctx, native, &dump); err != nil {
					t.Fatal(err)
				}
				fresh := fixture.UpdateReturningNativeDestination(t)
				if err := stuntgres.Restore(ctx, fresh, bytes.NewReader(dump.Bytes())); err != nil {
					t.Fatal(err)
				}
				native, db = fresh, fresh
			}
			rows, err := db.Query("SELECT key,label,extra FROM s.cv")
			fixture.DeleteReferenceConsume(t, backend.PG, rows, err, fixture.Step{Columns: []string{"key", "label", "extra"}, Types: []string{"TEXT", "VARCHAR", "BOOL"}, Rows: [][]any{{"a", "é", true}}, Bag: false})
			_, err = db.Exec(`CREATE OR REPLACE VIEW s.cv AS SELECT txt COLLATE "POSIX" AS key,'é'::VARCHAR(3) COLLATE "C" AS label,TRUE AS extra FROM t`)
			fixture.UpdateReturningReferenceDiagnostic(t, err, fixture.Diagnostic{Code: "42P16", Message: `cannot change collation of view column "key" from "C" to "POSIX"`})
		}
		fixture.UpdateReturningReferenceExec(t, db, "INSERT INTO t VALUES('a')")
		rows, err := db.Query("SELECT key FROM s.cv")
		fixture.DeleteReferenceConsume(t, backend.PG, rows, err, fixture.Step{Columns: []string{"key"}, Types: []string{"TEXT"}, Rows: [][]any{{"a"}, {"a"}}, Bag: false})
	}
}
