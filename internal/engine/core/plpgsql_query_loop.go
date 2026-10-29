package core

import (
	"context"
	"fmt"
	"slices"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/proc"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// plQueryRows is an open query-FOR producer under the loop's lexical
// activation. The private producer and its PL body still belong to this
// function; nested SQL calls get their own cleared activation and restore it.
type plQueryRows struct {
	host      *plHost
	position  parser.Position
	origin    plNoticeOrigin
	restore   func(error) error
	result    Result
	schema    []catalog.Column
	previous  *plQueryRows
	readOnly  bool
	opening   parser.Statement // Identity of the command currently acquiring its canonical strategy.
	held      bool
	values    []sqltype.Value
	offset    int
	remaining int // Cardinality is independent of width, including empty records.
}

var _ proc.PLRows = (*plQueryRows)(nil)

// beginQueryRows takes the adapter for the next nesting depth and starts the
// loop's activation. Close ends both.
func (h *plHost) beginQueryRows(f *proc.PLFrame, in proc.PLInstruction) *plQueryRows {
	q := &h.first
	if h.depth != 0 {
		for len(h.nested) < h.depth {
			h.nested = append(h.nested, new(plQueryRows))
		}
		q = h.nested[h.depth-1]
	}
	h.depth++
	s := h.s
	diagnosticIdentity := s.sqlRoutine.plDiagnostic
	*q = plQueryRows{host: h, position: in.Syntax.Pos, origin: s.plNoticeOrigin(f, in.Syntax.Pos, proc.PLQueryLoopAction)}
	q.previous, s.plLoops = s.plLoops, q
	q.restore = s.ActivateSQLRoutine(nil, h.read)
	s.sqlRoutine.lexical, s.sqlRoutine.scalar, s.sqlRoutine.rows = f, false, true
	s.sqlRoutine.plDiagnostic = diagnosticIdentity
	return q
}

// open plans and executes the loop query. On failure it has already closed q.
func (q *plQueryRows) open(f *proc.PLFrame, in proc.PLInstruction) (err error) {
	s := q.host.s
	host := q.host
	previous := host.setOrigin(proc.PLOrigin{Position: q.position, Action: proc.PLQueryLoopAction})
	defer func() { host.live.origin = previous }()
	noticeStart, sourceComplete := len(s.Op.Notices), false
	defer func() {
		if !sourceComplete {
			s.appendPLNoticeContext(noticeStart, q.origin)
		}
	}()
	raw, query := in.Syntax.QueryLoop, in.QueryLoop.Query
	q.opening = query
	defer func() { q.opening = nil }()
	planningError := func(cause error) error {
		diagnostic := proc.PLDiagnosticQuery{Text: parser.PLExpressionSource(f.Program.Source, raw.Start, raw.End), Offset: raw.Start.Character - 1, Callback: proc.PLSQLCallback}
		return s.plRuntimeQueryError(cause, q.host.read, q.host.d, q.position, proc.PLQueryLoopAction, diagnostic)
	}
	s.sqlRoutine.plPlanning = true
	if err = s.AcquireRelation(query, true); err != nil {
		return q.Close(planningError(err))
	}
	description, err := s.Engine().describeOperation(s, query)
	if err != nil {
		return q.Close(planningError(err))
	}
	s.Expected = &description
	if !nestedTupleStatement(query) {
		return q.Close(q.queryError(SQLError("42P11", "cannot open "+RoutineCommandTag(query)+" query as cursor")))
	}
	finishCommand, err := s.BeginRoutineCommand()
	if err != nil {
		return q.Close(err)
	}
	result, executionErr := s.Engine().executeStatement(s, query, f.Program.FormalCount, func(_ context.Context, _ analyze.ParameterInfo, dst []sqltype.Value, _ sqltype.InputEnvironment) error {
		for i := range dst {
			dst[i] = f.Parameter(i)
		}
		return nil
	})
	// DML and modifying WITH have completed eagerly. Settle their command before
	// any body SQL can observe the effects or create its own command identity.
	err = finishCommand(executionErr)
	s.appendPLNoticeContext(noticeStart, q.origin)
	sourceComplete = true
	q.result = result
	if err != nil {
		if s.sqlRoutine.plPlanning {
			return q.Close(planningError(err))
		}
		return q.Close(q.queryError(err))
	}
	if result.Rows == nil {
		return q.Close(fmt.Errorf("stuntgres: query FOR has no row producer"))
	}
	q.schema = result.Rows.Schema()
	return nil
}

func (q *plQueryRows) queryError(cause error) error {
	return q.host.s.plRuntimeQueryError(cause, q.host.read, q.host.d, q.position, proc.PLQueryLoopAction, proc.PLDiagnosticQuery{})
}

func (q *plQueryRows) Width() int { return len(q.schema) }

func (q *plQueryRows) Column(i int) (string, sqltype.Usage) {
	return q.schema[i].Name, q.schema[i].Usage
}

func (q *plQueryRows) Next(ctx context.Context, dst []sqltype.Value) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if q.held {
		if q.remaining == 0 {
			return false, nil
		}
		copy(dst, q.values[q.offset:q.offset+len(dst)])
		q.offset += len(dst)
		q.remaining--
		return true, nil
	}
	s := q.host.s
	previous := q.host.setOrigin(proc.PLOrigin{Position: q.position, Action: proc.PLQueryLoopAction})
	defer func() { q.host.live.origin = previous }()
	noticeStart := len(s.Op.Notices)
	present, err := q.result.Rows.Next(ctx, dst)
	s.appendPLNoticeContext(noticeStart, q.origin)
	return present, err
}

// Close closes the producers, then ends the loop's activation. It releases
// the adapter for reuse by the next query loop at this depth.
func (q *plQueryRows) Close(primary error) error {
	s, err := q.host.s, primary
	host := q.host
	previous := host.setOrigin(proc.PLOrigin{Position: q.position, Action: proc.PLQueryLoopAction})
	defer func() { host.live.origin = previous }()
	noticeStart := len(s.Op.Notices)
	err = closePLCapture(q.result, err, q.queryError)
	s.appendPLNoticeContext(noticeStart, q.origin)
	err = q.restore(err)
	s.plLoops = q.previous
	*q = plQueryRows{}
	host.depth--
	return err
}

// promote detaches the transaction producer but leaves the loop activation
// alive. Values already own immutable typed payloads; only the row vector grows.
func (q *plQueryRows) promote() error {
	if q.held {
		return nil
	}
	width := len(q.schema)
	var values []sqltype.Value
	count := 0
	for {
		max := int(^uint(0) >> 1)
		if count == max || width > max-len(values) {
			return fmt.Errorf("stuntgres: cursor materialization cardinality exhausted")
		}
		start := len(values)
		values = slices.Grow(values, width)
		values = values[:start+width]
		present, err := q.result.Rows.Next(q.host.Context(), values[start:])
		if err != nil {
			return err
		}
		if !present {
			values = values[:start]
			break
		}
		count++
	}
	result := q.result
	q.result = Result{}
	if err := closePLCapture(result, nil, func(err error) error { return err }); err != nil {
		return err
	}
	q.values, q.held = values, true
	q.remaining = count
	return nil
}
