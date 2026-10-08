package stuntgres_test

import (
	"context"
	"fmt"
	"stuntgres"
	"testing"
)

func TestViewReplaceSelectedTargetOwnershipProtocol(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, kind := range []string{"table", "sequence"} {
			t.Run(fmt.Sprintf("%s/prepared=%v", kind, prepared), func(t *testing.T) {
				db := openLocal(t, stuntgres.NewDriver(), t.Name())
				ctx := context.Background()
				c, err := db.Conn(ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer c.Close()
				for _, q := range []string{"CREATE USER actor", "CREATE SCHEMA chosen AUTHORIZATION actor", "CREATE TABLE chosen.t(id INT)", "INSERT INTO chosen.t VALUES(1)"} {
					if _, err := c.ExecContext(ctx, q); err != nil {
						t.Fatal(q, err)
					}
				}
				query := "CREATE TABLE chosen.v(id INT)"
				if kind == "sequence" {
					query = "CREATE SEQUENCE chosen.v START WITH 7"
				}
				if _, err := c.ExecContext(ctx, query); err != nil {
					t.Fatal(err)
				}
				if kind == "table" {
					if _, err := c.ExecContext(ctx, "INSERT INTO chosen.v VALUES(7)"); err != nil {
						t.Fatal(err)
					}
				}
				var oid, namespace, owner int64
				var targetKind string
				if err := c.QueryRowContext(ctx, "SELECT oid,relnamespace,relowner,relkind FROM pg_class WHERE relname='v'").Scan(&oid, &namespace, &owner, &targetKind); err != nil {
					t.Fatal(err)
				}
				if _, err := c.ExecContext(ctx, "SET SESSION AUTHORIZATION actor"); err != nil {
					t.Fatal(err)
				}
				query = "CREATE OR REPLACE VIEW chosen.v AS SELECT id FROM chosen.t"
				if prepared {
					s, err := c.PrepareContext(ctx, query)
					if err != nil {
						t.Fatal("effect-free Prepare", err)
					}
					defer s.Close()
					_, err = s.ExecContext(ctx)
					typeCoreDiagnostic(t, err, stuntgres.Error{Code: "42501", Message: "must be owner of " + kind + " v"})
				} else {
					_, err := c.ExecContext(ctx, query)
					typeCoreDiagnostic(t, err, stuntgres.Error{Code: "42501", Message: "must be owner of " + kind + " v"})
				}
				var retainedOID, retainedNamespace, retainedOwner int64
				var retainedKind string
				if err := c.QueryRowContext(ctx, "SELECT oid,relnamespace,relowner,relkind FROM pg_class WHERE relname='v'").Scan(&retainedOID, &retainedNamespace, &retainedOwner, &retainedKind); err != nil {
					t.Fatal(err)
				}
				if retainedOID != oid || retainedNamespace != namespace || retainedOwner != owner || retainedKind != targetKind {
					t.Fatal("unauthorized replacement changed target identity/kind")
				}
				if _, err := c.ExecContext(ctx, "RESET SESSION AUTHORIZATION"); err != nil {
					t.Fatal(err)
				}
				var value int64
				query = "SELECT id FROM chosen.v"
				if kind == "sequence" {
					query = "SELECT nextval('chosen.v')"
				}
				if err := c.QueryRowContext(ctx, query).Scan(&value); err != nil || value != 7 {
					t.Fatal(value, err)
				}
			})
		}
	}
}
