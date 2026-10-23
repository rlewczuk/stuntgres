package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"stuntgres"
	"stuntgres/reference/fixture"
)

// This opt-in capability uses the existing genuine source transaction/savepoint
// implementation. Only snapshot capture touches sql.Tx; workers consume owned
// occurrence intervals and never mutate the public SQL owner's participant state.
// The statement marker is a real uncommitted SQL effect: successful owner
// completion persists it, whereas cancellation/failure must roll it back.
type fdwParallelTransactionReader struct {
	*fdwParallelReader
	writer                           *fdwSQLWriter
	mu                               sync.Mutex
	current                          *fdwParallelTransaction
	safety, shared, consumed, active int
	violation                        error
}

func (r *fdwParallelTransactionReader) ForeignTransactionCapabilities(ctx context.Context, request stuntgres.ForeignRequest) (stuntgres.ForeignTransactionCapabilities, error) {
	return r.writer.ForeignTransactionCapabilities(ctx, request)
}

func (r *fdwParallelTransactionReader) BeginForeignTransaction(ctx context.Context, request stuntgres.ForeignRequest) (stuntgres.ForeignTransaction, error) {
	tx, err := r.writer.BeginForeignTransaction(ctx, request)
	if err != nil {
		return tx, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.current != nil {
		r.violation = errors.New("second participant started before prior completion")
	}
	current := &fdwParallelTransaction{fdwWriterTransaction: tx.(*fdwWriterTransaction), reader: r}
	r.current = current
	return current, nil
}

func (r *fdwParallelTransactionReader) enlisted(request stuntgres.ForeignRequest) (*fdwParallelTransaction, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	tx, ok := request.Transaction.(*fdwParallelTransaction)
	if !ok || tx != r.current || !tx.started || tx.finished {
		return nil, errors.New("parallel callback preceded owner statement startup or used a different transaction")
	}
	return tx, nil
}

func (r *fdwParallelTransactionReader) IsForeignScanParallelSafe(ctx context.Context, request stuntgres.ForeignRequest, plan stuntgres.ForeignPlan) (bool, error) {
	tx, err := r.enlisted(request)
	if err != nil {
		return false, err
	}
	// A separate source owner cannot see this marker until Commit. Its visibility
	// here proves the exact live transaction, not just request pointer forwarding.
	var markers int
	if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM fdw_source.parallel_transactions WHERE marker=$1", r.writer.transactions).Scan(&markers); err != nil {
		return false, err
	}
	if markers != 1 {
		return false, errors.New("safety did not see the uncommitted statement effect")
	}
	r.mu.Lock()
	r.safety++
	r.mu.Unlock()
	return r.fdwParallelReader.IsForeignScanParallelSafe(ctx, request, plan)
}

func (r *fdwParallelTransactionReader) BeginForeignParallelScan(ctx context.Context, request stuntgres.ForeignRequest, plan stuntgres.ForeignPlan, maximum int) (stuntgres.ForeignParallelScan, error) {
	tx, err := r.enlisted(request)
	if err != nil {
		return nil, err
	}
	r.mu.Lock()
	r.shared++
	r.mu.Unlock()
	r.fdwParallelReader.mu.Lock()
	r.begins++
	n := r.partitions
	r.fdwParallelReader.mu.Unlock()
	if n == 0 {
		n = maximum
	}
	shared := &fdwParallelTransactionSnapshot{fdwParallelSnapshot: &fdwParallelSnapshot{reader: r.fdwParallelReader, count: n}, transaction: tx}
	// Same real transaction as BeginStatement and safety. The plan's actual
	// projected columns determine the SQL source rows; no canned result values.
	columns := make([]string, 0, len(plan.Attributes))
	for _, attribute := range plan.Attributes {
		for _, column := range request.Columns {
			if column.Attribute == attribute {
				name := fdwOption(column.Options, "column_name")
				if name == "" {
					name = column.Name
				}
				columns = append(columns, fdwQuote(name))
				break
			}
		}
	}
	projection := strings.Join(columns, ",")
	if len(columns) == 0 {
		projection = "1"
	}
	rows, err := tx.QueryContext(ctx, "SELECT "+projection+" FROM "+plan.Private.(string))
	if err != nil {
		return shared, err
	}
	defer rows.Close()
	for rows.Next() {
		values := make([]any, max(1, len(columns)))
		dest := make([]any, len(values))
		for i := range values {
			dest[i] = &values[i]
		}
		if err := rows.Scan(dest...); err != nil {
			return shared, err
		}
		row := make(stuntgres.ForeignRow, len(columns))
		for i := range row {
			if bytes, ok := values[i].([]byte); ok {
				row[i] = append([]byte{}, bytes...)
			} else {
				row[i] = values[i]
			}
		}
		shared.rows = append(shared.rows, row)
	}
	if err := rows.Err(); err != nil {
		return shared, err
	}
	return shared, rows.Close()
}

