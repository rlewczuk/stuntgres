package reference_test

import (
	"context"
	"database/sql/driver"
	"strings"
	"sync"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

// A genuine physical Open returns late despite cancellation. Successful native
// publication/physical Close must join that late handle, not serialize or leak it.
func postgresFDWPendingControls(t *testing.T, p *fixture.Pair, provider *fixture.ExternalSQLProvider, dump string) {
	t.Helper()
	for _, action := range []string{"reset", "restore", "close"} {
		t.Run("late_open_"+action, func(t *testing.T) {
			if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump)); err != nil {
				t.Fatal(err)
			}
			physical, err := p.NativeConnector.Connect(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer physical.Close()
			opened := make(chan *fixture.ExternalSQLWire, 1)
			canceled := make(chan struct{})
			release := make(chan struct{})
			var releaseOnce sync.Once
			unblock := func() { releaseOnce.Do(func() { close(release) }) }
			provider.ConfigureFaults(func(f *fixture.ExternalSQLFaults) {
				f.AfterOpen = func(ctx context.Context, wire *fixture.ExternalSQLWire) error {
					opened <- wire
					<-ctx.Done()
					close(canceled)
					<-release
					return nil
				}
			})
			defer provider.ConfigureFaults(func(f *fixture.ExternalSQLFaults) { f.AfterOpen = nil })
			queryCtx, cancelQuery := context.WithCancel(p.Ctx)
			defer cancelQuery()
			queryDone := make(chan struct{})
			var queryErr error
			var returned driver.Rows
			go func() {
				defer close(queryDone)
				returned, queryErr = physical.(driver.QueryerContext).QueryContext(queryCtx, "SELECT id FROM pf_table", nil)
			}()
			var wire *fixture.ExternalSQLWire
			defer func() {
				cancelQuery()
				unblock()
				cleanup, cancel := context.WithTimeout(context.Background(), fixture.Liveness)
				defer cancel()
				if wire != nil {
					_ = wire.Cancel(cleanup)
				}
				select {
				case <-queryDone:
					if returned != nil {
						returned.Close()
					}
				case <-cleanup.Done():
					t.Error("late Open query failed to join")
				}
			}()
			select {
			case wire = <-opened:
			case <-queryDone:
				t.Fatal("query ended before physical Open", queryErr)
			case <-p.Ctx.Done():
				t.Fatal(p.Ctx.Err())
			}
			controlDone := make(chan error, 1)
			go func() {
				switch action {
				case "reset":
					controlDone <- stuntgres.Reset(p.Ctx, p.Native)
				case "restore":
					controlDone <- stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump))
				case "close":
					controlDone <- physical.Close()
				}
			}()
			defer func() {
				unblock()
				cleanup, cancel := context.WithTimeout(context.Background(), fixture.Liveness)
				defer cancel()
				select {
				case <-controlDone:
				case <-cleanup.Done():
					t.Error("late Open control failed to join")
				}
			}()
			select {
			case <-canceled:
			case err := <-controlDone:
				controlDone <- err
				t.Fatal("control completed before canceling pending physical Open", err)
			case <-p.Ctx.Done():
				t.Fatal(p.Ctx.Err())
			}
			select {
			case err := <-controlDone:
				controlDone <- err
				t.Fatal("control did not join late Open", err)
			default:
			}
			unblock()
			select {
			case err := <-controlDone:
				controlDone <- err
				if err != nil {
					t.Fatal(err)
				}
			case <-p.Ctx.Done():
				t.Fatal(p.Ctx.Err())
			}
			select {
			case <-queryDone:
			case <-p.Ctx.Done():
				t.Fatal(p.Ctx.Err())
			}
			if queryErr == nil || returned != nil {
				t.Fatal("late stale Open published successful rows", queryErr)
			}
			postgresFDWObserveGone(t, p, wire.Info().BackendPID)
			if wire.Activity().Closes != 1 {
				t.Fatal("late transport was not closed exactly once", wire.Activity())
			}
		})
	}
}
