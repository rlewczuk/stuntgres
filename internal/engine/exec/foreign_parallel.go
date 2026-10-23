package exec

import (
	"context"
	"fmt"
	"sync"

	"stuntgres/internal/engine/catalog"
)

// foreignParallelExecution is an execution-owned scan adapter, never a second
// source lifetime authority. Only the leader starts/ends states and imports rows;
// workers call Iterate on distinct provider scans and transfer owned cells.
type foreignParallelExecution struct {
	shared         catalog.ForeignParallelScan
	scans          []catalog.ForeignScan
	ctx            context.Context
	cancel         context.CancelFunc
	rows           chan catalog.ForeignRow
	done           chan struct{}
	workers        sync.WaitGroup
	mu             sync.Mutex
	failure        error
	started, ended bool
	endError       error
}

func newForeignParallelExecution(ctx context.Context) *foreignParallelExecution {
	ctx, cancel := context.WithCancel(ctx)
	return &foreignParallelExecution{ctx: ctx, cancel: cancel, done: make(chan struct{})}
}

// First callback failure is recorded before cancellation wakes siblings. The
// same error object survives sibling context errors and terminal cleanup.
func (p *foreignParallelExecution) fail(err error) {
	if err == nil {
		return
	}
	p.mu.Lock()
	if p.failure == nil {
		p.failure = ForeignCallbackError(err)
	}
	p.mu.Unlock()
	p.cancel()
}
func (p *foreignParallelExecution) error() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.failure
}

func (p *foreignParallelExecution) start(provider catalog.ForeignParallelScanProvider, request catalog.ForeignRequest, plan catalog.ForeignPlan, maximum int) error {
	shared, err := provider.BeginForeignParallelScan(p.ctx, CopyForeignRequest(request), plan, maximum)
	p.shared = shared // A nonnil result transfers ownership even alongside error.
	if err != nil {
		p.fail(err)
		return p.error()
	}
	if shared == nil {
		return fmt.Errorf("stuntgres: foreign-data wrapper returned a nil parallel scan")
	}
	n := shared.PartitionCount()
	if n < 1 || n > maximum {
		return fmt.Errorf("stuntgres: invalid foreign parallel partition count")
	}
	p.scans = make([]catalog.ForeignScan, 0, n)
	for i := range n {
		scan, err := shared.BeginPartitionForeignScan(p.ctx, i)
		if scan != nil {
			p.scans = append(p.scans, scan)
		}
		if err != nil {
			p.fail(err)
			return p.error()
		}
		if scan == nil {
			return fmt.Errorf("stuntgres: foreign-data wrapper returned a nil partition scan")
		}
	}
	// At most one pending row per admitted partition, plus one local worker row.
	p.rows = make(chan catalog.ForeignRow, n)
	p.started = true
	for _, scan := range p.scans {
		p.workers.Add(1)
		go func() {
			defer p.workers.Done()
			for {
				row, present, err := scan.IterateForeignScan(p.ctx)
				if err != nil {
					p.fail(err)
					return
				}
				if !present {
					return
				}
				if len(row) != len(plan.Attributes) {
					p.fail(fmt.Errorf("stuntgres: foreign scan row width does not match its plan"))
					return
				}
				owned := catalog.ForeignRow(CopyForeignCells(row))
				select {
				case p.rows <- owned:
				case <-p.ctx.Done():
					return
				}
			}
		}()
	}
	go func() { p.workers.Wait(); close(p.rows); close(p.done) }()
	return nil
}

func (p *foreignParallelExecution) IterateForeignScan(ctx context.Context) (catalog.ForeignRow, bool, error) {
	if err := p.error(); err != nil {
		return nil, false, err
	}
	select {
	case row, present := <-p.rows:
		if err := p.error(); err != nil {
			return nil, false, err
		}
		if err := ctx.Err(); err != nil {
			return nil, false, err
		}
		return row, present, nil
	case <-p.ctx.Done():
		if err := p.error(); err != nil {
			return nil, false, err
		}
		return nil, false, p.ctx.Err()
	case <-ctx.Done():
		if err := p.error(); err != nil {
			return nil, false, err
		}
		return nil, false, ctx.Err()
	}
}
func (*foreignParallelExecution) ReScanForeignScan(context.Context, catalog.ForeignScanParameters) error {
	return fmt.Errorf("stuntgres: parallel foreign scan cannot be rescanned")
}
func (p *foreignParallelExecution) EndForeignScan(ctx context.Context) error {
	if p.ended {
		return p.endError
	}
	p.ended = true
	p.cancel()
	if p.started {
		<-p.done
	}
	ctx = context.WithoutCancel(ctx)
	for _, scan := range p.scans {
		if err := ForeignCallbackError(scan.EndForeignScan(ctx)); p.endError == nil {
			p.endError = err
		}
	}
	if p.shared != nil {
		if err := ForeignCallbackError(p.shared.EndForeignParallelScan(ctx)); p.endError == nil {
			p.endError = err
		}
	}
	p.scans = nil
	p.shared = nil
	return p.endError
}
