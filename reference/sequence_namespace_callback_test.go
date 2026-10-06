//go:build reference

package reference_test

import (
	"database/sql"
	"testing"
)

func TestReferenceSequencePreparedNamespaceCallback(t *testing.T) {
	for _, event := range []string{"Create", "Rename", "ACL"} {
		t.Run(event, func(t *testing.T) {
			p := newReferencePair(t)
			backends := []struct {
				name string
				db   *sql.DB
			}{{"native", p.memory}, {"postgres", p.postgres}}
			if event == "ACL" {
				backends = backends[1:] // Native ACL witness uses the real internal SetObjectACL seam.
			}
			for _, backend := range backends {
				t.Run(backend.name, func(t *testing.T) {
					c, err := backend.db.Conn(p.ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer c.Close()
					exec := func(q string) { t.Helper(); sequenceProbeExec(t, p.ctx, c, q) }
					if backend.name == "postgres" {
						defer func() {
							exec("SET search_path TO public")
							for _, name := range []string{"a", "b", "unrelated", "callback_created", "callback_renamed"} {
								exec("DROP SCHEMA IF EXISTS " + name + " CASCADE")
							}
						}()
					}
					for _, q := range []string{"CREATE SCHEMA a", "CREATE SCHEMA b", "CREATE SCHEMA unrelated", "CREATE SEQUENCE b.s START 10 CACHE 1", "SET search_path TO a,b"} {
						exec(q)
					}
					held, err := c.PrepareContext(p.ctx, "SELECT nextval('s')")
					if err != nil {
						t.Fatal(err)
					}
					defer held.Close()
					value := func(want int64) {
						t.Helper()
						var got int64
						if err := held.QueryRowContext(p.ctx).Scan(&got); err != nil || got != want {
							t.Fatal("held sequence identity", got, want, err)
						}
					}
					value(10)
					exec("CREATE SEQUENCE a.s START 100 CACHE 1")
					value(11)
					switch event {
					case "Create":
						exec("CREATE SCHEMA callback_created")
					case "Rename":
						exec("ALTER SCHEMA unrelated RENAME TO callback_renamed")
					case "ACL":
						exec("GRANT USAGE ON SCHEMA unrelated TO PUBLIC")
					}
					value(100)
					sequenceProbeValue(t, p.ctx, c, "SELECT last_value FROM b.s", 11)
					sequenceProbeValue(t, p.ctx, c, "SELECT last_value FROM a.s", 100)
					sequenceProbeValue(t, p.ctx, c, "SELECT nextval('s')", 101)
					sequenceProbeValue(t, p.ctx, c, "SELECT nextval('b.s')", 12)
				})
			}
		})
	}
}
