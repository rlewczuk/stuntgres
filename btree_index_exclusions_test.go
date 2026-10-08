package stuntgres_test

import (
	"bytes"
	"context"
	"testing"

	"stuntgres"
)

func TestBtreeIndexExcludedDefinitionsDiagnostics(t *testing.T) {
	for _, tc := range []struct {
		name, query, message, position string
		execute                        bool
	}{
		{"unique", "CREATE UNIQUE INDEX i ON t(n)", "unique indexes are unsupported", "8", false},
		{"concurrent", "CREATE INDEX CONCURRENTLY i ON t(n)", "concurrent indexes are unsupported", "14", false},
		{"expression", "CREATE INDEX ix ON t((c+1))", "expression indexes are unsupported", "22", false},
		{"spaced-expression", "CREATE INDEX ix ON t ((c+1))", "expression indexes are unsupported", "23", false},
		{"unicode-expression", "/*é*/ CREATE INDEX ix ON t((c+1))", "expression indexes are unsupported", "28", false},
		{"partial", "CREATE INDEX i ON t(n) WHERE n>0", "partial indexes are unsupported", "24", false},
		{"collation", "CREATE INDEX i ON t(s COLLATE \"C\")", "index collations are unsupported", "23", false},
		{"opclass", "CREATE INDEX i ON t(n int4_ops)", "index operator classes are unsupported", "23", false},
		{"include", "CREATE INDEX i ON t(n) INCLUDE(s)", "included index columns are unsupported", "24", false},
		{"storage", "CREATE INDEX i ON t(n) WITH(fillfactor=80)", "index storage parameters are unsupported", "24", false},
		{"tablespace", "CREATE INDEX i ON t(n) TABLESPACE pg_default", "index tablespaces are unsupported", "24", false},
		{"method", "CREATE INDEX i ON t USING hash(n)", "non-btree indexes are unsupported", "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			db := openLocal(t, stuntgres.NewDriver(), t.Name())
			execSQL(t, db, "CREATE TABLE t(n INT,c INT,s TEXT)")
			execSQL(t, db, "INSERT INTO t VALUES(2,1,'one')")
			var before bytes.Buffer
			if err := stuntgres.Dump(ctx, db, &before); err != nil {
				t.Fatal(err)
			}
			want := stuntgres.Error{Code: "0A000", Message: tc.message, Position: tc.position}
			_, err := db.Exec(tc.query)
			typeCoreDiagnostic(t, err, want)
			stmt, err := db.Prepare(tc.query)
			if tc.execute {
				if err != nil {
					t.Fatal("method Prepare must be syntax-only", err)
				}
				_, err = stmt.Exec()
				stmt.Close()
			} else {
				if stmt != nil {
					stmt.Close()
				}
				if err == nil {
					t.Fatal("excluded definition admitted at Prepare")
				}
			}
			typeCoreDiagnostic(t, err, want)
			var after bytes.Buffer
			if err := stuntgres.Dump(ctx, db, &after); err != nil || before.String() != after.String() {
				t.Fatal("excluded definition changed persistent state", err, after.String())
			}
			wantTuples(t, db, "SELECT c,s FROM t", [][]any{{int64(1), "one"}})
			wantTuples(t, db, "SELECT relname FROM pg_class WHERE relname='i' OR relname='ix'", [][]any{})
			execSQL(t, db, "CREATE INDEX i ON t(n)")
			execSQL(t, db, "ALTER TABLE t ALTER c TYPE TEXT USING c::text")
			wantTuples(t, db, "SELECT c FROM t", [][]any{{"1"}})
		})
	}
}
