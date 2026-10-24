package reference_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"

	"stuntgres"
)

// Each registered endpoint owns a real SQL cursor. HTTP requests advance that
// cursor once; neither Begin nor the transport snapshots or replays the source.
type fdwAsyncReader struct {
	*fdwSQLReader
	server                                                   *httptest.Server
	mu                                                       sync.Mutex
	scans                                                    map[string]*fdwAsyncScan
	jobs                                                     map[string]*fdwAsyncRequest
	next                                                     int
	gate                                                     chan struct{}
	slow                                                     chan struct{}
	ready                                                    chan struct{}
	release                                                  sync.Once
	slowOnce                                                 sync.Once
	readyOnce                                                sync.Once
	active                                                   int
	failSQL                                                  string
	requests                                                 int
	abort                                                    bool
	readyAfterSlow                                           bool
	ownedAsyncRequests                                       int
	peakScans, peakRequests, peakAsyncRequests, peakHandlers int
}

func newFDWAsyncReader(t *testing.T) *fdwAsyncReader {
	r := &fdwAsyncReader{fdwSQLReader: newFDWSQLReader(t), scans: make(map[string]*fdwAsyncScan), gate: make(chan struct{}), slow: make(chan struct{}), ready: make(chan struct{})}
	r.jobs = make(map[string]*fdwAsyncRequest)
	r.server = httptest.NewServer(http.HandlerFunc(r.serve))
	t.Cleanup(func() { r.unblock(); r.server.Close() })
	return r
}
func (r *fdwAsyncReader) unblock() { r.release.Do(func() { close(r.gate) }) }
func (r *fdwAsyncReader) counts() (int, int, int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.active, len(r.scans), r.requests
}
func (r *fdwAsyncReader) configure(d *stuntgres.Driver) error {
	if err := d.RegisterForeignDataWrapper(stuntgres.ForeignDataWrapperDefinition{HandlerName: stuntgres.ForeignFunctionName{Schema: "public", Name: "fdw_async_handler"}, Wrapper: r}); err != nil {
		return err
	}
	if err := d.RegisterForeignDataWrapper(stuntgres.ForeignDataWrapperDefinition{HandlerName: stuntgres.ForeignFunctionName{Schema: "public", Name: "fdw_async_plain_handler"}, Wrapper: fdwScanOnly{r}}); err != nil {
		return err
	}
	return d.RegisterForeignValidator(stuntgres.ForeignValidatorDefinition{Name: stuntgres.ForeignFunctionName{Schema: "public", Name: "fdw_async_validator"}, Validate: r.validateAsync})
}
func (r *fdwAsyncReader) validateAsync(ctx context.Context, kind stuntgres.ForeignObjectKind, options []stuntgres.ForeignOption) error {
	var ordinary []stuntgres.ForeignOption
	for _, o := range options {
		if o.Name == "async_capable" && (kind == stuntgres.ForeignServer || kind == stuntgres.ForeignTable) {
			if o.Value != "true" && o.Value != "false" {
				return fmt.Errorf("invalid async_capable value %q", o.Value)
			}
		} else if o.Name != "fetch_size" {
			ordinary = append(ordinary, o)
		}
	}
	return r.validate(ctx, kind, ordinary)
}
func (r *fdwAsyncReader) IsForeignPathAsyncCapable(ctx context.Context, req stuntgres.ForeignRequest, path stuntgres.ForeignPath) (bool, error) {
	value := fdwOption(req.ServerOptions, "async_capable")
	if table := fdwOption(req.TableOptions, "async_capable"); table != "" {
		value = table
	}
	return value == "true", ctx.Err()
}
func (r *fdwAsyncReader) BeginForeignScan(ctx context.Context, req stuntgres.ForeignRequest, plan stuntgres.ForeignPlan) (stuntgres.ForeignScan, error) {
	raw, err := r.fdwSQLReader.BeginForeignScan(ctx, req, plan)
	if raw == nil {
		return nil, err
	}
	r.mu.Lock()
	r.next++
	id := strconv.Itoa(r.next)
	s := &fdwAsyncScan{ForeignScan: raw, reader: r, id: id, slow: fdwOption(req.TableOptions, "table_name") == "slow"}
	r.scans[id] = s
	r.peakScans = max(r.peakScans, len(r.scans))
	r.mu.Unlock()
	return s, err
}

