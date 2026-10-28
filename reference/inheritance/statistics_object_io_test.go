package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceStatisticsObjectPreCanceledUtilities(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
			run(fixture.Exec("CREATE TABLE stats_t(a INT,b INT)", 0))
			run(fixture.Exec("INSERT INTO stats_t VALUES(77,78)", 1))
			run(fixture.Exec("CREATE STATISTICS preserved_s (dependencies) ON b,a FROM stats_t", 0))
			if !b.PG {
				var before bytes.Buffer
				if err := stuntgres.Dump(p.Ctx, p.Native, &before); err != nil {
					t.Fatal(err)
				}
				canceled, cancel := context.WithCancel(p.Ctx)
				cancel()
				for _, query := range []string{"CREATE STATISTICS canceled_s ON a,b FROM stats_t", "DROP STATISTICS preserved_s"} {
					stmt, err := b.DB.PrepareContext(p.Ctx, query)
					if err != nil {
						t.Fatal(err)
					}
					for _, prepared := range []bool{false, true} {
						var result sql.Result
						if prepared {
							result, err = stmt.ExecContext(canceled)
						} else {
							result, err = b.DB.ExecContext(canceled, query)
						}
						if result != nil || !errors.Is(err, context.Canceled) {
							stmt.Close()
							t.Fatal("pre-canceled statistics utility", result, err)
						}
						run(statisticsRows([]any{"preserved_s", "1 2", "{f}", nil, nil}))
					}
					if err = stmt.Close(); err != nil {
						t.Fatal(err)
					}
				}
				var after bytes.Buffer
				if err := stuntgres.Dump(p.Ctx, p.Native, &after); err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(before.Bytes(), after.Bytes()) {
					t.Fatal("pre-canceled utility changed committed definitions/state")
				}
			}
			run(statisticsRows([]any{"preserved_s", "1 2", "{f}", nil, nil}))
			run(fixture.Select("SELECT a,b FROM stats_t", []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(77), int64(78)}}))
		})
	}
}

func TestReferenceStatisticsObjectExternalIO(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
			run(fixture.Exec("CREATE TABLE stats_t(a INT,b INT)", 0))
			run(fixture.Exec("INSERT INTO stats_t VALUES(77,78)", 1))
			run(fixture.Exec("CREATE STATISTICS io_s (ndistinct) ON b,a FROM stats_t", 0))
			run(statisticsRows([]any{"io_s", "1 2", "{d}", nil, nil}))
			if b.PG {
				return
			} // External native controls have no PostgreSQL equivalent.
			for _, dump := range []bool{true, false} {
				func() {
					ctx, cancel := context.WithCancel(p.Ctx)
					defer cancel()
					started, release := make(chan struct{}), make(chan struct{})
					writer := &fixture.CatalogControlGatedWriter{Started: started, Release: release}
					reader := &fixture.CatalogControlGatedReader{Started: started, Release: release}
					finished := make(chan error, 1)
					go func() {
						if dump {
							finished <- stuntgres.Dump(ctx, p.Native, writer)
						} else {
							finished <- stuntgres.Restore(ctx, p.Native, reader)
						}
					}()
					released, joined := false, false
					defer func() {
						cancel()
						if !released {
							close(release)
						}
						if !joined {
							<-finished
						}
					}()
					select {
					case <-started:
					case <-ctx.Done():
						t.Fatal("control did not enter external I/O", ctx.Err())
					}
					operation := make(chan error, 1)
					go func() {
						var kind string
						err := p.Native.QueryRowContext(ctx, "SELECT stxkind FROM pg_statistic_ext WHERE stxname='io_s'").Scan(&kind)
						want := "{d}"
						replacement := "mcv"
						if !dump {
							want = "{m}"
							replacement = "ndistinct"
						}
						if err == nil && kind != want {
							err = fmt.Errorf("statistics capture during I/O: got %q want %q", kind, want)
						}
						if err == nil {
							_, err = p.Native.ExecContext(ctx, "DROP STATISTICS io_s")
						}
						if err == nil {
							_, err = p.Native.ExecContext(ctx, "CREATE STATISTICS io_s ("+replacement+") ON a,b FROM stats_t")
						}
						operation <- err
					}()
					operationJoined := false
					defer func() {
						if !operationJoined {
							cancel()
							<-operation
						}
					}()
					select {
					case err := <-operation:
						operationJoined = true
						if err != nil {
							t.Fatal("statistics utility during external I/O", err)
						}
					case <-ctx.Done():
						t.Fatal("external control held target locks", ctx.Err())
					}
					// A catalog read and real statistics DROP/CREATE completed while I/O was
					// still gated. Dump owns its old snapshot; Restore has not replaced target.
					close(release)
					released = true
					err := <-finished
					joined = true
					if err != nil {
						t.Fatal(err)
					}
					if dump {
						independent := fixture.CatalogControlPool(t, stuntgres.NewDriver(), "statistics_io_copy")
						if err = stuntgres.Restore(p.Ctx, independent, bytes.NewReader(writer.Output.Bytes())); err != nil {
							t.Fatal(err)
						}
						fixture.CatalogControlTuples(t, independent, "SELECT stxkeys,stxkind FROM pg_statistic_ext WHERE stxname='io_s'", [][]any{{"1 2", "{d}"}})
						fixture.CatalogControlTuples(t, independent, "SELECT a,b FROM stats_t", [][]any{{int64(77), int64(78)}})
						run(statisticsRows([]any{"io_s", "1 2", "{m}", nil, nil}))
					} else {
						fixture.CatalogControlTuples(t, p.Native, "SELECT stxname FROM pg_statistic_ext WHERE stxname='io_s'", nil)
						fixture.CatalogControlTuples(t, p.Native, "SELECT v FROM replaced", [][]any{{int64(99)}})
					}
				}()
			}
		})
	}
}
