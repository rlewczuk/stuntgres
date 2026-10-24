package exec

import (
	"context"
	"database/sql/driver"
	"errors"
	"reflect"
	"sync"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

type asyncUnitRequest struct {
	ready                      chan struct{}
	row                        catalog.ForeignRow
	present                    bool
	failure, closeFailure      error
	completes, closes, cancels int
	cancelOnce, closeOnce      sync.Once
	cancelHook, closeHook      func()
}

func (r *asyncUnitRequest) Ready() <-chan struct{} { return r.ready }
func (r *asyncUnitRequest) Complete(context.Context) (catalog.ForeignRow, bool, error) {
	r.completes++
	return r.row, r.present, r.failure
}
func (r *asyncUnitRequest) Cancel() {
	r.cancelOnce.Do(func() {
		r.cancels++
		if r.cancelHook != nil {
			r.cancelHook()
		}
	})
}
func (r *asyncUnitRequest) Close(ctx context.Context) error {
	r.closeOnce.Do(func() {
		r.closes++
		if r.closeHook != nil {
			r.closeHook()
		}
	})
	return r.closeFailure
}
func asyncUnitReady(row catalog.ForeignRow, present bool, err error) *asyncUnitRequest {
	r := &asyncUnitRequest{ready: make(chan struct{}), row: row, present: present, failure: err}
	close(r.ready)
	return r
}

type asyncUnitScan struct {
	catalog.ForeignScan
	request        catalog.ForeignAsyncRequest
	requestError   error
	requests, ends int
	endHook        func(context.Context)
}

func (s *asyncUnitScan) RequestForeignScan(ctx context.Context) (catalog.ForeignAsyncRequest, error) {
	s.requests++
	return s.request, s.requestError
}
func (s *asyncUnitScan) EndForeignScan(ctx context.Context) error {
	s.ends++
	if s.endHook != nil {
		s.endHook(ctx)
	}
	return nil
}
func asyncUnitType(t *testing.T, id sqltype.ID) *sqltype.Descriptor {
	t.Helper()
	for _, typ := range sqltype.Builtins() {
		if typ.ID() == id {
			return typ
		}
	}
	t.Fatalf("missing builtin %v", id)
	return nil
}
func asyncUnitExecution(scan catalog.ForeignScan) *ForeignExecution {
	return &ForeignExecution{Scan: scan, async: foreignAsyncState{capable: true, importRow: func(context.Context, catalog.ForeignRow) ([]sqltype.Value, error) { return []sqltype.Value{}, nil }}}
}

func TestFDWAsyncProtocol(t *testing.T) {
	t.Run("pending_zero_width_eof", func(t *testing.T) {
		request := &asyncUnitRequest{ready: make(chan struct{}), present: true}
		scan := &asyncUnitScan{request: request}
		e := asyncUnitExecution(scan)
		if err := e.requestAsync(context.Background()); err != nil {
			t.Fatal(err)
		}
		if err := e.settleAsync(context.Background(), false); err != errForeignPending || request.completes != 0 {
			t.Fatal("pending treated as completion", err)
		}
		if err := e.requestAsync(context.Background()); err != nil || scan.requests != 1 {
			t.Fatal("duplicate outstanding request", err)
		}
		close(request.ready)
		if err := e.settleAsync(context.Background(), false); err != nil || !e.async.buffered || e.async.eof || request.completes != 1 || request.closes != 1 {
			t.Fatal("zero-width row lost", err)
		}
		if err := e.requestAsync(context.Background()); err != nil || scan.requests != 1 {
			t.Fatal("unconsumed row requested again", err)
		}
		e.async.values, e.async.buffered = nil, false
		eof := asyncUnitReady(nil, false, nil)
		scan.request = eof
		if err := e.requestAsync(context.Background()); err != nil {
			t.Fatal(err)
		}
		if err := e.settleAsync(context.Background(), false); err != nil || !e.async.eof || e.async.buffered {
			t.Fatal("EOF", err)
		}
	})
	t.Run("owned_borrowed_cells", func(t *testing.T) {
		text := asyncUnitType(t, sqltype.Text)
		f := &ForeignQuerySource{Columns: []catalog.Column{{Name: "v", Usage: text.Usage()}}, ImportValue: func(ctx context.Context, v driver.Value, usage sqltype.Usage, input sqltype.InputEnvironment) (sqltype.Value, error) {
			return usage.Type.InputWithEnvironment(ctx, v.(string), usage.Typmod, input)
		}}
		scratch := []byte("stable")
		request := asyncUnitReady(catalog.ForeignRow{scratch}, true, nil)
		request.closeHook = func() {
			for i := range scratch {
				scratch[i] = '!'
			}
		}
		scan := &asyncUnitScan{request: request}
		e := asyncUnitExecution(scan)
		e.async.importRow = func(ctx context.Context, row catalog.ForeignRow) ([]sqltype.Value, error) {
			return f.ImportValues(ctx, row, nil, sqltype.InputEnvironment{}, sqltype.OutputEnvironment{}, nil)
		}
		if err := e.requestAsync(context.Background()); err != nil {
			t.Fatal(err)
		}
		if err := e.settleAsync(context.Background(), false); err != nil {
			t.Fatal(err)
		}
		want, err := text.Input(context.Background(), "stable", -1)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(e.async.values, []sqltype.Value{want}) {
			t.Fatal("Close mutated imported result")
		}
	})
	t.Run("errors_and_partial_request", func(t *testing.T) {
		primary := &sqlstate.Error{Code: "42P01", Message: "real source failure"}
		request := asyncUnitReady(nil, false, primary)
		request.closeFailure = errors.New("cleanup")
		scan := &asyncUnitScan{request: request}
		e := asyncUnitExecution(scan)
		if err := e.requestAsync(context.Background()); err != nil {
			t.Fatal(err)
		}
		if err := e.settleAsync(context.Background(), false); err != primary {
			t.Fatal("primary identity lost", err)
		}
		scan.requestError = primary
		partial := &asyncUnitRequest{ready: make(chan struct{})}
		scan.request = partial
		if err := e.requestAsync(context.Background()); err != primary || e.async.request != partial {
			t.Fatal("partial request not adopted", err)
		}
		e.cancelAsync()
		if err := e.closeAsync(context.Background()); err != nil || partial.cancels != 1 || partial.closes != 1 {
			t.Fatal("partial request leaked", err)
		}
	})
	t.Run("invalid_promised_protocol", func(t *testing.T) {
		for _, scan := range []catalog.ForeignScan{&asyncUnitScan{}, &asyncUnitScan{request: &asyncUnitRequest{}}, &asyncNoRequestScan{}} {
			e := asyncUnitExecution(scan)
			if err := e.requestAsync(context.Background()); err == nil {
				t.Fatal("invalid protocol accepted")
			}
			e.cancelAsync()
			_ = e.closeAsync(context.Background())
		}
	})
	t.Run("pending_does_not_finish_stage", func(t *testing.T) {
		closes := 0
		pending := true
		stage := Stage{Source: &Source{FlatWidth: 0, Materialized: true}, Selection: []int{}, Finish: func() error { closes++; return nil }, Produce: func(ctx context.Context, out *Stage) (bool, error) {
			if pending {
				return false, errForeignPending
			}
			out.Selection = append(out.Selection, out.Source.FlatCount)
			out.Source.FlatCount++
			return true, nil
		}}
		if _, present, err := stage.NextOccurrence(context.Background()); present || err != errForeignPending || stage.Done || stage.Closed || stage.Terminal != nil || stage.Position != 0 || closes != 0 {
			t.Fatal("pending changed lifecycle", err)
		}
		pending = false
		if _, present, err := stage.NextOccurrence(context.Background()); !present || err != nil || stage.Position != 1 {
			t.Fatal("pending frontier not resumable", err)
		}
		_ = stage.Close()
	})
}

type asyncNoRequestScan struct{ catalog.ForeignScan }
