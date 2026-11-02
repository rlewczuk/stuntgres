package enginetests

import (
	"testing"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/parser"
)

func TestMaterializedStorageFingerprint(t *testing.T) {
	for _, tc := range []struct {
		left, right string
		same        bool
	}{
		{"ALTER TABLE mv SET (fillfactor=70)", "ALTER MATERIALIZED VIEW mv SET (fillfactor=70)", false},
		{"ALTER TABLE ALL IN TABLESPACE pg_default SET TABLESPACE pg_default", "ALTER MATERIALIZED VIEW ALL IN TABLESPACE pg_default SET TABLESPACE pg_default", false},
		{"ALTER MATERIALIZED VIEW ALL IN TABLESPACE pg_default SET TABLESPACE pg_default", "ALTER MATERIALIZED VIEW ALL IN TABLESPACE pg_default SET TABLESPACE pg_default NOWAIT", false},
		{"ALTER MATERIALIZED VIEW ALL IN TABLESPACE pg_default OWNED BY CURRENT_USER SET TABLESPACE pg_default", `ALTER MATERIALIZED VIEW ALL IN TABLESPACE pg_default OWNED BY "current_user" SET TABLESPACE pg_default`, false},
		{`ALTER MATERIALIZED VIEW ALL IN TABLESPACE "pg_default" SET TABLESPACE pg_default`, " ALTER MATERIALIZED VIEW ALL IN TABLESPACE pg_default SET TABLESPACE pg_default", true},
	} {
		t.Run(tc.left+" / "+tc.right, func(t *testing.T) {
			left, _ := analyze.JumbleUtility(enginetest.Statement(t, tc.left))
			right, _ := analyze.JumbleUtility(enginetest.Statement(t, tc.right))
			if (left.QueryID() == right.QueryID()) != tc.same {
				t.Fatalf("utility identity same=%v want %v", left.QueryID() == right.QueryID(), tc.same)
			}
		})
	}
	table := &parser.AlterTableAllInTablespace{Source: parser.Identifier{Text: "pg_default"}, Destination: parser.Identifier{Text: "pg_default"}}
	materialized := *table
	materialized.Materialized = true
	tableID, _ := analyze.JumbleUtility(table)
	materializedID, _ := analyze.JumbleUtility(&materialized)
	if tableID.QueryID() == materializedID.QueryID() {
		t.Fatal("typed bulk Materialized field omitted from utility identity")
	}
}
