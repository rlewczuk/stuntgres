package engine

import (
	"context"
	"database/sql/driver"
	"errors"
	"reflect"
	"testing"
	"time"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func cursorUnitExecute(t *testing.T, s *Session, ctx context.Context, q string) (Result, error) {
	t.Helper()
	op, err := s.Acquire(ctx)
	if err != nil {
		return Result{}, err
	}
	defer op.Done()
	return s.Engine().Execute(op, statement(t, q), 0, nil)
}
func cursorUnitError(t *testing.T, s *Session, q, code, message string) {
	t.Helper()
	result, err := cursorUnitExecute(t, s, context.Background(), q)
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) || diagnostic.Code != code || diagnostic.Message != message || result.Rows != nil || result.Count != 0 {
		t.Fatalf("%s: result=%+v diagnostic=%+v error=%v", q, result, diagnostic, err)
	}
}
func cursorUnitBatch(t *testing.T, s *Session, q string, want [][]driver.Value) Result {
	t.Helper()
	result := sessionSQL(t, s, q)
	if result.Count != int64(len(want)) || !reflect.DeepEqual(tuples(result), want) {
		t.Fatalf("%s: count=%d rows=%v want=%v", q, result.Count, tuples(result), want)
	}
	rows := result.Rows.(*finalRows)
	if rows.source != nil || rows.anonymous != nil {
		t.Fatal("FETCH batch borrows source rows")
	}
	return result
}
func cursorUnitSetup(t *testing.T) *Session {
	t.Helper()
	s := sessionStart(t, New(), nil)
	sessionSQL(t, s, "CREATE TABLE unit_rows(i INT NOT NULL)")
	sessionSQL(t, s, "INSERT INTO unit_rows VALUES(1),(2),(3)")
	sessionSQL(t, s, "BEGIN")
	return s
}
func TestSQLCursorDetachedBatchesAndLifetime(t *testing.T) {
	s := cursorUnitSetup(t)
	declaration := sessionSQL(t, s, "DECLARE c NO SCROLL CURSOR WITHOUT HOLD FOR SELECT i FROM unit_rows ORDER BY i")
	if declaration.Rows != nil || declaration.Count != 0 {
		t.Fatal(declaration)
	}
	cursor := s.cursors.entries["c"]
	pin, cell := cursor.source.view, cursor.authority
	if cursor.stage != nil || cell.scope != nil || cell.expression != nil || pin.Load() == nil {
		t.Fatal("DECLARE opens demand or loses capture")
	}
	first := cursorUnitBatch(t, s, "FETCH 1 FROM c", [][]driver.Value{{int64(1)}})
	cursorUnitBatch(t, s, "FETCH 1 IN c", [][]driver.Value{{int64(2)}})
	if cell.scope != nil || cell.expression == nil {
		t.Fatal("FETCH retains admission or misses first demand")
	}
	cursorUnitBatch(t, s, "FETCH ALL FROM c", [][]driver.Value{{int64(3)}})
	empty := cursorUnitBatch(t, s, "FETCH ALL FROM c", [][]driver.Value{})
	if !reflect.DeepEqual(first.Rows.Schema(), empty.Rows.Schema()) {
		t.Fatal("EOF loses metadata")
	}
	cursorUnitBatch(t, s, "FETCH ALL FROM c", [][]driver.Value{})
	sessionSQL(t, s, "CLOSE c")
	if pin.Load() != nil || cell.scope != nil || cell.expression != nil {
		t.Fatal("SQL close leaks retained execution")
	}
	sessionSQL(t, s, "COMMIT")
	// A detached batch is independent even of the physical session lifetime.
	s.RequestEnd(CleanClose)
	if err := s.WaitEnd(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(tuples(first), [][]driver.Value{{int64(1)}}) {
		t.Fatal("end mutates detached batch")
	}
	if err := s.cursors.EndSession(s.Token(), CleanClose); err != nil {
		t.Fatal(err)
	}
}
func TestSQLCursorZeroWidthAndForwardStates(t *testing.T) {
	s := cursorUnitSetup(t)
	sessionSQL(t, s, "DECLARE c NO SCROLL CURSOR FOR SELECT FROM unit_rows")
	cursor := s.cursors.entries["c"]
	cursorUnitBatch(t, s, "FETCH FORWARD 0 FROM c", [][]driver.Value{})
	if cursor.stage != nil || cursor.position != cursorBefore {
		t.Fatal("zero starts demand")
	}
	cursorUnitBatch(t, s, "FETCH 1 FROM c", [][]driver.Value{{}})
	if cursor.position != cursorOn {
		t.Fatal("exact limit performs lookahead")
	}
	sessionSQL(t, s, "SAVEPOINT recover")
	result, err := cursorUnitExecute(t, s, context.Background(), "FETCH FORWARD 0 FROM c")
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) || diagnostic.Code != "55000" || diagnostic.Message != "cursor can only scan forward" || diagnostic.Hint != "Declare it with SCROLL option to enable backward scan." || result.Rows != nil {
		t.Fatal(result, err)
	}
	sessionSQL(t, s, "ROLLBACK TO recover")
	cursorUnitError(t, s, "FETCH ALL FROM c", "55000", "portal \"c\" cannot be run")
	sessionSQL(t, s, "ROLLBACK TO recover")
	sessionSQL(t, s, "CLOSE c")
	sessionSQL(t, s, "DECLARE c NO SCROLL CURSOR FOR SELECT FROM unit_rows")
	cursorUnitBatch(t, s, "FETCH FORWARD 2147483647 FROM c", [][]driver.Value{{}, {}, {}})
	cursorUnitBatch(t, s, "FETCH FORWARD 0 FROM c", [][]driver.Value{})
	sessionSQL(t, s, "CLOSE ALL")
	sessionSQL(t, s, "CLOSE ALL")
	cursorUnitError(t, s, "FETCH BACKWARD 1 FROM missing", "34000", "cursor \"missing\" does not exist")
}
func TestSQLCursorSavepointOrdinalAndStaleIncarnation(t *testing.T) {
	s := cursorUnitSetup(t)
	sessionSQL(t, s, "DECLARE parent NO SCROLL CURSOR FOR SELECT i FROM unit_rows ORDER BY i")
	sessionSQL(t, s, "SAVEPOINT mark")
	cursorUnitBatch(t, s, "FETCH 1 FROM parent", [][]driver.Value{{int64(1)}})
	sessionSQL(t, s, "DECLARE child NO SCROLL CURSOR FOR SELECT 9")
	sessionSQL(t, s, "ROLLBACK TO mark")
	cursorUnitBatch(t, s, "FETCH 1 FROM parent", [][]driver.Value{{int64(2)}})
	cursorUnitError(t, s, "FETCH ALL FROM child", "34000", "cursor \"child\" does not exist")
	sessionSQL(t, s, "ROLLBACK TO mark")
	sessionSQL(t, s, "CLOSE parent")
	sessionSQL(t, s, "ROLLBACK TO mark")
	if s.cursors.entries["parent"] != nil {
		t.Fatal("rollback resurrects CLOSE")
	}
	old := s.transactions.ref
	sessionSQL(t, s, "COMMIT")
	sessionSQL(t, s, "BEGIN")
	sessionSQL(t, s, "DECLARE parent NO SCROLL CURSOR FOR SELECT 4")
	op, err := s.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.Engine().Rollback(op, old)
	op.Done()
	if err == nil || s.cursors.entries["parent"] == nil {
		t.Fatal("stale Tx closes new cursor", err)
	}
	cursorUnitBatch(t, s, "FETCH ALL FROM parent", [][]driver.Value{{int64(4)}})
}
func TestSQLCursorNoLookaheadAndFailedBatch(t *testing.T) {
	s := cursorUnitSetup(t)
	sessionSQL(t, s, "DECLARE c NO SCROLL CURSOR FOR SELECT i FROM unit_rows ORDER BY i")
	cursor := s.cursors.entries["c"]
	original := cursor.root.expressions[0]
	draws := 0
	sentinel := errors.New("cursor projected failure")
	cursor.root.expressions = []expr.Expr{queryProbe(original.ResultUsage(), func(c *EvalContext) (sqltype.Value, error) {
		draws++
		if draws == 3 {
			return sqltype.Value{}, sentinel
		}
		return original.Eval(c)
	})}
	first := cursorUnitBatch(t, s, "FETCH 1 FROM c", [][]driver.Value{{int64(1)}})
	if draws != 1 {
		t.Fatal("output lookahead", draws)
	}
	sessionSQL(t, s, "SAVEPOINT recover")
	result, err := cursorUnitExecute(t, s, context.Background(), "FETCH ALL FROM c")
	if !errors.Is(err, sentinel) || result.Rows != nil || result.Count != 0 || draws != 3 || cursor.position != cursorFailed || cursor.source != nil || cursor.authority.scope != nil {
		t.Fatal(result, err, draws, cursor)
	}
	sessionSQL(t, s, "ROLLBACK TO recover")
	cursorUnitError(t, s, "FETCH ALL FROM c", "55000", "portal \"c\" cannot be run")
	if !reflect.DeepEqual(tuples(first), [][]driver.Value{{int64(1)}}) {
		t.Fatal("error mutates earlier batch")
	}
}

