package reference_test

import (
	"bytes"
	"context"
	"stuntgres"
	"testing"
)

func TestReferenceViewReplaceCollationReplay(t *testing.T) {
	ctx := context.Background()
	p := newReferenceScenario(t)
	schemaReferenceCleanup(t, "s")
	for _, backend := range p.backends() {
		db := backend.db
		native := p.native
		for _, q := range []string{
			"CREATE SCHEMA s", "CREATE TABLE t(txt TEXT)", "INSERT INTO t VALUES('a')",
			`CREATE VIEW cv AS SELECT txt COLLATE "C" AS x,'é'::VARCHAR(3) COLLATE "C" AS label FROM t WHERE txt COLLATE "C"='a' ORDER BY txt COLLATE "C"`,
			`CREATE OR REPLACE VIEW cv AS SELECT txt COLLATE "C" AS x,'é'::VARCHAR(3) COLLATE "C" AS label,TRUE AS extra FROM t WHERE txt COLLATE "C"='a' ORDER BY txt COLLATE "C"`,
			"ALTER VIEW cv RENAME COLUMN x TO key", "ALTER VIEW cv SET SCHEMA s",
		} {
			updateReturningReferenceExec(t, db, q)
		}
		for range 2 {
			if !backend.pg {
				var dump bytes.Buffer
				if err := stuntgres.Dump(ctx, native, &dump); err != nil {
					t.Fatal(err)
				}
				fresh := updateReturningNativeDestination(t)
				if err := stuntgres.Restore(ctx, fresh, bytes.NewReader(dump.Bytes())); err != nil {
					t.Fatal(err)
				}
				native, db = fresh, fresh
			}
			rows, err := db.Query("SELECT key,label,extra FROM s.cv")
			deleteReferenceConsume(t, backend.pg, rows, err, referenceStep{columns: []string{"key", "label", "extra"}, types: []string{"TEXT", "VARCHAR", "BOOL"}, rows: [][]any{{"a", "é", true}}, bag: false})
			_, err = db.Exec(`CREATE OR REPLACE VIEW s.cv AS SELECT txt COLLATE "POSIX" AS key,'é'::VARCHAR(3) COLLATE "C" AS label,TRUE AS extra FROM t`)
			updateReturningReferenceDiagnostic(t, err, referenceDiagnostic{Code: "42P16", Message: `cannot change collation of view column "key" from "C" to "POSIX"`})
		}
		updateReturningReferenceExec(t, db, "INSERT INTO t VALUES('a')")
		rows, err := db.Query("SELECT key FROM s.cv")
		deleteReferenceConsume(t, backend.pg, rows, err, referenceStep{columns: []string{"key"}, types: []string{"TEXT"}, rows: [][]any{{"a"}, {"a"}}, bag: false})
	}
}