type fdwAsyncWire struct {
	Row        []any
	Present    bool
	Diagnostic *stuntgres.Error
	Error      string
}

func (r *fdwAsyncReader) serve(w http.ResponseWriter, q *http.Request) {
	r.mu.Lock()
	job := r.jobs[q.URL.Query().Get("request")]
	s := r.scans[q.URL.Path[1:]]
	if job == nil || s == nil {
		r.mu.Unlock()
		http.Error(w, "closed source request", 410)
		return
	}
	if job.claimed {
		r.mu.Unlock()
		http.Error(w, "source request already claimed", http.StatusConflict)
		return
	}
	job.claimed = true
	r.active++
	r.peakHandlers = max(r.peakHandlers, r.active)
	r.requests++
	abort := r.abort
	readyAfterSlow := r.readyAfterSlow
	r.mu.Unlock()
	defer func() { r.mu.Lock(); r.active--; r.mu.Unlock(); close(job.serverDone) }()
	if abort {
		conn, _, err := w.(http.Hijacker).Hijack()
		if err == nil {
			conn.Close()
		}
		return
	}
	row, present, err := s.ForeignScan.IterateForeignScan(q.Context())
	// The pending-close case must admit the actual slow HTTP handler before a
	// ready result can let LIMIT cancel its sibling. Preserve both SQL frontiers:
	// only delivery of this already-selected ready row is held.
	if !s.slow && present && err == nil && readyAfterSlow {
		select {
		case <-r.slow:
		case <-q.Context().Done():
			return
		}
	}
	if s.slow && present && err == nil {
		r.slowOnce.Do(func() { close(r.slow) })
		select {
		case <-r.gate:
		case <-q.Context().Done():
			return
		}
	}
	if s.slow && present && err == nil {
		r.mu.Lock()
		failSQL := r.failSQL
		r.mu.Unlock()
		if failSQL != "" {
			_, err = r.source.ExecContext(q.Context(), failSQL)
		}
	}
	result := fdwAsyncWire{Present: present}
	for _, v := range row {
		result.Row = append(result.Row, v)
	}
	if err != nil {
		if diagnostic, ok := err.(*stuntgres.Error); ok {
			result.Diagnostic = diagnostic
		} else {
			result.Error = err.Error()
		}
	}
	_ = json.NewEncoder(w).Encode(result)
}

var _ stuntgres.ForeignAsyncScanProvider = (*fdwAsyncReader)(nil)
var _ stuntgres.ForeignAsyncScan = (*fdwAsyncScan)(nil)
var _ stuntgres.ForeignAsyncRequest = (*fdwAsyncRequest)(nil)

type fdwAsyncScan struct {
	stuntgres.ForeignScan
	reader  *fdwAsyncReader
	id      string
	slow    bool
	pending *fdwAsyncRequest // protected by reader.mu
}

func (s *fdwAsyncScan) RequestForeignScan(ctx context.Context) (stuntgres.ForeignAsyncRequest, error) {
	return s.requestHTTP(ctx, true)
}