type cursorUnitContextKey struct{}

func TestSQLCursorNestedLiveAuthority(t *testing.T) {
	for _, route := range []string{"view", "union"} {
		t.Run(route, func(t *testing.T) {
			s := sessionStart(t, New(), nil)
			sessionSQL(t, s, "CREATE TABLE unit_rows(i INT)")
			sessionSQL(t, s, "INSERT INTO unit_rows VALUES(1),(2)")
			sessionSQL(t, s, "CREATE SEQUENCE unit_sequence START 10")
			sessionSQL(t, s, "CREATE VIEW unit_view AS SELECT i,nextval('unit_sequence') AS n FROM unit_rows")
			sessionSQL(t, s, "BEGIN")
			sequenceState := func(value int64, called bool) {
				t.Helper()
				result := sessionSQL(t, s, "SELECT last_value,is_called FROM unit_sequence")
				defer result.Rows.Close()
				want := [][]driver.Value{{value, called}}
				if result.Count != 1 || !reflect.DeepEqual(tuples(result), want) {
					t.Fatal("unexpected sequence demand", result.Count, tuples(result), want)
				}
			}
			query := "SELECT i,n FROM unit_view"
			if route == "union" {
				query = "SELECT 1 AS i,nextval('unit_sequence') AS n UNION ALL SELECT 2 AS i,nextval('unit_sequence') AS n"
			}
			sequenceState(10, false)
			sessionSQL(t, s, "DECLARE c NO SCROLL CURSOR FOR "+query)
			cursor := s.cursors.entries["c"]
			// Observe the expressions actually demanded by each consumer. Set
			// arms use their common-type expressions, not the project targets.
			var expressions []expr.Expr
			if route == "union" {
				expressions = cursor.root.child.(*setNode).rightExpressions
			} else {
				expressions = cursor.source.nestedRoot.(*projectNode).expressions
			}
			original := expressions[1]
			expected, calls := "first", 0
			expressions[1] = queryProbe(original.ResultUsage(), func(c *EvalContext) (sqltype.Value, error) {
				scope := c.liveScope()
				if scope == nil || scope.finished || scope.op.done || c.Context.Value(cursorUnitContextKey{}) != expected {
					t.Fatalf("nested %s borrows old admission: scope=%p context=%v expected=%s", route, scope, c.Context.Value(cursorUnitContextKey{}), expected)
				}
				calls++
				return original.Eval(c)
			})
			sequenceState(10, false)
			for i, phase := range []string{"first", "second"} {
				expected = phase
				result, err := cursorUnitExecute(t, s, context.WithValue(context.Background(), cursorUnitContextKey{}, phase), "FETCH 1 FROM c")
				if err != nil || result.Count != 1 || !reflect.DeepEqual(tuples(result), [][]driver.Value{{int64(i + 1), int64(10 + i)}}) {
					t.Fatal(result, err)
				}
				if cursor.authority.scope != nil {
					t.Fatal("nested cell retained scope")
				}
				result.Rows.Close()
				// This nonblocking query draws exactly the demanded occurrence.
				sequenceState(int64(10+i), true)
				wantCalls := i + 1
				if route == "union" {
					wantCalls = i
				}
				if calls != wantCalls {
					t.Fatal("unexpected nested consumer demand", phase, calls, wantCalls)
				}
			}
			cursorUnitBatch(t, s, "FETCH ALL FROM c", [][]driver.Value{})
			sequenceState(11, true)
			sessionSQL(t, s, "CLOSE c")
			sequenceState(11, true)
		})
	}
}
func TestSQLCursorCancellationClearsAuthority(t *testing.T) {
	s := cursorUnitSetup(t)
	sessionSQL(t, s, "DECLARE c NO SCROLL CURSOR FOR SELECT i FROM unit_rows ORDER BY i")
	cursor := s.cursors.entries["c"]
	cursorUnitBatch(t, s, "FETCH 1 FROM c", [][]driver.Value{{int64(1)}})
	sessionSQL(t, s, "SAVEPOINT recover")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	original := cursor.stage.expressions[0]
	cursor.stage.expressions = []expr.Expr{queryProbe(original.ResultUsage(), func(c *EvalContext) (sqltype.Value, error) { cancel(); return original.Eval(c) })}
	result, err := cursorUnitExecute(t, s, ctx, "FETCH 1 FROM c")
	if !errors.Is(err, context.Canceled) || result.Rows != nil || result.Count != 0 || cursor.authority.scope != nil || cursor.source != nil || cursor.position != cursorFailed {
		t.Fatal(result, err, cursor)
	}
	sessionSQL(t, s, "ROLLBACK TO recover")
	cursorUnitError(t, s, "FETCH ALL FROM c", "55000", "portal \"c\" cannot be run")
}
func TestSQLCursorQuietPreparedDescriptionAndCounter(t *testing.T) {
	s := cursorUnitSetup(t)
	op, err := s.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	description, err := s.Engine().Describe(op, statement(t, "DECLARE c NO SCROLL CURSOR FOR SELECT $1::INT AS i"))
	op.Done()
	if err != nil || description.Parameters.Count != 1 || len(description.Results) != 0 || len(s.cursors.entries) != 0 {
		t.Fatal(description, err)
	}
	s.cursors.ordinal = ^uint64(0)
	sessionSQL(t, s, "SAVEPOINT recover")
	result, err := cursorUnitExecute(t, s, context.Background(), "DECLARE c NO SCROLL CURSOR FOR SELECT 1")
	if err == nil || result.Rows != nil || s.cursors.entries["c"] != nil {
		t.Fatal("wrapped cursor ordinal", result, err)
	}
	sessionSQL(t, s, "ROLLBACK TO recover")
}

