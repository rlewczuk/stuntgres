package reference_test

import (
	"bytes"
	"strings"
	"sync"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceDatabaseDropControls(t *testing.T) {
	for _, mode := range []string{"direct", "fresh", "held"} {
		t.Run(mode, func(t *testing.T) {
			const a, b = "dd_control_target", "dd_control_peer"
			names := []string{a, b}
			fixture.DatabaseReferenceCleanup(t, &names)
			p := fixture.NewScenario(t)
			for _, backend := range p.Backends() {
				t.Run(backend.Name, func(t *testing.T) {
					var notices []fixture.Diagnostic
					_, admin, record := fixture.NoticeReferencePool(t, p, backend.PG, &notices)
					backend.Recorder = record
					c := &databaseCreateCase{t, p, backend, admin, &names}
					query := "DROP DATABASE " + a
					route := databaseDropPrepare(c, mode, query)
					c.create(a, "", false)
					c.create(b, "", false)
					adb, araw, arec := c.pool(a, "")
					araw.SetMaxIdleConns(0)
					bdb, braw, brec := c.pool(b, "")
					for _, target := range []struct {
						db     fixture.Executor
						record *fixture.Recorder
						value  int64
					}{{adb, arec, 11}, {bdb, brec, 22}} {
						fixture.RunStep(t, p.Ctx, target.db, target.record, fixture.Exec("CREATE TABLE control_value(v integer)", 0))
						fixture.RunStep(t, p.Ctx, target.db, target.record, fixture.Exec("INSERT INTO control_value VALUES($1)", 1, target.value))
					}
					var reader *fixture.CatalogControlGatedReader
					var writer *fixture.CatalogControlGatedWriter
					var restoreDone, dumpDone chan error
					var release sync.Once
					var joined bool
					if !backend.PG {
						reader = &fixture.CatalogControlGatedReader{Started: make(chan struct{}), Release: make(chan struct{})}
						writer = &fixture.CatalogControlGatedWriter{Started: make(chan struct{}), Release: make(chan struct{})}
						restoreDone, dumpDone = make(chan error, 1), make(chan error, 1)
						t.Cleanup(func() {
							release.Do(func() { close(reader.Release); close(writer.Release) })
							if !joined {
								<-restoreDone
								<-dumpDone
							}
						})
						go func() { restoreDone <- stuntgres.Restore(p.Ctx, araw, reader) }()
						go func() { dumpDone <- stuntgres.Dump(p.Ctx, araw, writer) }()
						select {
						case <-reader.Started:
						case <-p.Ctx.Done():
							t.Fatal(p.Ctx.Err())
						}
						select {
						case <-writer.Started:
						case <-p.Ctx.Done():
							t.Fatal(p.Ctx.Err())
						}
					}
					// Both native captures have released their real borrower before external
					// I/O. Closing SQL owners must therefore leave exactly zero admissions.
					databaseDropClose(t, adb)
					fixture.DatabaseReferenceOwners(t, p, admin, record, a, 0)
					route.run(c, fixture.Exec(query, 0))
					c.absent(a)
					c.create(a, "", false)
					fresh, freshRaw, freshRecord := c.pool(a, "")
					fixture.RunStep(t, p.Ctx, fresh, freshRecord, fixture.Exec("CREATE TABLE control_value(v integer)", 0))
					fixture.RunStep(t, p.Ctx, fresh, freshRecord, fixture.Exec("INSERT INTO control_value VALUES(44)", 1))
					fixture.RunStep(t, p.Ctx, bdb, brec, fixture.Select("SELECT v FROM control_value", []string{"v"}, []string{"INT4"}, [][]any{{int64(22)}}))
					if !backend.PG {
						release.Do(func() { close(reader.Release); close(writer.Release) })
						restoreErr, dumpErr := <-restoreDone, <-dumpDone
						joined = true
						if restoreErr == nil || restoreErr.Error() != "stuntgres: database retired before control publication" {
							t.Fatal("stale Restore publication", restoreErr)
						}
						if dumpErr != nil {
							t.Fatal("detached Dump capture could not finish I/O", dumpErr)
						}
						if writer.Output.Len() == 0 {
							t.Fatal("detached capture lost its script")
						}
						d := stuntgres.NewDriver()
						connector, err := d.OpenConnector("dd_detached_dump")
						if err != nil {
							t.Fatal(err)
						}
						replay, replayRaw, replayRecord := fixture.DatabaseConnectorScenarioPool(t, connector, false)
						if err := stuntgres.Restore(p.Ctx, replayRaw, bytes.NewReader(writer.Output.Bytes())); err != nil {
							t.Fatal(err)
						}
						fixture.RunStep(t, p.Ctx, replay, replayRecord, fixture.Select("SELECT v FROM control_value", []string{"v"}, []string{"INT4"}, [][]any{{int64(11)}}))
						var peerDump bytes.Buffer
						if err := stuntgres.Dump(p.Ctx, braw, &peerDump); err != nil {
							t.Fatal(err)
						}
						if err := stuntgres.Restore(p.Ctx, braw, strings.NewReader(peerDump.String())); err != nil {
							t.Fatal(err)
						}
						databaseDropClose(t, replay, replayRaw)
					}
					fixture.RunStep(t, p.Ctx, fresh, freshRecord, fixture.Select("SELECT v FROM control_value", []string{"v"}, []string{"INT4"}, [][]any{{int64(44)}}))
					fixture.RunStep(t, p.Ctx, fresh, freshRecord, fixture.Select("SELECT relname FROM pg_class WHERE relname='replaced'", []string{"relname"}, []string{"NAME"}, nil))
					databaseDropClose(t, araw, fresh, freshRaw, bdb, braw)
				})
			}
		})
	}
}