type fdwParallelTransaction struct {
	*fdwWriterTransaction
	reader            *fdwParallelTransactionReader
	started, finished bool
}

func (tx *fdwParallelTransaction) BeginStatement(ctx context.Context) error {
	if err := tx.fdwWriterTransaction.BeginStatement(ctx); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO fdw_source.parallel_transactions VALUES($1)", tx.reader.writer.transactions); err != nil {
		return err
	}
	tx.reader.mu.Lock()
	tx.started = true
	tx.reader.mu.Unlock()
	return nil
}

func (tx *fdwParallelTransaction) cleanupOrder() error {
	r := tx.reader
	r.mu.Lock()
	defer r.mu.Unlock()
	counts := r.parallelCounts()
	if r.active != 0 || counts.begins != counts.ends || counts.opened != counts.closed {
		r.violation = fmt.Errorf("participant completion before worker join/partition End/shared End: active=%d counts=%+v", r.active, counts)
	}
	return r.violation
}

func (tx *fdwParallelTransaction) EndStatement(ctx context.Context, success bool) error {
	if err := tx.cleanupOrder(); err != nil {
		return err
	}
	err := tx.fdwWriterTransaction.EndStatement(ctx, success)
	tx.reader.mu.Lock()
	tx.finished = true
	tx.reader.mu.Unlock()
	return err
}

func (tx *fdwParallelTransaction) complete(ctx context.Context, commit bool) error {
	if err := tx.cleanupOrder(); err != nil {
		return err
	}
	var err error
	if commit {
		err = tx.fdwWriterTransaction.Commit(ctx)
	} else {
		err = tx.fdwWriterTransaction.Rollback(ctx)
	}
	tx.reader.mu.Lock()
	tx.reader.current = nil
	tx.reader.mu.Unlock()
	return err
}
func (tx *fdwParallelTransaction) Commit(ctx context.Context) error   { return tx.complete(ctx, true) }
func (tx *fdwParallelTransaction) Rollback(ctx context.Context) error { return tx.complete(ctx, false) }

type fdwParallelTransactionSnapshot struct {
	*fdwParallelSnapshot
	transaction *fdwParallelTransaction
}

func (s *fdwParallelTransactionSnapshot) BeginPartitionForeignScan(ctx context.Context, index int) (stuntgres.ForeignScan, error) {
	r := s.transaction.reader
	r.mu.Lock()
	ready := s.transaction.started && !s.transaction.finished && r.current == s.transaction
	r.mu.Unlock()
	if !ready {
		return nil, errors.New("partition startup before owner statement")
	}
	scan, err := s.fdwParallelSnapshot.BeginPartitionForeignScan(ctx, index)
	if scan == nil {
		return nil, err
	}
	return &fdwParallelTransactionPartition{ForeignScan: scan, reader: r}, err
}

func (s *fdwParallelTransactionSnapshot) EndForeignParallelScan(ctx context.Context) error {
	r := s.transaction.reader
	r.mu.Lock()
	active := r.active
	r.mu.Unlock()
	counts := r.parallelCounts()
	if active != 0 || counts.opened != counts.closed {
		r.mu.Lock()
		r.violation = errors.New("shared End preceded worker join or partition End")
		err := r.violation
		r.mu.Unlock()
		return err
	}
	return s.fdwParallelSnapshot.EndForeignParallelScan(ctx)
}

type fdwParallelTransactionPartition struct {
	stuntgres.ForeignScan
	reader *fdwParallelTransactionReader
}

