package core

import (
	"context"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/sqltype"
	"time"
)

// This wrapper stays at the canonical executor result boundary. It observes
// real producer work/disposal, never transport Next counts of materialized rows.
type statementStatsRows struct {
	exec.RowResult
	frame  *statementStatsFrame
	closed bool
}

func (r *statementStatsRows) Next(ctx context.Context, row []sqltype.Value) (bool, error) {
	start := time.Now()
	present, err := r.RowResult.Next(ctx, row)
	r.frame.elapsed += time.Since(start)
	if err != nil {
		r.frame.failed = true
	} else if present {
		r.frame.rows++
	}
	return present, err
}
func (r *statementStatsRows) Close() error {
	if r.closed {
		return nil
	}
	r.closed = true
	start := time.Now()
	err := r.RowResult.Close()
	r.frame.elapsed += time.Since(start)
	if err != nil {
		r.frame.failed = true
	} else {
		r.frame.executed = true
	}
	return err
}
