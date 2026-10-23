package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"sync"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

// Opt-in only: existing reader scenarios retain their ordinary cursor lifecycle.
// Begin captures actual independent SQL rows once; partitions consume disjoint
// occurrence intervals, not distinct values. This proves in-process consumption,
// not concurrent network requests.
type fdwParallelReader struct {
	*fdwSQLReader
	mu                           sync.Mutex
	safe                         bool
	partitions                   int
	begins, ends, opened, closed int
	entered                      chan int
	release                      <-chan struct{}
	hook                         func(context.Context, string, int) error
	transaction                  *fdwParallelTransactionReader
}

func (r *fdwParallelReader) IsForeignScanParallelSafe(ctx context.Context, _ stuntgres.ForeignRequest, _ stuntgres.ForeignPlan) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.safe, ctx.Err()
}

func (r *fdwParallelReader) BeginForeignParallelScan(ctx context.Context, request stuntgres.ForeignRequest, plan stuntgres.ForeignPlan, maximum int) (result stuntgres.ForeignParallelScan, failure error) {
	r.mu.Lock()
	r.begins++
	n := r.partitions
	r.mu.Unlock()
	if n == 0 {
		n = maximum
	}
	shared := &fdwParallelSnapshot{reader: r, count: n}
	if r.hook != nil {
		if err := r.hook(ctx, "Begin", -1); err != nil {
			return shared, err
		}
	}
	scan, err := r.fdwSQLReader.BeginForeignScan(ctx, request, plan)
	if scan != nil {
		defer func() {
			if err := scan.EndForeignScan(context.WithoutCancel(ctx)); failure == nil {
				failure = err
			}
		}()
	}
	if err != nil {
		return shared, err
	}
	for {
		row, present, err := scan.IterateForeignScan(ctx)
		if err != nil {
			return shared, err
		}
		if !present {
			break
		}
		owned := append(stuntgres.ForeignRow{}, row...)
		for i, value := range owned {
			if bytes, ok := value.([]byte); ok {
				owned[i] = append([]byte{}, bytes...)
			}
		}
		shared.rows = append(shared.rows, owned)
	}
	return shared, nil
}

type fdwParallelSnapshot struct {
	reader *fdwParallelReader
	count  int
	rows   []stuntgres.ForeignRow
	ended  bool
}

func (s *fdwParallelSnapshot) PartitionCount() int { return s.count }
func (s *fdwParallelSnapshot) BeginPartitionForeignScan(ctx context.Context, index int) (stuntgres.ForeignScan, error) {
	if index < 0 || index >= s.count {
		return nil, fmt.Errorf("invalid partition %d", index)
	}
	s.reader.mu.Lock()
	s.reader.opened++
	s.reader.mu.Unlock()
	scan := &fdwParallelPartition{shared: s, index: index, next: len(s.rows) * index / s.count, stop: len(s.rows) * (index + 1) / s.count}
	if s.reader.hook != nil {
		return scan, s.reader.hook(ctx, "Partition", index)
	}
	return scan, ctx.Err()
}
func (s *fdwParallelSnapshot) EndForeignParallelScan(ctx context.Context) error {
	if s.ended {
		return fmt.Errorf("shared state ended twice")
	}
	s.ended = true
	s.reader.mu.Lock()
	s.reader.ends++
	s.reader.mu.Unlock()
	if s.reader.hook != nil {
		return s.reader.hook(ctx, "EndShared", -1)
	}
	return nil
}

type fdwParallelPartition struct {
	shared            *fdwParallelSnapshot
	index, next, stop int
	entered, ended    bool
	cells             stuntgres.ForeignRow
	scratch           []byte
}

