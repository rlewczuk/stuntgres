package core_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"testing"
	"time"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/visibility"
)

// Canonical session end cancels admitted operations, drains their leases, then
// retires the actual accepted owner. Never race a raw artifact.Retire call with
// unsynchronized readers: that is not an engine-supported ownership transition.
func TestSnapshotSyncAcceptedDefaultLifetime(t *testing.T) {
	for _, afterCapture := range []bool{false, true} {
		t.Run(fmt.Sprintf("end-after-capture=%v", afterCapture), func(t *testing.T) {
			for range 16 {
				e := engine.New()
				s := enginetest.StartSession(t, e, nil)
				for _, query := range []string{
					"CREATE TABLE snapshot_lifetime_unread(id integer)",
					"CREATE FUNCTION snapshot_lifetime_callback() RETURNS text STABLE LANGUAGE plpgsql AS $$BEGIN RETURN pg_export_snapshot(); END$$",
					"CREATE FUNCTION snapshot_lifetime_default() RETURNS text STABLE RETURN snapshot_lifetime_callback()",
					"CREATE TABLE snapshot_lifetime_rows(v text DEFAULT snapshot_lifetime_default())",
					"BEGIN",
				} {
					enginetest.SessionSQL(t, s, query)
				}
				cycle := s.NewProtocolCycle()
				enginetest.PreparedFoldProtocolCommand(t, e, s, cycle, func(scope *core.StatementScope) error {
					_, err := scope.ParseProtocol("lifetime", "INSERT INTO snapshot_lifetime_rows DEFAULT VALUES RETURNING v", nil)
					return err
				})
				enginetest.PreparedFoldProtocolCommand(t, e, s, cycle, func(scope *core.StatementScope) error {
					_, err := scope.BindProtocolPortal("lifetime", "lifetime", &enginetest.ProtocolReader{Data: []byte{0, 0, 0, 0, 0, 0}})
					return err
				})
				binding := core.PayloadOf(s).PreparedSQL.Binding("lifetime")
				owner := reflect.ValueOf(binding).Elem().FieldByName("defaults").Elem()
				if owner.FieldByName("entries").Len() != 1 || owner.FieldByName("retired").Bool() {
					t.Fatal("missing live accepted lifetime owner")
				}
				enginetest.SessionSQL(t, s, "CREATE OR REPLACE FUNCTION snapshot_lifetime_default() RETURNS text STABLE RETURN 'quiet'::text")
				op, _, err := s.AcquireCommand(t.Context())
				if err != nil {
					t.Fatal(err)
				}
				scope, err := e.StartProtocolCommand(op, cycle)
				if err != nil {
					op.Done()
					t.Fatal(err)
				}
				start, captured, executionDone := make(chan struct{}), make(chan struct{}), make(chan struct{})
				var once sync.Once
				var execution error
				var token string
				var retainedRoot uintptr
				var retainedRoots map[ids.ObjectAddress]visibility.Root
				var complete bool
				var workers sync.WaitGroup
				workers.Add(2)
				go func() {
					defer workers.Done()
					defer close(executionDone)
					defer op.Done()
					<-start
					_, execution = scope.ExecuteProtocolPortal("lifetime", 0, func(fields []session.ProtocolField) error {
						if len(fields) != 1 || fields[0].Null {
							return fmt.Errorf("accepted export lost its text result")
						}
						token = string(fields[0].Bytes)
						roots, _, snapshot, frontier := core.ScopeExportCapture(scope)
						saved, value, cut := core.EngineExportCapture(e, token)
						retainedRoot = reflect.ValueOf(roots).Pointer()
						retainedRoots = roots
						complete = roots != nil && snapshot != nil && retainedRoot == reflect.ValueOf(saved).Pointer() && snapshot == value && frontier == cut
						once.Do(func() { close(captured) })
						if afterCapture {
							<-s.EndRequested()
						}
						return nil
					}, nil, nil, core.CopyProtocolHooks{})
					execution = scope.Finish(execution)
				}()
				go func() {
					defer workers.Done()
					<-start
					if afterCapture {
						select {
						case <-captured:
						case <-executionDone:
						}
					}
					s.RequestEnd(session.Terminated)
				}()
				close(start)
				workers.Wait()
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				err = s.WaitEnd(ctx)
				cancel()
				if err != nil {
					t.Fatal("joining canonical accepted-owner retirement", err)
				}
				if execution != nil && !errors.Is(execution, context.Canceled) && !errors.Is(execution, ids.ErrCatalogReplaced) && execution.Error() != "stuntgres: inactive operation" && execution.Error() != "stuntgres: session unavailable" {
					t.Fatal("session end did not return an existing operation fence", execution)
				}
				if token != "" && (!complete || retainedRoot == 0 || len(retainedRoots) == 0 || reflect.ValueOf(retainedRoots).Pointer() != retainedRoot) {
					t.Fatal("concurrent source end observed half-captured export backing")
				}
				if afterCapture && token == "" {
					t.Fatal("admitted predecessor did not capture before deterministic retirement", execution)
				}
				if !binding.Retired || !owner.FieldByName("retired").Bool() || core.EngineExportCount(e) != 0 {
					t.Fatal("canonical session end leaked or resurrected accepted/export ownership")
				}
				if next, _, err := s.AcquireCommand(t.Context()); err == nil {
					next.Done()
					t.Fatal("terminal accepted owner admitted a new operation")
				}
			}
		})
	}
}
