package reference_test

import (
	"bytes"
	"context"
	"stuntgres"
	"testing"
)

func TestReferenceLikePatternEscapeStoredDependencies(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			db := backend.db
			stringCoreControlExec(t, db, "CREATE TABLE t(id INT,p TEXT,e TEXT)")
			stringCoreControlExec(t, db, "INSERT INTO t VALUES(1,'a%','!'),(2,NULL,NULL)")
			stringCoreControlExec(t, db, "CREATE VIEW matched AS SELECT id,'ab' LIKE p ESCAPE e AS matched FROM t")
			stmt, err := db.Prepare("SELECT matched FROM matched ORDER BY id")
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			for _, q := range []string{"ALTER TABLE t RENAME COLUMN p TO pattern", "ALTER TABLE t RENAME COLUMN e TO escape_value"} {
				stringCoreControlExec(t, db, q)
				rows, err := stmt.Query()
				stringCoreRetainedRows(t, rows, err, nil, []string{"BOOL"}, [][]any{{true}, {nil}}, false)
			}
			if !backend.pg {
				controlDB := p.native
				var dump bytes.Buffer
				ctx := context.Background()
				if err := stuntgres.Dump(ctx, controlDB, &dump); err != nil {
					t.Fatal(err)
				}
				if err := stuntgres.Reset(ctx, controlDB); err != nil {
					t.Fatal(err)
				}
				if err := stuntgres.Restore(ctx, controlDB, bytes.NewReader(dump.Bytes())); err != nil {
					t.Fatal(err)
				}
			}
			rows, err := db.Query("SELECT matched FROM matched ORDER BY id")
			stringCoreRetainedRows(t, rows, err, nil, []string{"BOOL"}, [][]any{{true}, {nil}}, false)
		})
	}
}