func (s *fdwParallelPartition) IterateForeignScan(ctx context.Context) (stuntgres.ForeignRow, bool, error) {
	r := s.shared.reader
	if !s.entered {
		s.entered = true
		if r.entered != nil {
			select {
			case r.entered <- s.index:
			case <-ctx.Done():
				return nil, false, ctx.Err()
			}
		}
		if r.release != nil {
			select {
			case <-r.release:
			case <-ctx.Done():
				return nil, false, ctx.Err()
			}
		}
	}
	if r.hook != nil {
		if err := r.hook(ctx, "Iterate", s.index); err != nil {
			return nil, false, err
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	if s.next == s.stop {
		return nil, false, nil
	}
	row := s.shared.rows[s.next]
	if s.cells == nil {
		s.cells = make(stuntgres.ForeignRow, len(row))
	}
	copy(s.cells, row)
	for i, value := range s.cells {
		switch value := value.(type) {
		case string:
			s.scratch = append(s.scratch[:0], value...)
			s.cells[i] = s.scratch
		case []byte:
			s.scratch = append(s.scratch[:0], value...)
			s.cells[i] = s.scratch
		}
	}
	s.next++
	return s.cells, true, nil
}
func (*fdwParallelPartition) ReScanForeignScan(context.Context, stuntgres.ForeignScanParameters) error {
	return fmt.Errorf("partition rescan is unsupported")
}
func (s *fdwParallelPartition) EndForeignScan(ctx context.Context) error {
	if s.ended {
		return fmt.Errorf("partition ended twice")
	}
	s.ended = true
	for i := range s.scratch {
		s.scratch[i] = '!'
	}
	for i := range s.cells {
		s.cells[i] = nil
	}
	s.shared.reader.mu.Lock()
	s.shared.reader.closed++
	s.shared.reader.mu.Unlock()
	if s.shared.reader.hook != nil {
		return s.shared.reader.hook(ctx, "End", s.index)
	}
	return nil
}

func fdwParallelScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn, *fdwParallelReader, func(fixture.Step))) {
	t.Helper()
	fdwParallelConfiguredScenario(t, false, body)
}

func fdwParallelConfiguredScenario(t *testing.T, transactional bool, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn, *fdwParallelReader, func(fixture.Step))) {
	t.Helper()
	fixture.SchemaReferenceCleanup(t, "fdw_source")
	fixture.RoleReferenceCleanup(t, []string{"fdw_reader", "fdw_other", "fdw_owner"})
	r := &fdwParallelReader{fdwSQLReader: newFDWSQLReader(t), safe: true}
	if transactional {
		r.transaction = &fdwParallelTransactionReader{fdwParallelReader: r, writer: &fdwSQLWriter{fdwSQLReader: r.fdwSQLReader}}
	}
	r.fdwSQLReader.parallel = r
	p := fixture.NewScenarioWithDriverConfig(t, r.configure)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			defer fdwCleanup(t, c)
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			body(t, p, b, c, r, run)
		})
	}
}

func fdwParallelLive(t *testing.T, p *fixture.Pair, b fixture.Backend, r *fdwParallelReader, run func(fixture.Step)) {
	fdwSourceExec(t, p, b, r.fdwSQLReader, run, "CREATE SCHEMA fdw_source", 0)
	fdwSourceExec(t, p, b, r.fdwSQLReader, run, "CREATE TABLE fdw_source.data(id integer,k integer,p text)", 0)
	fdwSourceExec(t, p, b, r.fdwSQLReader, run, "INSERT INTO fdw_source.data VALUES(1,2,'a'),(2,NULL,'b'),(3,1,'c'),(4,2,'a'),(5,NULL,'b'),(6,1,NULL)", 6)
	if b.PG {
		run(fixture.Exec("CREATE EXTENSION IF NOT EXISTS postgres_fdw", 0))
		run(fixture.Exec("CREATE SERVER fdw_live FOREIGN DATA WRAPPER postgres_fdw OPTIONS(host '127.0.0.1',port '5432',dbname 'stuntgres_reference')", 0))
		run(fixture.Exec("CREATE USER MAPPING FOR CURRENT_USER SERVER fdw_live OPTIONS(user 'stuntgres',password 'stuntgres')", 0))
	} else {
		run(fixture.Exec("CREATE FOREIGN DATA WRAPPER fdw_native HANDLER fdw_sql_handler VALIDATOR fdw_sql_validator", 0))
		run(fixture.Exec("CREATE SERVER fdw_live FOREIGN DATA WRAPPER fdw_native", 0))
		run(fixture.Exec("CREATE USER MAPPING FOR CURRENT_USER SERVER fdw_live", 0))
	}
	run(fixture.Exec("CREATE FOREIGN TABLE fdw_data(id integer,k integer,p text) SERVER fdw_live OPTIONS(schema_name 'fdw_source',table_name 'data')", 0))
}
