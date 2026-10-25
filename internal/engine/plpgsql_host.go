package engine

import (
	"context"
	"fmt"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/proc"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// plHost is the root's proc.PLHost for one PL invocation. Each method takes
// Engine.mu or owner.Mu itself, as the root helpers it calls always did.
type plHost struct {
	s    *StatementScope
	d    *catalog.Procedure
	read catalog.Read
	run  *proc.PLRun
	// Query-FOR adapters by nesting depth: the outermost is inline, nested
	// ones are kept for reuse by later iterations of this invocation.
	depth  int
	first  plQueryRows
	nested []*plQueryRows
}

var _ proc.PLHost = (*plHost)(nil)

// plInvocation allocates a run and its host together.
type plInvocation struct {
	run  proc.PLRun
	host plHost
}

// newPLRun builds the walk for d over frame f. d may be nil only for units
// that never format a diagnostic.
func (s *StatementScope) newPLRun(d *catalog.Procedure, f *proc.PLFrame, read catalog.Read) *proc.PLRun {
	inv := &plInvocation{host: plHost{s: s, d: d, read: read}}
	inv.host.run = &inv.run
	inv.run = proc.PLRun{Host: &inv.host, Frame: f}
	if d != nil {
		inv.run.Definition = d.SQL
	}
	return &inv.run
}

func (h *plHost) Context() context.Context { return h.s.Op.Context() }

func (h *plHost) Identity() string { return h.s.plRoutineDiagnosticIdentity(h.read, h.d) }

func (h *plHost) LookupType(id sqltype.ID) (*sqltype.Descriptor, bool) {
	return h.read.LookupType(id)
}

func (h *plHost) TypeName(typ *sqltype.Descriptor) string {
	e := h.s.Engine()
	e.Mu.Lock()
	defer e.Mu.Unlock()
	binder := NewExpressionBinder(h.s.Op.Context(), e, h.s.Input, h.s.Op, nil)
	return binder.DiagnosticTypeName(typ)
}

func (h *plHost) Evaluate(f *proc.PLFrame, q *parser.Select, at proc.PLOrigin) (sqltype.Value, sqltype.Usage, error) {
	value, usage, _, err := h.s.evaluatePLExpression(f, q, h.read, nil, h.s.plNoticeOrigin(f, at.Position, at.Action))
	return value, usage, err
}

func (h *plHost) Convert(v sqltype.Value, target sqltype.Usage, at proc.PLOrigin) (sqltype.Value, error) {
	noticeStart := len(h.s.Op.Notices)
	v, err := h.s.convertPLValue(v, target)
	if at.Action != "" && noticeStart != len(h.s.Op.Notices) {
		h.s.appendPLNoticeContext(noticeStart, h.s.plNoticeOrigin(h.run.Frame, at.Position, at.Action))
	}
	return v, err
}

func (h *plHost) AssignTarget(f *proc.PLFrame, t proc.PLTarget, raw parser.Expr, q *parser.Select, at proc.PLOrigin) error {
	_, err := h.s.assignPLTarget(f, t, raw, q, h.read, h.s.plNoticeOrigin(f, at.Position, at.Action))
	return err
}

func (h *plHost) TargetUsage(f *proc.PLFrame, target proc.PLTarget) (sqltype.Usage, error) {
	usage := f.Usage(target.Slot)
	if f.Program.Variables[target.Slot].Record {
		current := f.Values[target.Slot]
		if current.Type() == nil {
			// Use the ordinary semantic-target binder's indeterminate-record error.
			_, err := h.s.preparePLTargetDatum(f, target, current, nil, nil)
			return sqltype.Usage{}, err
		}
		usage = current.Type().Usage()
	}
	e := h.s.Engine()
	if leaf := analyze.TargetUsage(usage, target.Steps); leaf.Valid() {
		return leaf, nil
	}
	e.Mu.Lock()
	defer e.Mu.Unlock()
	auth, err := e.AuthorizationLocked(h.s.Op)
	if err != nil {
		return sqltype.Usage{}, err
	}
	binder := NewExpressionBinder(h.s.Op.Context(), e, h.s.Input, h.s.Op, nil)
	binder.PL, binder.Query.Auth = f, &auth
	bound, err := binder.BindAssignmentTarget(catalog.Column{Name: f.Program.Variables[target.Slot].Name, Usage: usage}, target.Steps, parser.Position{})
	if err != nil {
		return sqltype.Usage{}, err
	}
	return bound.Destination, nil
}

func (h *plHost) AssignValue(f *proc.PLFrame, target proc.PLTarget, value sqltype.Value, at proc.PLOrigin) (err error) {
	noticeStart := len(h.s.Op.Notices)
	defer func() { h.s.appendPLNoticeContext(noticeStart, h.s.plNoticeOrigin(f, at.Position, at.Action)) }()
	candidate, err := h.s.preparePLTargetValue(h.run, f, target, value, f.Values[target.Slot], h.read)
	if err == nil {
		f.Values[target.Slot] = candidate
	}
	return err
}

func (h *plHost) BeginException() (proc.PLCheckpoint, error) {
	c, err := h.s.beginPLException()
	if err != nil {
		return nil, err
	}
	return c, nil
}

func (h *plHost) EndException(c proc.PLCheckpoint, rollback bool, primary error) error {
	checkpoint, err := plCheckpointOf(c)
	if err != nil {
		return err
	}
	return h.s.endPLException(checkpoint, rollback, primary)
}

func (h *plHost) OpenQuery(f *proc.PLFrame, in proc.PLInstruction) (proc.PLRows, error) {
	q := h.beginQueryRows(f, in)
	if err := q.open(f, in); err != nil {
		return nil, err
	}
	return q, nil
}

func (h *plHost) Cursor(r *proc.PLRun, f *proc.PLFrame, in proc.PLInstruction) (sqltype.Value, proc.PLFlow, error) {
	return h.s.executePLCursor(h.d, r, f, in, h.read)
}

func (h *plHost) Message(f *proc.PLFrame, in proc.PLInstruction) error {
	if in.Raise != nil {
		return h.s.executePLRaise(h.d, f, in, h.read)
	}
	return h.s.executePLAssert(h.d, f, in, h.read)
}

// plCheckpointOf recovers the root checkpoint behind a proc.PLCheckpoint.
func plCheckpointOf(c proc.PLCheckpoint) (*plExceptionCheckpoint, error) {
	checkpoint, ok := c.(*plExceptionCheckpoint)
	if !ok || checkpoint == nil {
		return nil, fmt.Errorf("stuntgres: foreign PL exception checkpoint %T", c)
	}
	return checkpoint, nil
}

// plColumns adapts a catalog schema to proc.PLColumns without copying it.
type plColumns []catalog.Column

func (c *plColumns) Width() int { return len(*c) }

func (c *plColumns) Column(i int) (string, sqltype.Usage) { return (*c)[i].Name, (*c)[i].Usage }
