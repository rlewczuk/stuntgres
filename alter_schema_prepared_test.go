package stuntgres_test

import (
	"context"
	"database/sql"
	"reflect"
	"stuntgres"
	"testing"
)

func TestAlterSchemaHeldPrepared(t *testing.T) {
	for _, qualified := range []bool{true, false} {
		t.Run(map[bool]string{true: "qualified", false: "search_path"}[qualified], func(t *testing.T) {
			db := alterSchemaFixture(t)
			c, err := db.Conn(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			if _, err := c.ExecContext(context.Background(), "SET search_path TO s1,s2"); err != nil {
				t.Fatal(err)
			}
			name := "t"
			if qualified {
				name = "s1.t"
			}
			prepare := func(q string) *sql.Stmt {
				t.Helper()
				s, err := c.PrepareContext(context.Background(), q)
				if err != nil {
					t.Fatal(q, err)
				}
				t.Cleanup(func() { s.Close() })
				return s
			}
			selectStmt := prepare("SELECT id,note FROM " + name + " ORDER BY id")
			insertStmt := prepare("INSERT INTO " + name + "(id,note) VALUES($1,$2)")
			sequenceStmt := prepare("SELECT nextval('s1.q1')")
			var n int64
			if err := sequenceStmt.QueryRow().Scan(&n); err != nil || n != 10 {
				t.Fatal(n, err)
			}
			if _, err := c.ExecContext(context.Background(), "ALTER TABLE s1.t SET SCHEMA s2"); err != nil {
				t.Fatal(err)
			}
			_, err = sequenceStmt.Query()
			alterDropDiagnostic(t, err, "42P01", `relation "s1.q1" does not exist`, "16")
			if qualified {
				_, err = selectStmt.Query()
				alterDropDiagnostic(t, err, "42P01", `relation "s1.t" does not exist`, "21")
				_, err = insertStmt.Exec(11, "new")
				alterDropDiagnostic(t, err, "42P01", `relation "s1.t" does not exist`, "13")
				insertStmt = prepare("INSERT INTO s2.t(id,note) VALUES($1,$2)")
				selectStmt = prepare("SELECT id,note FROM s2.t ORDER BY id")
			}
			result, err := insertStmt.Exec(11, "new")
			if err != nil {
				t.Fatal(err)
			}
			if n, err := result.RowsAffected(); err != nil || n != 1 {
				t.Fatal(n, err)
			}
			rows, err := selectStmt.Query()
			if err != nil {
				t.Fatal(err)
			}
			var got [][]any
			for rows.Next() {
				var a, b any
				if err := rows.Scan(&a, &b); err != nil {
					rows.Close()
					t.Fatal(err)
				}
				got = append(got, []any{a, b})
			}
			err = rows.Err()
			rows.Close()
			if err != nil || !reflect.DeepEqual(got, [][]any{{int64(7), "keep"}, {int64(9), nil}, {int64(11), "new"}}) {
				t.Fatal(got, err)
			}
			if !qualified {
				for _, q := range []string{"CREATE TABLE s1.t(id INT CONSTRAINT nn_id NOT NULL,note TEXT)", "INSERT INTO s1.t VALUES(99,'shadow')"} {
					if _, err := c.ExecContext(context.Background(), q); err != nil {
						t.Fatal(err)
					}
				}
				rows, err = selectStmt.Query()
				if err != nil {
					t.Fatal(err)
				}
				defer rows.Close()
				var id int64
				var note string
				if !rows.Next() {
					t.Fatal(rows.Err())
				}
				if err := rows.Scan(&id, &note); err != nil || id != 99 || note != "shadow" {
					t.Fatal(id, note, err)
				}
				if rows.Next() || rows.Err() != nil {
					t.Fatal("shadow count", rows.Err())
				}
			}
		})
	}
}

func TestAlterSchemaNonTableAndDestinationBoundaries(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	for _, q := range []string{"CREATE SCHEMA s1", "CREATE SCHEMA s2", "CREATE TABLE s1.t(id INT)", "CREATE SEQUENCE s1.q", "CREATE VIEW s1.v AS SELECT id FROM s1.t"} {
		execSQL(t, db, q)
	}
	for _, tc := range []struct{ q, message string }{
		{"ALTER TABLE s1.q SET SCHEMA s2", "ALTER TABLE SET SCHEMA supports ordinary tables only"},
		{"ALTER TABLE s1.t SET SCHEMA pg_temp", "temporary schema creation is unsupported"},
		{"ALTER TABLE s1.t SET SCHEMA pg_catalog", "system schema creation is unsupported"},
	} {
		before := alterSchemaDump(t, db)
		s, err := db.Prepare(tc.q)
		if err != nil {
			t.Fatal("Prepare", tc.q, err)
		}
		_, err = s.Exec()
		s.Close()
		alterDropDiagnostic(t, err, "0A000", tc.message, "")
		if got := alterSchemaDump(t, db); got != before {
			t.Fatal(tc.q, "changed catalog")
		}
	}
	execSQL(t, db, "INSERT INTO s1.t VALUES(7)")
	var viewOID, oldNamespace int64
	if err := db.QueryRow("SELECT oid,relnamespace FROM pg_class WHERE relname='v'").Scan(&viewOID, &oldNamespace); err != nil {
		t.Fatal(err)
	}
	stmt, err := db.Prepare("ALTER TABLE s1.v SET SCHEMA s2")
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	if _, err := stmt.Exec(); err != nil {
		t.Fatal(err)
	}
	wantTuples(t, db, "SELECT id FROM s2.v", [][]any{{int64(7)}})
	var movedOID, newNamespace, sourceNamespace int64
	if err := db.QueryRow("SELECT oid,relnamespace FROM pg_class WHERE relname='v'").Scan(&movedOID, &newNamespace); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow("SELECT relnamespace FROM pg_class WHERE relname='t'").Scan(&sourceNamespace); err != nil {
		t.Fatal(err)
	}
	if movedOID != viewOID || newNamespace == oldNamespace || sourceNamespace != oldNamespace {
		t.Fatal("view move changed identity or source namespace", viewOID, movedOID, oldNamespace, newNamespace, sourceNamespace)
	}
}
