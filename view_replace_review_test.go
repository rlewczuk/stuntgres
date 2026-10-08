package stuntgres_test

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"stuntgres"
	"testing"
)

func TestViewReplaceCreationNamespace(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, kind := range []string{"view", "table", "sequence"} {
			t.Run(fmt.Sprintf("%s/prepared=%v", kind, prepared), func(t *testing.T) {
				db := openLocal(t, stuntgres.NewDriver(), t.Name())
				c, err := db.Conn(context.Background())
				if err != nil {
					t.Fatal(err)
				}
				defer c.Close()
				for _, q := range []string{"CREATE SCHEMA first_schema", "CREATE TABLE public.t(id INT)", "INSERT INTO public.t VALUES(1)"} {
					if _, err := c.ExecContext(context.Background(), q); err != nil {
						t.Fatal(q, err)
					}
				}
				later := map[string]string{"view": "CREATE VIEW public.v AS SELECT id FROM public.t", "table": "CREATE TABLE public.v(id INT)", "sequence": "CREATE SEQUENCE public.v START WITH 7"}[kind]
				if _, err := c.ExecContext(context.Background(), later); err != nil {
					t.Fatal(err)
				}
				var oldOID, oldNamespace int64
				if err := c.QueryRowContext(context.Background(), "SELECT oid,relnamespace FROM pg_class WHERE relname='v'").Scan(&oldOID, &oldNamespace); err != nil {
					t.Fatal(err)
				}
				if _, err := c.ExecContext(context.Background(), "SET search_path TO first_schema,public"); err != nil {
					t.Fatal(err)
				}
				query := "CREATE OR REPLACE VIEW v AS SELECT id+10 AS id FROM public.t"
				var result sql.Result
				if prepared {
					s, err := c.PrepareContext(context.Background(), query)
					if err != nil {
						t.Fatal(err)
					}
					defer s.Close()
					result, err = s.ExecContext(context.Background())
					if err != nil {
						t.Fatal(err)
					}
				} else {
					result, err = c.ExecContext(context.Background(), query)
					if err != nil {
						t.Fatal(err)
					}
				}
				if n, err := result.RowsAffected(); err != nil || n != 0 {
					t.Fatal(n, err)
				}
				var id, createdOID, createdNamespace int64
				if err := c.QueryRowContext(context.Background(), "SELECT id FROM first_schema.v").Scan(&id); err != nil || id != 11 {
					t.Fatal(id, err)
				}
				if err := c.QueryRowContext(context.Background(), "SELECT oid,relnamespace FROM pg_class WHERE relname='v' AND oid<>$1", oldOID).Scan(&createdOID, &createdNamespace); err != nil {
					t.Fatal(err)
				}
				if createdOID == oldOID || createdNamespace == oldNamespace {
					t.Fatal("creation targeted later relation")
				}
				var retainedNamespace int64
				if err := c.QueryRowContext(context.Background(), "SELECT relnamespace FROM pg_class WHERE oid=$1", oldOID).Scan(&retainedNamespace); err != nil || retainedNamespace != oldNamespace {
					t.Fatal(retainedNamespace, err)
				}
				switch kind {
				case "view":
					if err := c.QueryRowContext(context.Background(), "SELECT id FROM public.v").Scan(&id); err != nil || id != 1 {
						t.Fatal(id, err)
					}
				case "table":
					if _, err := c.ExecContext(context.Background(), "INSERT INTO public.v VALUES(7)"); err != nil {
						t.Fatal(err)
					}
					if err := c.QueryRowContext(context.Background(), "SELECT id FROM public.v").Scan(&id); err != nil || id != 7 {
						t.Fatal(id, err)
					}
				case "sequence":
					if err := c.QueryRowContext(context.Background(), "SELECT nextval('public.v')").Scan(&id); err != nil || id != 7 {
						t.Fatal(id, err)
					}
				}
			})
		}
	}
}

func TestViewReplaceBPCharDiagnostics(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, oldBPChar := range []bool{false, true} {
			t.Run(fmt.Sprintf("old_bpchar=%v/prepared=%v", oldBPChar, prepared), func(t *testing.T) {
				db := openLocal(t, stuntgres.NewDriver(), t.Name())
				oldExpression, newExpression, oldType, newType := "txt", "txt::bpchar", "text", "bpchar"
				if oldBPChar {
					oldExpression, newExpression, oldType, newType = newExpression, oldExpression, newType, oldType
				}
				for _, q := range []string{"CREATE TABLE t(txt TEXT)", "INSERT INTO t VALUES('a')", "CREATE VIEW v AS SELECT " + oldExpression + " AS x FROM t", "CREATE VIEW w AS SELECT x FROM v"} {
					execSQL(t, db, q)
				}
				var before bytes.Buffer
				if err := stuntgres.Dump(context.Background(), db, &before); err != nil {
					t.Fatal(err)
				}
				query := "CREATE OR REPLACE VIEW v AS SELECT " + newExpression + " AS x FROM t"
				var err error
				if prepared {
					s, prepareErr := db.Prepare(query)
					if prepareErr != nil {
						t.Fatal(prepareErr)
					}
					defer s.Close()
					_, err = s.Exec()
				} else {
					_, err = db.Exec(query)
				}
				typeCoreDiagnostic(t, err, stuntgres.Error{Code: "42P16", Message: "cannot change data type of view column \"x\" from " + oldType + " to " + newType})
				var after bytes.Buffer
				if err := stuntgres.Dump(context.Background(), db, &after); err != nil {
					t.Fatal(err)
				}
				if before.String() != after.String() {
					t.Fatal("failed replacement changed persistent definition")
				}
				wantTuples(t, db, "SELECT x::TEXT FROM v", [][]any{{"a"}})
				wantTuples(t, db, "SELECT x::TEXT FROM w", [][]any{{"a"}})
			})
		}
	}
}