func TestSQLCursorOwnedParametersAndDeclarationSnapshot(t *testing.T) {
	s := cursorUnitSetup(t)
	marker := []byte("before")
	op, err := s.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	result, err := testExecuteFrame(s.Engine(), op, statement(t, "DECLARE c NO SCROLL CURSOR FOR SELECT i,$1::TEXT AS marker FROM unit_rows ORDER BY i"), []driver.NamedValue{{Ordinal: 1, Value: marker}})
	op.Done()
	if err != nil || result.Count != 0 || result.Rows != nil {
		t.Fatal(result, err)
	}
	copy(marker, "after!")
	sessionSQL(t, s, "UPDATE unit_rows SET i=99 WHERE i=1")
	sessionSQL(t, s, "INSERT INTO unit_rows VALUES(4)")
	cursorUnitBatch(t, s, "FETCH ALL FROM c", [][]driver.Value{{int64(1), "before"}, {int64(2), "before"}, {int64(3), "before"}})
	sessionSQL(t, s, "CLOSE c")
	sessionSQL(t, s, "DECLARE c NO SCROLL CURSOR FOR SELECT i FROM unit_rows ORDER BY i")
	cursorUnitBatch(t, s, "FETCH ALL FROM c", [][]driver.Value{{int64(2)}, {int64(3)}, {int64(4)}, {int64(99)}})
}

