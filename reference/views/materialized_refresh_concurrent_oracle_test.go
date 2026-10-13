package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"github.com/lib/pq"
	"regexp"
	"stuntgres/reference/fixture"
	"testing"
)

var concurrentInsertContext = regexp.MustCompile(`^SQL statement "INSERT INTO public\.mv SELECT \(diff\.newdata\)\.\* FROM pg_temp_[1-9][0-9]*\.pg_temp_[1-9][0-9]*_2 diff WHERE tid IS NULL"$`)

// PostgreSQL's SPI diff heap and generated physical SQL are not native engine
// mechanisms. Validate the complete observed server context before removing
// only that field; every logical diagnostic field remains an exact assertion.
// No shared fixture or ordinary uniqueness diagnostic is normalized here.
type concurrentRuntimeExecutor struct {
	fixture.Executor
	t  *testing.T
	pg bool
}

func (e concurrentRuntimeExecutor) ExecContext(ctx context.Context, q string, args ...any) (sql.Result, error) {
	result, err := e.Executor.ExecContext(ctx, q, args...)
	if e.pg && err != nil {
		var server *pq.Error
		if errors.As(err, &server) && server.Code == "23505" {
			if !concurrentInsertContext.MatchString(server.Where) {
				e.t.Fatalf("unexpected concurrent runtime SPI context: %q", server.Where)
			}
			normalized := *server
			normalized.Where = ""
			err = &normalized
		}
	}
	return result, err
}

func concurrentRuntimeFailure(t *testing.T, p *fixture.Pair, b fixture.Backend, source fixture.Executor, s fixture.Step) {
	t.Helper()
	if s.Diagnostic == nil || s.Diagnostic.Code != "23505" {
		t.Fatal("runtime context boundary requires concurrent23505")
	}
	if s.Prepared {
		stmt, err := source.PrepareContext(context.WithValue(p.Ctx, fixture.RecorderKey{}, b.Recorder), s.Query)
		if err != nil {
			t.Fatal("concurrent runtime Prepare", err)
		}
		defer func() {
			if err := stmt.Close(); err != nil {
				t.Error("concurrent runtime statement close", err)
			}
		}()
		source = fixture.HeldStatement{Executor: source, Statement: stmt, SQL: s.Query}
		s.Prepared = false // The actual retained statement executes through source.
	}
	fixture.RunStep(t, p.Ctx, concurrentRuntimeExecutor{source, t, b.PG}, b.Recorder, s)
}
