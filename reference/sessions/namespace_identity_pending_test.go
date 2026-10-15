package reference_test

import (
	"context"
	"errors"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceNamespaceIdentityPendingTemp(t *testing.T) {
	for _, query := range []string{"SELECT current_schema", "SELECT current_schema()", "SELECT current_schemas(false)", "SELECT current_schemas(true)"} {
		t.Run(query, func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					c := namespaceConn(t, p, b)
					namespaceExec(t, p, b, c, "SET search_path TO missing_namespace_identity,pg_temp,public")
					if got := namespaceOID(t, p, c); got != 0 {
						t.Fatal("fresh identity", got)
					}
					held, err := c.PrepareContext(p.Ctx, query)
					if err != nil {
						t.Fatal(err)
					}
					defer held.Close()
					if got := namespaceOID(t, p, c); got != 0 {
						t.Fatal("Prepare initialized TEMP", got)
					}
					var got string
					if err := held.QueryRowContext(p.Ctx).Scan(&got); err != nil {
						t.Fatal(err)
					}
					id := namespaceOID(t, p, c)
					name, _ := namespacePair(t, p, c, id)
					want := name
					if query == "SELECT current_schemas(false)" {
						want = "{" + name + ",public}"
					}
					if query == "SELECT current_schemas(true)" {
						want = "{pg_catalog," + name + ",public}"
					}
					if got != want {
						t.Fatal("initializer", got, want)
					}
					namespaceExec(t, p, b, c, "CREATE TEMP TABLE namespace_identity_after_path(i INT)")
					if got := namespaceOID(t, p, c); got != id {
						t.Fatal("DDL changed staged identity", got, id)
					}
				})
			}
		})
	}
	for _, mode := range []string{"rollback", "savepoint", "commit", "read-only"} {
		t.Run(mode, func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					c, peer := namespaceConn(t, p, b), namespaceConn(t, p, b)
					namespaceExec(t, p, b, c, "SET search_path TO pg_temp,public", "BEGIN")
					defer c.ExecContext(p.Ctx, "ROLLBACK")
					if mode == "savepoint" {
						namespaceExec(t, p, b, c, "SAVEPOINT namespace_first_use")
					}
					if mode == "read-only" {
						namespaceExec(t, p, b, c, "SET TRANSACTION READ ONLY")
					}
					var name string
					if err := c.QueryRowContext(p.Ctx, "SELECT current_schema()").Scan(&name); err != nil {
						t.Fatal("first use", err)
					}
					id := namespaceOID(t, p, c)
					actual, _ := namespacePair(t, p, c, id)
					if name != actual {
						t.Fatal("owner path", name, actual)
					}
					// A fresh first-use namespace is not published to another session.
					fixture.RunStep(t, p.Ctx, peer, b.Recorder, fixture.Select("SELECT nspname FROM pg_namespace WHERE oid=$1", []string{"nspname"}, []string{"NAME"}, nil, id))
					if mode == "commit" {
						namespaceExec(t, p, b, c, "COMMIT")
						namespaceValue(t, p, b, peer, "SELECT pg_is_other_temp_schema($1)", "pg_is_other_temp_schema", "BOOL", true, id)
						namespaceExec(t, p, b, c, "BEGIN", "ROLLBACK")
						if got := namespaceOID(t, p, c); got != id {
							t.Fatal("later rollback changed identity", got, id)
						}
					} else {
						if mode == "savepoint" {
							namespaceExec(t, p, b, c, "ROLLBACK TO namespace_first_use")
						} else {
							namespaceExec(t, p, b, c, "ROLLBACK")
						}
						if got := namespaceOID(t, p, c); got != 0 {
							t.Fatal("rollback leaked first use", got)
						}
						fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT nspname FROM pg_namespace WHERE oid=$1", []string{"nspname"}, []string{"NAME"}, nil, id))
						if mode == "savepoint" {
							namespaceExec(t, p, b, c, "COMMIT")
						}
					}
				})
			}
		})
	}
	t.Run("NoDemand", func(t *testing.T) {
		p := fixture.NewScenario(t)
		for _, b := range p.Backends() {
			t.Run(b.Name, func(t *testing.T) {
				c := namespaceConn(t, p, b)
				namespaceExec(t, p, b, c, "SET search_path TO public,pg_temp")
				namespaceValue(t, p, b, c, "SELECT current_schema()", "current_schema", "NAME", "public")
				namespaceValue(t, p, b, c, "SELECT current_schemas(false)", "current_schemas", "_NAME", "{public}")
				namespaceExec(t, p, b, c, "SET search_path TO pg_temp,public")
				namespaceValue(t, p, b, c, "SELECT current_schemas(NULL)", "current_schemas", "_NAME", nil)
				if got := namespaceOID(t, p, c); got != 0 {
					t.Fatal("undemanded first use", got)
				}
			})
		}
	})
	t.Run("FailedDemandAndCancellation", func(t *testing.T) {
		p := fixture.NewScenario(t)
		for _, b := range p.Backends() {
			t.Run(b.Name, func(t *testing.T) {
				c := namespaceConn(t, p, b)
				namespaceExec(t, p, b, c, "SET search_path TO pg_temp,public")
				// Precanceled driver calls are a native adapter contract; lib/pq
				// does not promise to reject a fast command before server execution.
				if !b.PG {
					ctx, cancel := context.WithCancel(p.Ctx)
					cancel()
					if _, err := c.ExecContext(ctx, "SELECT current_schema()"); !errors.Is(err, context.Canceled) {
						t.Fatal("canceled inquiry", err)
					}
					if got := namespaceOID(t, p, c); got != 0 {
						t.Fatal("cancellation initialized TEMP", got)
					}
				}
				// CASE demands the schema inquiry before reaching the failing arm.
				// The denominator is stable runtime input, not a folded constant.
				const q = "SELECT CASE WHEN current_schema() IS NOT NULL THEN 1 / (pg_my_temp_schema()::int - pg_my_temp_schema()::int) ELSE 1 END"
				s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "22012", Message: "division by zero"})
				s.SelectRows = true
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
				if got := namespaceOID(t, p, c); got != 0 {
					t.Fatal("failed statement published TEMP", got)
				}
			})
		}
	})
	t.Run("SameStatementTransition", func(t *testing.T) {
		p := fixture.NewScenario(t)
		for _, b := range p.Backends() {
			t.Run(b.Name, func(t *testing.T) {
				c := namespaceConn(t, p, b)
				namespaceExec(t, p, b, c, "SET search_path TO pg_temp,public")
				if got := namespaceOID(t, p, c); got != 0 {
					t.Fatal("fresh precondition", got)
				}
				// CASE guarantees demand of current_schema before the getter,
				// without imposing target-list evaluation order.
				const q = "SELECT CASE WHEN current_schema() IS NOT NULL THEN pg_my_temp_schema() ELSE 0::oid END AS own"
				var id int64
				if err := c.QueryRowContext(p.Ctx, q).Scan(&id); err != nil {
					t.Fatal(err)
				}
				name, _ := namespacePair(t, p, c, id)
				if got := namespaceOID(t, p, c); got != id {
					t.Fatal("same-statement overlay identity", got, id)
				}
				namespaceValue(t, p, b, c, "SELECT current_schemas(false)", "current_schemas", "_NAME", "{"+name+",public}")
			})
		}
	})
}
