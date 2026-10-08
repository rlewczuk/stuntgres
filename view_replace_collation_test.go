package stuntgres_test

import (
	"bytes"
	"context"
	"stuntgres"
	"testing"
)

func TestViewReplaceCollationReplay(t *testing.T) {
	ctx := context.Background()
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	for _, q := range []string{
		"CREATE SCHEMA s", "CREATE TABLE t(txt TEXT)", "INSERT INTO t VALUES('a')",
		`CREATE VIEW cv AS SELECT txt COLLATE "C" AS x,'é'::VARCHAR(3) COLLATE "C" AS label FROM t WHERE txt COLLATE "C"='a' ORDER BY txt COLLATE "C"`,
		`CREATE OR REPLACE VIEW cv AS SELECT txt COLLATE "C" AS x,'é'::VARCHAR(3) COLLATE "C" AS label,TRUE AS extra FROM t WHERE txt COLLATE "C"='a' ORDER BY txt COLLATE "C"`,
		"ALTER VIEW cv RENAME COLUMN x TO key", "ALTER VIEW cv SET SCHEMA s",
	} {
		execSQL(t, db, q)
	}
	for i := range 2 {
		var dump bytes.Buffer
		if err := stuntgres.Dump(ctx, db, &dump); err != nil {
			t.Fatal(err)
		}
		fresh := openLocal(t, stuntgres.NewDriver(), t.Name()+string(rune('a'+i)))
		if err := stuntgres.Restore(ctx, fresh, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		db = fresh
		rows, err := db.Query("SELECT key,label,extra FROM s.cv")
		queryCoreRows(t, rows, err, []string{"key", "label", "extra"}, []string{"TEXT", "VARCHAR", "BOOL"}, [][]any{{"a", "é", true}}, false)
		_, err = db.Exec(`CREATE OR REPLACE VIEW s.cv AS SELECT txt COLLATE "POSIX" AS key,'é'::VARCHAR(3) COLLATE "C" AS label,TRUE AS extra FROM t`)
		typeCoreDiagnostic(t, err, stuntgres.Error{Code: "42P16", Message: `cannot change collation of view column "key" from "C" to "POSIX"`})
	}
	execSQL(t, db, "INSERT INTO t VALUES('a')")
	rows, err := db.Query("SELECT key FROM s.cv")
	queryCoreRows(t, rows, err, []string{"key"}, []string{"TEXT"}, [][]any{{"a"}, {"a"}}, false)
}

func TestViewReplaceDependencyVisibility(t *testing.T) {
	for _, tc := range []struct{ name, view, dependent, path, shadow, description, detail string }{
		{"moved", "v", "w", "public", "", "s.v", "w"},
		{"visible", "v", "w", "s,public", "", "v", "w"},
		{"shadowed", "v", "w", "public,s", "CREATE VIEW v AS SELECT id FROM t", "s.v", "w"},
		{"quoted", `"odd view"`, `"odd dependent"`, "public", "", `s."odd view"`, `"odd dependent"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := openLocal(t, stuntgres.NewDriver(), t.Name())
			for _, q := range []string{"CREATE SCHEMA s", "CREATE TABLE t(id INT)", "INSERT INTO t VALUES(7)", "CREATE VIEW s." + tc.view + " AS SELECT id FROM t", "CREATE VIEW " + tc.dependent + " AS SELECT id FROM s." + tc.view} {
				execSQL(t, db, q)
			}
			if tc.shadow != "" {
				execSQL(t, db, tc.shadow)
			}
			execSQL(t, db, "SET search_path TO "+tc.path)
			_, err := db.Exec("DROP VIEW s." + tc.view)
			typeCoreDiagnostic(t, err, stuntgres.Error{Code: "2BP01", Message: "cannot drop view " + tc.description + " because other objects depend on it", Detail: "view " + tc.detail + " depends on view " + tc.description, Hint: "Use DROP ... CASCADE to drop the dependent objects too."})
			rows, err := db.Query("SELECT id FROM " + tc.dependent)
			queryCoreRows(t, rows, err, []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}, false)
		})
	}
}
