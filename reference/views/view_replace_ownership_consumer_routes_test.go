package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceViewReplaceSelectedTargetOwnershipProtocolConsumerRoutes(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, kind := range []string{"table", "sequence"} {
			t.Run(fmt.Sprintf("%s/prepared=%v", kind, prepared), func(t *testing.T) {
				fixture.RoleReferenceCleanup(t, []string{"actor"})
				p := fixture.NewScenario(t)
				fixture.SchemaReferenceCleanup(t, "chosen")
				for _, backend := range p.Backends() {
					db := backend.DB
					ctx := context.Background()
					c, err := db.Conn(ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer func() {
						if err := c.Close(); err != nil {
							t.Error(err)
						}
					}()
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
						defer func() {
							if err := s.Close(); err != nil {
								t.Error(err)
							}
						}()
						_, err = s.ExecContext(ctx)
						fixture.UpdateReturningReferenceDiagnostic(t, err, fixture.Diagnostic{Code: "42501", Message: "must be owner of " + kind + " v"})
					} else {
						_, err := c.ExecContext(ctx, query)
						fixture.UpdateReturningReferenceDiagnostic(t, err, fixture.Diagnostic{Code: "42501", Message: "must be owner of " + kind + " v"})
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
				}
			})
		}
	}
}

func TestReferenceViewReplaceCreationNamespaceConsumerRoutes(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, kind := range []string{"view", "table", "sequence"} {
			t.Run(fmt.Sprintf("%s/prepared=%v", kind, prepared), func(t *testing.T) {
				p := fixture.NewScenario(t)
				fixture.SchemaReferenceCleanup(t, "first_schema")
				for _, backend := range p.Backends() {
					db := backend.DB
					c, err := db.Conn(context.Background())
					if err != nil {
						t.Fatal(err)
					}
					defer func() {
						if err := c.Close(); err != nil {
							t.Error(err)
						}
					}()
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
						defer func() {
							if err := s.Close(); err != nil {
								t.Error(err)
							}
						}()
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
				}
			})
		}
	}
}
