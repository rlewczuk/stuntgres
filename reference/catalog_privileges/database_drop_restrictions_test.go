package reference_test

import (
	"context"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceDatabaseDropRestrictions(t *testing.T) {
	for _, mode := range []string{"direct", "fresh", "held"} {
		t.Run(mode, func(t *testing.T) {
			const a, b = "dd_sub_target", "dd_sub_peer"
			names := []string{a, b}
			fixture.DatabaseReferenceCleanup(t, &names)
			p := fixture.NewScenario(t)
			for _, backend := range p.Backends() {
				t.Run(backend.Name, func(t *testing.T) {
					var notices []fixture.Diagnostic
					_, admin, record := fixture.NoticeReferencePool(t, p, backend.PG, &notices)
					backend.Recorder = record
					c := &databaseCreateCase{t, p, backend, admin, &names}
					ordinary := "DROP DATABASE " + a
					force := ordinary + " WITH (FORCE)"
					plain, forced := databaseDropPrepare(c, mode, ordinary), databaseDropPrepare(c, mode, force)
					c.create(a, "", false)
					c.create(b, "", false)
					adb, araw, arec := c.pool(a, "")
					adb.SetMaxIdleConns(0)
					bdb, braw, brec := c.pool(b, "")
					bdb.SetMaxIdleConns(0)
					pending := map[string]bool{}
					create := func(db fixture.Executor, rec *fixture.Recorder, name string) {
						t.Helper()
						pending[name] = true
						cleanup := adb
						if name == "dd_sub_peer_only" {
							cleanup = bdb
						}
						t.Cleanup(func() {
							if pending[name] {
								ctx, cancel := context.WithTimeout(context.Background(), fixture.Liveness)
								defer cancel()
								if _, err := cleanup.ExecContext(ctx, "DROP SUBSCRIPTION "+name); err != nil {
									t.Error("subscription cleanup", err)
								}
							}
						})
						fixture.RunStep(t, p.Ctx, db, rec, fixture.Exec("CREATE SUBSCRIPTION "+name+" CONNECTION 'host=invalid password=unused' PUBLICATION dd_publication WITH (connect=false,enabled=false,create_slot=false,slot_name=NONE)", 0))
					}
					create(bdb, brec, "dd_sub_peer_only")
					victim, err := adb.Conn(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					t.Cleanup(func() { victim.Close() })
					for i, name := range []string{"dd_sub_one", "dd_sub_two"} {
						create(victim, arec, name)
						detail := "There is 1 subscription."
						if i == 1 {
							detail = "There are 2 subscriptions."
						}
						want := databaseCreateDiagnostic("55006", "database \""+a+"\" is being used by logical replication subscription")
						want.Detail = detail
						for range 2 {
							plain.run(c, fixture.ErrorDiagnostic(ordinary, want))
							forced.run(c, fixture.ErrorDiagnostic(force, want))
							fixture.RunStep(t, p.Ctx, victim, arec, fixture.Select("SELECT subname,subenabled,subslotname FROM pg_subscription WHERE subdbid=(SELECT oid FROM pg_database WHERE datname=current_database()) ORDER BY subname", []string{"subname", "subenabled", "subslotname"}, []string{"NAME", "BOOL", "NAME"}, func() [][]any {
								if i == 0 {
									return [][]any{{"dd_sub_one", false, nil}}
								}
								return [][]any{{"dd_sub_one", false, nil}, {"dd_sub_two", false, nil}}
							}()))
							fixture.RunStep(t, p.Ctx, victim, arec, fixture.Select("SELECT 7 AS alive", []string{"alive"}, []string{"INT4"}, [][]any{{int64(7)}}))
						}
					}
					// The refusal outlives its creating physical connection and still precedes
					// ordinary occupancy. No disabled/NONE declaration is treated as absent.
					victim.Close()
					fixture.DatabaseReferenceOwners(t, p, admin, record, a, 0)
					want := databaseCreateDiagnostic("55006", "database \""+a+"\" is being used by logical replication subscription")
					want.Detail = "There are 2 subscriptions."
					forced.run(c, fixture.ErrorDiagnostic(force, want))
					for _, name := range []string{"dd_sub_one", "dd_sub_two"} {
						fixture.RunStep(t, p.Ctx, adb, arec, fixture.Exec("DROP SUBSCRIPTION "+name, 0))
						pending[name] = false
					}
					fixture.DatabaseReferenceOwners(t, p, admin, record, a, 0)
					forced.run(c, fixture.Exec(force, 0))
					c.absent(a)
					fixture.RunStep(t, p.Ctx, bdb, brec, fixture.Select("SELECT subname,subenabled FROM pg_subscription WHERE subname='dd_sub_peer_only'", []string{"subname", "subenabled"}, []string{"NAME", "BOOL"}, [][]any{{"dd_sub_peer_only", false}}))
					fixture.RunStep(t, p.Ctx, bdb, brec, fixture.Exec("DROP SUBSCRIPTION dd_sub_peer_only", 0))
					pending["dd_sub_peer_only"] = false
					databaseDropClose(t, adb, araw, bdb, braw)
				})
			}
		})
	}
}