func TestSQLCursorFirstDemandExpressionData(t *testing.T) {
	s := cursorUnitSetup(t)
	sessionSQL(t, s, "DECLARE c NO SCROLL CURSOR FOR SELECT statement_timestamp() AS stamp FROM unit_rows")
	cursor := s.cursors.entries["c"]
	cursorUnitBatch(t, s, "FETCH 0 FROM c", [][]driver.Value{})
	if cursor.authority.expression != nil || cursor.stage != nil {
		t.Fatal("FETCH0 captures expression environment")
	}
	first := sessionSQL(t, s, "FETCH 1 FROM c")
	snapshot := cursor.authority.expression
	if snapshot == nil || snapshot.statementStart.IsZero() || cursor.authority.scope != nil {
		t.Fatal("missing data-only first demand")
	}
	sessionSQL(t, s, "SELECT statement_timestamp()")
	second := sessionSQL(t, s, "FETCH 1 FROM c")
	if cursor.authority.expression != snapshot || !reflect.DeepEqual(first.Rows.(*finalRows).values, second.Rows.(*finalRows).values) {
		t.Fatal("second FETCH recaptures stable expression data")
	}
	sessionSQL(t, s, "CLOSE c")
}

func TestSQLCursorForeignRegistryAndEmptyAuthority(t *testing.T) {
	s := cursorUnitSetup(t)
	sessionSQL(t, s, "DECLARE c NO SCROLL CURSOR FOR SELECT i FROM unit_rows")
	cursor := s.cursors.entries["c"]
	foreign := s.Engine().catalog.Domain.Session(s.token.ID() + 1)
	if err := s.cursors.EndSession(foreign, CleanClose); err == nil || s.cursors.entries["c"] != cursor {
		t.Fatal("foreign cleanup consumed cursor", err)
	}
	if cursor.source.scope != nil || cursor.authority.scope != nil {
		t.Fatal("finished declaration remains authoritative")
	}
	sessionSQL(t, s, "CLOSE c")
}

