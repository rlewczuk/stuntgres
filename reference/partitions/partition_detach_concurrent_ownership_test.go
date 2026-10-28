package reference_test

import (
	"bytes"
	"database/sql"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func detachConcurrentOwnership(t *testing.T) {
	for _, authority := range []string{"parent_owner", "child_owner", "table_grant", "schema_denied"} {
		for _, held := range []bool{false, true} {
			t.Run("ownership/"+authority+"/"+map[bool]string{false: "direct", true: "held"}[held], func(t *testing.T) {
				fixture.RoleReferenceCleanup(t, []string{"dc_actor"})
				fixture.SchemaReferenceCleanup(t, "dc_private")
				detachConcurrentScenario(t, func(p *fixture.Pair, b fixture.Backend) {
					c := detachConcurrentConn(t, p, b)
					run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
					if authority == "schema_denied" {
						run(fixture.Exec("CREATE SCHEMA dc_private", 0))
						run(fixture.Exec("SET search_path=dc_private,public", 0))
					}
					detachConcurrentSetup(run)
					run(fixture.Exec("CREATE ROLE dc_actor", 0))
					if b.PG {
						run(fixture.Exec("GRANT USAGE ON SCHEMA public TO dc_actor", 0))
					} else {
						run(fixture.Exec("ALTER SCHEMA public OWNER TO dc_actor", 0))
					}
					parent := "dc_parent"
					switch authority {
					case "parent_owner":
						run(fixture.Exec("ALTER TABLE dc_parent OWNER TO dc_actor", 0))
						run(fixture.Exec("GRANT SELECT,INSERT ON dc_leave TO dc_actor", 0))
					case "child_owner":
						run(fixture.Exec("ALTER TABLE dc_leave OWNER TO dc_actor", 0))
					case "table_grant":
						run(fixture.Exec("GRANT ALL ON dc_parent,dc_leave TO dc_actor", 0))
					case "schema_denied":
						parent = "dc_private.dc_parent"
					}
					q := "ALTER TABLE " + parent + " DETACH PARTITION dc_leave CONCURRENTLY"
					var e fixture.Executor = c
					if held {
						e = partitionDetachHold(t, p, c, q)
					}
					run(fixture.Exec("SET ROLE dc_actor", 0))
					defer c.ExecContext(p.Ctx, "RESET ROLE")
					want := fixture.Diagnostic{Code: "42501", Message: "must be owner of table dc_parent"}
					if authority == "schema_denied" {
						want = fixture.Diagnostic{Code: "42501", Message: "permission denied for schema dc_private"}
					}
					s := fixture.ImplicitError(q, want)
					if authority == "parent_owner" {
						s = fixture.Exec(q, 0)
					}
					fixture.RunStep(t, p.Ctx, e, b.Recorder, s)
					run(fixture.Exec("RESET ROLE", 0))
					run(detachConcurrentGraph(parent, "dc_leave", authority != "parent_owner", false))
					run(detachConcurrentRows("dc_leave", 2))
					if authority == "parent_owner" {
						run(fixture.Exec("SET ROLE dc_actor", 0))
						run(detachConcurrentRows("dc_leave", 2))
						run(fixture.Exec("INSERT INTO dc_leave VALUES(3,'acl')", 1))
						run(fixture.Exec("RESET ROLE", 0))
					}
				})
			})
		}
	}
}

func detachConcurrentReplacement(t *testing.T) {
	for _, mode := range []string{"reset", "restore", "recreate"} {
		t.Run("replacement/"+mode, func(t *testing.T) {
			detachConcurrentScenario(t, func(p *fixture.Pair, b fixture.Backend) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
				detachConcurrentSetup(run)
				detachConcurrentPending(t, p, b, true)
				if b.PG {
					return
				} // Native lifecycle APIs have no oracle counterpart.
				var stream bytes.Buffer
				if err := stuntgres.Dump(p.Ctx, p.Native, &stream); err != nil {
					t.Fatal(err)
				}
				// An unrelated stable snapshot holds FINALIZE's virtual completion barrier.
				tx, err := b.DB.BeginTx(p.Ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { tx.Rollback() })
				fixture.RunStep(t, p.Ctx, tx, b.Recorder, detachConcurrentRows("dc_keep", 1))
				w := detachConcurrentStart(t, p, b, "ALTER TABLE dc_parent DETACH PARTITION dc_leave FINALIZE", true)
				fixture.VirtualCompletionPending(t, p, b, 0, 0, w.finished)
				if mode == "restore" {
					if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(stream.Bytes())); err != nil {
						t.Fatal(err)
					}
				} else {
					if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
						t.Fatal(err)
					}
					if mode == "recreate" {
						detachConcurrentSetup(run)
					}
				}
				o := w.join()
				if o.Err == nil || o.Result != nil {
					t.Fatal("old utility crossed replacement epoch", o)
				}
				if mode == "restore" {
					detachConcurrentState(run, true, true)
				}
				if mode == "recreate" {
					detachConcurrentState(run, true, false)
				}
			})
		})
	}
}