func (s *fdwParallelTransactionPartition) IterateForeignScan(ctx context.Context) (stuntgres.ForeignRow, bool, error) {
	r := s.reader
	r.mu.Lock()
	if r.current == nil || !r.current.started || r.current.finished {
		r.mu.Unlock()
		return nil, false, errors.New("partition consumption outside owner statement")
	}
	r.active++
	r.consumed++
	r.mu.Unlock()
	defer func() { r.mu.Lock(); r.active--; r.mu.Unlock() }()
	return s.ForeignScan.IterateForeignScan(ctx)
}

func (s *fdwParallelTransactionPartition) EndForeignScan(ctx context.Context) error {
	s.reader.mu.Lock()
	active := s.reader.active
	s.reader.mu.Unlock()
	if active != 0 {
		s.reader.mu.Lock()
		s.reader.violation = errors.New("partition End preceded worker join")
		err := s.reader.violation
		s.reader.mu.Unlock()
		return err
	}
	return s.ForeignScan.EndForeignScan(ctx)
}

func TestReferenceFDWParallelTransactions(t *testing.T) {
	fdwParallelConfiguredScenario(t, true, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fdwParallelReader, run func(fixture.Step)) {
		fdwParallelLive(t, p, b, r, run)
		fdwSourceExec(t, p, b, r.fdwSQLReader, run, "CREATE TABLE fdw_source.parallel_transactions(marker integer)", 0)
		run(fixture.Exec("SET max_parallel_workers_per_gather=2", 0))
		// The shared SQL contract has a genuine serial postgres_fdw counterpart.
		// PostgreSQL is not claimed to implement our Go transaction capability.
		run(fdwParallelIDs("SELECT id FROM fdw_data ORDER BY id", 1, 2, 3, 4, 5, 6))
		if b.PG {
			return
		}
		tr := r.transaction
		assert := func(commits, rollbacks int) {
			t.Helper()
			fdwParallelBalanced(t, r)
			tr.mu.Lock()
			defer tr.mu.Unlock()
			w := tr.writer
			n := commits + rollbacks
			if tr.violation != nil || tr.current != nil || tr.active != 0 || tr.safety != n || tr.shared != n || tr.consumed < 2*n || w.transactions != n || w.statementBegins != n || w.statementEnds != n || w.commits != commits || w.rollbacks != rollbacks {
				t.Fatalf("owner transaction lifecycle: violation=%v current=%v active=%d safety/shared/consumed=%d/%d/%d participant/statement=%d/%d/%d commit/rollback=%d/%d want=%d/%d", tr.violation, tr.current != nil, tr.active, tr.safety, tr.shared, tr.consumed, w.transactions, w.statementBegins, w.statementEnds, w.commits, w.rollbacks, commits, rollbacks)
			}
			var persisted int
			if err := r.source.QueryRowContext(p.Ctx, "SELECT count(*) FROM fdw_source.parallel_transactions").Scan(&persisted); err != nil || persisted != commits {
				t.Fatalf("source transaction effects: persisted=%d want=%d err=%v", persisted, commits, err)
			}
		}
		assert(1, 0)
		fdwParallelGate(t, p, c, r, false)
		assert(2, 0)
		fdwParallelGate(t, p, c, r, true)
		assert(2, 1)
		primary := errors.New("transactional partition read failed")
		ctx, cancel := context.WithTimeout(p.Ctx, 5*time.Second)
		defer cancel()
		sibling := make(chan struct{})
		var entered sync.Once
		r.hook = func(ctx context.Context, phase string, index int) error {
			if phase == "Iterate" && index == 0 {
				select {
				case <-sibling:
					return primary
				case <-ctx.Done():
					return ctx.Err()
				}
			}
			if phase == "Iterate" {
				entered.Do(func() { close(sibling) })
				<-ctx.Done()
			}
			return ctx.Err()
		}
		fdwHostQueryError(t, ctx, c, "SELECT id FROM fdw_data ORDER BY id", primary)
		cancel()
		r.hook = nil
		assert(2, 2)
		run(fdwParallelIDs("SELECT id FROM fdw_data ORDER BY id", 1, 2, 3, 4, 5, 6))
		assert(3, 2)
	})
}