func TestSQLCursorCompletedPublicationBoundary(t *testing.T) {
	// Cancellation here is deliberately after Execute's successful publication
	// preflight, not inside demand (covered by CancellationClearsAuthority).
	completed := func(t *testing.T, s *Session, query string) Result {
		t.Helper()
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		op, err := s.Acquire(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer op.Done()
		scope, err := s.Engine().StartStatement(op)
		if err != nil {
			t.Fatal(err)
		}
		defer scope.Finish(context.Canceled)
		result, err := scope.Execute(statement(t, query), 0, nil)
		if result.Rows != nil {
			t.Cleanup(func() { result.Rows.Close() })
		}
		if err != nil || !scope.completed {
			t.Fatalf("%s did not reach completed boundary: result=%+v completed=%v error=%v", query, result, scope.completed, err)
		}
		cancel()
		if !errors.Is(op.ctx.Err(), context.Canceled) {
			t.Fatal("boundary context was not canceled")
		}
		if err := scope.Finish(nil); err != nil {
			t.Fatalf("%s: Finish retracts completed success: %v", query, err)
		}
		if err := scope.Finish(nil); err != nil {
			t.Fatalf("%s: repeated Finish changes success: %v", query, err)
		}
		if s.transactions.state != TxActive {
			t.Fatalf("%s aborts completed explicit transaction: %v", query, s.transactions.state)
		}
		return result
	}
	t.Run("move", func(t *testing.T) {
		s := cursorUnitSetup(t)
		sessionSQL(t, s, "DECLARE c SCROLL CURSOR FOR SELECT i FROM unit_rows ORDER BY i")
		result := completed(t, s, "MOVE FORWARD 2 FROM c")
		c := s.cursors.entries["c"]
		if result.Count != 2 || result.Rows != nil || c.authority.scope != nil || c.position != cursorOn || c.currentSource == nil {
			t.Fatal("completed MOVE loses count, physical authority or quiet output", result, c)
		}
		cursorUnitBatch(t, s, "FETCH PRIOR FROM c", [][]driver.Value{{int64(1)}})
	})
	t.Run("fetch-older-than-savepoint", func(t *testing.T) {
		s := cursorUnitSetup(t)
		sessionSQL(t, s, "DECLARE c NO SCROLL CURSOR FOR SELECT i FROM unit_rows ORDER BY i")
		cursor := s.cursors.entries["c"]
		sessionSQL(t, s, "SAVEPOINT recover")
		first := completed(t, s, "FETCH 1 FROM c")
		want := [][]driver.Value{{int64(1)}}
		if first.Count != 1 || !reflect.DeepEqual(tuples(first), want) {
			t.Fatalf("completed FETCH loses delivered row: count=%d rows=%v", first.Count, tuples(first))
		}
		rows := first.Rows.(*finalRows)
		if rows.source != nil || rows.anonymous != nil || cursor.authority.scope != nil || cursor.position != cursorOn {
			t.Fatal("completed FETCH is not detached or retains admission", rows, cursor)
		}
		// An older cursor's motion survives recovery. Row 1 was successfully
		// delivered, so the next batch must start at 2, never replay or skip it.
		sessionSQL(t, s, "ROLLBACK TO recover")
		if s.cursors.entries["c"] != cursor {
			t.Fatal("recovery discards older completed cursor")
		}
		cursorUnitBatch(t, s, "FETCH 1 FROM c", [][]driver.Value{{int64(2)}})
		cursorUnitBatch(t, s, "FETCH ALL FROM c", [][]driver.Value{{int64(3)}})
		sessionSQL(t, s, "CLOSE c")
		sessionSQL(t, s, "COMMIT")
		if !reflect.DeepEqual(tuples(first), want) {
			t.Fatal("recovery/close/commit mutates delivered detached row")
		}
	})
	t.Run("declare", func(t *testing.T) {
		s := cursorUnitSetup(t)
		sessionSQL(t, s, "SAVEPOINT recover")
		result := completed(t, s, "DECLARE c NO SCROLL CURSOR FOR SELECT i FROM unit_rows ORDER BY i")
		cursor := s.cursors.entries["c"]
		if result.Count != 0 || result.Rows != nil || cursor == nil || cursor.stage != nil || cursor.authority.scope != nil {
			t.Fatal("completed DECLARE loses quiet installation", result, cursor)
		}
		cursorUnitBatch(t, s, "FETCH 1 FROM c", [][]driver.Value{{int64(1)}})
		sessionSQL(t, s, "ROLLBACK TO recover")
		if s.cursors.entries["c"] != nil {
			t.Fatal("rollback retains declaration newer than savepoint")
		}
		sessionSQL(t, s, "COMMIT")
	})
	for _, query := range []string{"CLOSE c", "CLOSE ALL"} {
		t.Run(query, func(t *testing.T) {
			s := cursorUnitSetup(t)
			sessionSQL(t, s, "DECLARE c NO SCROLL CURSOR FOR SELECT i FROM unit_rows ORDER BY i")
			cursor := s.cursors.entries["c"]
			pin := cursor.source.view
			sessionSQL(t, s, "SAVEPOINT recover")
			result := completed(t, s, query)
			if result.Count != 0 || result.Rows != nil || s.cursors.entries["c"] != nil || pin.Load() != nil || cursor.authority.scope != nil {
				t.Fatal("completed CLOSE loses removal or leaks capture", result, cursor)
			}
			sessionSQL(t, s, "ROLLBACK TO recover")
			if s.cursors.entries["c"] != nil {
				t.Fatal("rollback resurrects completed CLOSE")
			}
			sessionSQL(t, s, "COMMIT")
		})
	}
}

func TestSQLCursorMetadataInstall(t *testing.T) {
	s := sessionStart(t, New(), nil)
	sessionSQL(t, s, "CREATE TABLE metadata_failure(i INTEGER)")
	sessionSQL(t, s, "INSERT INTO metadata_failure VALUES(2)")
	executeAt := func(query string, instant time.Time) {
		t.Helper()
		op, err := s.Acquire(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		defer op.Done()
		op.commandStart = instant
		result, err := s.Engine().Execute(op, statement(t, query), 0, nil)
		if err != nil {
			t.Fatal(err)
		}
		if result.Rows != nil {
			if err := result.Rows.Close(); err != nil {
				t.Fatal(err)
			}
		}
	}
	birth := time.Date(2024, 4, 5, 6, 7, 8, 123456000, time.UTC)
	executeAt("BEGIN", birth)
	source := " /* retained */ DECLARE clocked NO SCROLL CURSOR WITH HOLD FOR SELECT 1 AS i"
	op, err := s.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.Engine().Describe(op, statement(t, source))
	op.Done()
	if err != nil || len(s.cursors.entries) != 0 {
		t.Fatal("Describe installs declaration metadata", err)
	}
	declarationTime := birth.Add(time.Hour)
	executeAt(source, declarationTime)
	cursor := s.cursors.entries["clocked"]
	check := func() {
		t.Helper()
		instant, err := cursor.created.TimestampTime()
		if err != nil || !instant.Equal(declarationTime) || cursor.statement != source || !cursor.hold || cursor.scroll {
			t.Fatalf("declaration metadata changed: source=%q instant=%v error=%v", cursor.statement, instant, err)
		}
	}
	check()
	executeAt("FETCH 1 FROM clocked", birth.Add(2*time.Hour))
	check()
	executeAt("COMMIT", birth.Add(3*time.Hour))
	if !cursor.promoted || cursor.source != nil || cursor.root != nil {
		t.Fatal("HOLD not promoted")
	}
	check()
	executeAt("BEGIN", birth.Add(4*time.Hour))
	executeAt("ROLLBACK", birth.Add(5*time.Hour))
	if s.cursors.entries["clocked"] != cursor {
		t.Fatal("later transaction removed held metadata")
	}
	check()
	sessionSQL(t, s, "CLOSE clocked")
	if len(s.cursors.entries) != 0 {
		t.Fatal("CLOSE retains metadata")
	}

	// Producer failure releases execution but not registry metadata. Recovery
	// keeps the older declaration's stamp, until an actual CLOSE removes it.
	sessionSQL(t, s, "BEGIN")
	executeAt("DECLARE failed NO SCROLL CURSOR FOR SELECT 10/(i-2) AS i FROM metadata_failure", declarationTime)
	failed := s.cursors.entries["failed"]
	stamp := failed.created
	sessionSQL(t, s, "SAVEPOINT recover")
	cursorUnitError(t, s, "FETCH ALL FROM failed", "22012", "division by zero")
	sessionSQL(t, s, "ROLLBACK TO recover")
	if s.cursors.entries["failed"] != failed || failed.source != nil || failed.created != stamp || failed.statement == "" {
		t.Fatal("failed producer loses declaration metadata")
	}
	sessionSQL(t, s, "CLOSE failed")
	sessionSQL(t, s, "SAVEPOINT canceled")
	ctx, cancel := context.WithCancel(context.Background())
	op, err = s.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	result, err := s.Engine().Execute(op, statement(t, "DECLARE canceled NO SCROLL CURSOR FOR SELECT 1"), 0, nil)
	op.Done()
	if !errors.Is(err, context.Canceled) || result.Rows != nil || len(s.cursors.entries) != 0 {
		t.Fatal("canceled declaration publishes metadata", result, err)
	}
}
