package reference_test

import (
	"context"
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceDatabaseDropPreparedLifetime(t *testing.T) {
	for _, mode := range []string{"direct", "fresh", "held"} {
		t.Run(mode, func(t *testing.T) {
			const name = "dd_prepared_target"
			names := []string{name}
			fixture.DatabaseReferenceCleanup(t, &names)
			p := fixture.NewScenario(t)
			for _, backend := range p.Backends() {
				t.Run(backend.Name, func(t *testing.T) {
					var notices []fixture.Diagnostic
					_, admin, record := fixture.NoticeReferencePool(t, p, backend.PG, &notices)
					backend.Recorder = record
					c := &databaseCreateCase{t, p, backend, admin, &names}
					ordinary := "DROP DATABASE " + name
					force := ordinary + " WITH (FORCE)"
					plain, forced := databaseDropPrepare(c, mode, ordinary), databaseDropPrepare(c, mode, force)
					c.create(name, "", false)
					setup, setupRaw, sr := c.pool(name, "")
					fixture.RunStep(t, p.Ctx, setup, sr, fixture.Exec("CREATE TABLE prepared_value(v integer)", 0))
					databaseDropClose(t, setup, setupRaw)
					completion, _, cr := c.pool(name, "")
					completion.SetMaxIdleConns(0)
					pending := map[string]bool{}
					for i := 1; i <= 2; i++ {
						gid := fmt.Sprintf("dd_prepared_gid_%d", i)
						t.Cleanup(func() {
							if pending[gid] {
								ctx, cancel := context.WithTimeout(context.Background(), fixture.Liveness)
								defer cancel()
								if _, err := completion.ExecContext(ctx, "ROLLBACK PREPARED '"+gid+"'"); err != nil {
									t.Error("prepared cleanup", err)
								}
							}
						})
						db, raw, rec := c.pool(name, "")
						db.SetMaxIdleConns(0)
						origin, err := db.Conn(p.Ctx)
						if err != nil {
							t.Fatal(err)
						}
						t.Cleanup(func() { origin.Close() })
						for _, step := range []fixture.Step{fixture.Exec("BEGIN", 0), fixture.Exec("INSERT INTO prepared_value VALUES($1)", 1, int64(i+10)), fixture.Exec("PREPARE TRANSACTION '"+gid+"'", 0)} {
							fixture.RunStep(t, p.Ctx, origin, rec, step)
						}
						pending[gid] = true
						origin.Close()
						databaseDropClose(t, db, raw)
						fixture.DatabaseReferenceOwners(t, p, admin, record, name, 0)
						detail := "There is 1 prepared transaction using the database."
						if i == 2 {
							detail = "There are 2 prepared transactions using the database."
						}
						busy := databaseCreateDiagnostic("55006", "database \""+name+"\" is being accessed by other users")
						busy.Detail = detail
						specific := busy
						specific.Message = "database \"" + name + "\" is being used by prepared transactions"
						for range 2 {
							plain.run(c, fixture.ErrorDiagnostic(ordinary, busy))
							forced.run(c, fixture.ErrorDiagnostic(force, specific))
						}
						// Query the actual target's prepared authority; native
						// introspection is selected-database scoped, as is completion.
						fixture.RunStep(t, p.Ctx, completion, cr, fixture.Select("SELECT count(*) FROM pg_prepared_xacts WHERE database=$1", []string{"count"}, []string{"INT8"}, [][]any{{int64(i)}}, name))
						live, liveRaw, lr := c.pool(name, "")
						live.SetMaxIdleConns(0)
						victim, err := live.Conn(p.Ctx)
						if err != nil {
							t.Fatal(err)
						}
						t.Cleanup(func() { victim.Close() })
						fixture.DatabaseReferenceOwners(t, p, admin, record, name, 1)
						mixed := busy
						mixed.Detail = fmt.Sprintf("There are 1 other session(s) and %d prepared transaction(s) using the database.", i)
						plain.run(c, fixture.ErrorDiagnostic(ordinary, mixed))
						forced.run(c, fixture.ErrorDiagnostic(force, specific))
						fixture.RunStep(t, p.Ctx, victim, lr, fixture.Select("SELECT v FROM prepared_value ORDER BY v", []string{"v"}, []string{"INT4"}, nil))
						victim.Close()
						databaseDropClose(t, live, liveRaw)
					}
					fixture.RunStep(t, p.Ctx, completion, cr, fixture.Exec("COMMIT PREPARED 'dd_prepared_gid_1'", 0))
					pending["dd_prepared_gid_1"] = false
					fixture.RunStep(t, p.Ctx, completion, cr, fixture.Exec("ROLLBACK PREPARED 'dd_prepared_gid_2'", 0))
					pending["dd_prepared_gid_2"] = false
					fixture.RunStep(t, p.Ctx, completion, cr, fixture.Select("SELECT v FROM prepared_value", []string{"v"}, []string{"INT4"}, [][]any{{int64(11)}}))
					fixture.DatabaseReferenceOwners(t, p, admin, record, name, 0)
					forced.run(c, fixture.Exec(force, 0))
					c.absent(name)
				})
			}
		})
	}
}