func (s *fdwAsyncScan) requestHTTP(ctx context.Context, asynchronous bool) (stuntgres.ForeignAsyncRequest, error) {
	ctx, cancel := context.WithCancel(ctx)
	request := &fdwAsyncRequest{ready: make(chan struct{}), serverDone: make(chan struct{}), cancel: cancel, reader: s.reader, scan: s, asynchronous: asynchronous}
	s.reader.mu.Lock()
	if s.pending != nil {
		s.reader.mu.Unlock()
		cancel()
		return nil, fmt.Errorf("second request before prior Close")
	}
	s.pending = request
	s.reader.next++
	request.id = strconv.Itoa(s.reader.next)
	s.reader.jobs[request.id] = request
	s.reader.peakRequests = max(s.reader.peakRequests, len(s.reader.jobs))
	if asynchronous {
		s.reader.ownedAsyncRequests++
		s.reader.peakAsyncRequests = max(s.reader.peakAsyncRequests, s.reader.ownedAsyncRequests)
	}
	s.reader.mu.Unlock()
	go func() {
		defer close(request.ready)
		// Advancing a SQL cursor is not idempotent: POST prevents net/http from
		// replaying a failed request on a reused connection after an EOF.
		q, err := http.NewRequestWithContext(ctx, http.MethodPost, s.reader.server.URL+"/"+s.id+"?request="+request.id, nil)
		if err != nil {
			request.err = err
			return
		}
		response, err := s.reader.server.Client().Do(q)
		if err != nil {
			request.err = err
			return
		}
		defer response.Body.Close()
		var wire fdwAsyncWire
		decoder := json.NewDecoder(response.Body)
		decoder.UseNumber()
		if err = decoder.Decode(&wire); err != nil {
			request.err = err
			return
		}
		if wire.Diagnostic != nil {
			request.err = wire.Diagnostic
			return
		}
		if wire.Error != "" {
			request.err = fmt.Errorf("remote SQL: %s", wire.Error)
			return
		}
		request.present = wire.Present
		for _, v := range wire.Row {
			if n, ok := v.(json.Number); ok {
				integer, e := n.Int64()
				if e != nil {
					request.err = e
					return
				}
				v = integer
			}
			request.row = append(request.row, v)
		}
		if !s.slow && wire.Present {
			s.reader.readyOnce.Do(func() { close(s.reader.ready) })
		}
	}()
	return request, nil
}
func (s *fdwAsyncScan) IterateForeignScan(ctx context.Context) (stuntgres.ForeignRow, bool, error) {
	request, err := s.requestHTTP(ctx, false)
	if err != nil {
		return nil, false, err
	}
	select {
	case <-request.Ready():
	case <-ctx.Done():
		request.Cancel()
		_ = request.Close(context.WithoutCancel(ctx))
		return nil, false, ctx.Err()
	}
	row, present, err := request.Complete(ctx)
	_ = request.Close(context.WithoutCancel(ctx))
	return row, present, err
}
func (s *fdwAsyncScan) EndForeignScan(ctx context.Context) error {
	s.reader.mu.Lock()
	delete(s.reader.scans, s.id)
	s.reader.mu.Unlock()
	return s.ForeignScan.EndForeignScan(ctx)
}

type fdwAsyncRequest struct {
	ready        chan struct{}
	serverDone   chan struct{}
	reader       *fdwAsyncReader
	scan         *fdwAsyncScan
	id           string
	claimed      bool // protected by reader.mu; Close unregisters before checking it
	asynchronous bool
	cancel       context.CancelFunc
	row          stuntgres.ForeignRow
	present      bool
	err          error
}

func (r *fdwAsyncRequest) Ready() <-chan struct{} { return r.ready }
func (r *fdwAsyncRequest) Complete(ctx context.Context) (stuntgres.ForeignRow, bool, error) {
	return r.row, r.present, r.err
}
func (r *fdwAsyncRequest) Cancel() { r.cancel() }
func (r *fdwAsyncRequest) Close(ctx context.Context) error {
	<-r.ready
	r.cancel()
	r.reader.mu.Lock()
	if r.reader.jobs[r.id] != nil && r.asynchronous {
		r.reader.ownedAsyncRequests--
	}
	delete(r.reader.jobs, r.id)
	claimed := r.claimed
	r.reader.mu.Unlock()
	if claimed {
		<-r.serverDone
	}
	r.reader.mu.Lock()
	if r.scan.pending == r {
		r.scan.pending = nil
	}
	r.reader.mu.Unlock()
	return nil
}
