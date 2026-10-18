package engine

import (
	"fmt"
	"math"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

type blockingInquiryKind uint8

const (
	ordinaryBlockingInquiry blockingInquiryKind = iota
	safeSnapshotBlockingInquiry
)

func (k blockingInquiryKind) name() string {
	switch k {
	case ordinaryBlockingInquiry:
		return "pg_blocking_pids"
	case safeSnapshotBlockingInquiry:
		return "pg_safe_snapshot_blocking_pids"
	}
	return ""
}

type blockingInquiryExpression struct {
	expressionInfo
	kind blockingInquiryKind
	arg  BoundExpr
}

func (b *expressionBinder) bindBlockingInquiry(raw parser.FunctionExpr) (BoundExpr, error) {
	var operand comparisonOperand
	var single [1]string
	names := single[:0]
	if len(raw.Args) == 1 {
		names = single[:]
	} else if len(raw.Args) <= 100 {
		names = make([]string, len(raw.Args))
	}
	for i, child := range raw.Args {
		arg, err := b.comparisonOperand(child)
		if err != nil {
			return nil, err
		}
		if len(raw.Args) == 1 {
			operand = arg
		}
		if len(raw.Args) <= 100 {
			name := "unknown"
			if typ := arg.descriptor(); typ != nil {
				name = typ.DiagnosticName()
			}
			names[i] = functionNamedType(raw, i, name)
		}
	}
	if len(raw.Args) > 100 {
		return nil, expressionError("54023", "cannot pass more than 100 arguments to a function", raw.Pos)
	}
	visible, err := b.builtinVisible(raw.Name, functionLookup)
	if err != nil {
		return nil, err
	}
	name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	kind := ordinaryBlockingInquiry
	if name == safeSnapshotBlockingInquiry.name() {
		kind = safeSnapshotBlockingInquiry
	}
	if !visible || name != kind.name() || len(raw.Args) != 1 || raw.Variadic || len(raw.ArgNames) != 0 {
		return nil, b.functionSignatureErrorForTypes(raw.Name, raw.Pos, names)
	}
	target := b.typ(sqltype.Int4).Usage()
	arg, err := b.finalizeOperand(operand, target.Type)
	if err != nil {
		return nil, err
	}
	if arg.ResultUsage() != target {
		plan, failure := sqltype.PlanCoercion(arg.ResultUsage(), target, sqltype.Implicit)
		if failure != nil {
			if d, ok := failure.(*sqlstate.Error); ok && (d.Code == "42804" || d.Code == "42846") {
				return nil, b.functionSignatureErrorForTypes(raw.Name, raw.Pos, names)
			}
			return nil, failure
		}
		arg = coercionExpression{b.info(target, raw.Args[0].SourcePosition(), coercionProperties(arg, plan)), arg, plan, sqltype.Implicit, false}
	}
	properties := arg.Properties()
	properties.Volatility = Volatile
	info, err := b.builtinRoutineInfo(b.info(b.typ(target.Type.AssociatedArray()).Usage(), raw.Pos, properties), catalogRoutineProvider, kind.name(), sqltype.Int4)
	if err != nil {
		return nil, err
	}
	return blockingInquiryExpression{info, kind, arg}, nil
}

func (n blockingInquiryExpression) arguments() []BoundExpr { return []BoundExpr{n.arg} }

func (n blockingInquiryExpression) validateStored() error {
	if n.kind.name() == "" || n.arg == nil || n.arg.ResultUsage().Type == nil || n.arg.ResultUsage() != n.arg.ResultUsage().Type.Usage() || n.arg.ResultUsage().Type.ID() != sqltype.Int4 {
		return fmt.Errorf("stuntgres: invalid stored blocking inquiry argument")
	}
	properties := n.arg.Properties()
	properties.Volatility = Volatile
	if n.usage.Type == nil || n.usage != n.usage.Type.Usage() || n.usage.Type.ID() != n.arg.ResultUsage().Type.AssociatedArray() || n.usage.Type.Element() != n.arg.ResultUsage().Type || n.Properties() != properties {
		return fmt.Errorf("stuntgres: invalid stored blocking inquiry signature")
	}
	return nil
}

func (n blockingInquiryExpression) Eval(c *EvalContext) (sqltype.Value, error) {
	if err := evaluationContext(c); err != nil {
		return sqltype.Value{}, err
	}
	value, err := n.arg.Eval(c)
	if err != nil {
		return sqltype.Value{}, err
	}
	if value.IsNull() {
		return sqltype.Null(n.usage.Type), nil
	}
	pid, err := value.Integer()
	if err != nil {
		return sqltype.Value{}, err
	}
	scope := c.liveScope()
	if scope == nil {
		return sqltype.Value{}, fmt.Errorf("stuntgres: blocking inquiry lacks statement authority")
	}
	e := scope.op.session.Engine()
	// Validate the statement under its existing engine authority, then release
	// it before entering ordinary manager metadata. Never nest these mutexes.
	e.mu.Lock()
	_, err = scope.writeAuthorizationLocked(e)
	var elements []sqltype.Value
	if err == nil && pid > 0 && n.kind == safeSnapshotBlockingInquiry {
		elements, err = e.safeSnapshotBlockingPidsLocked(SessionID(pid), n.usage.Type.Element())
	}
	e.mu.Unlock()
	if err != nil {
		return sqltype.Value{}, err
	}
	if pid > 0 && n.kind == ordinaryBlockingInquiry {
		elements, err = e.locks.blockingPids(SessionID(pid), n.usage.Type.Element())
		if err != nil {
			return sqltype.Value{}, err
		}
	}
	if n.kind.name() == "" {
		return sqltype.Value{}, fmt.Errorf("stuntgres: invalid blocking inquiry kind")
	}
	var dimensions []sqltype.ArrayDimension
	if len(elements) != 0 {
		dimensions = []sqltype.ArrayDimension{{Lower: 1, Length: int32(len(elements))}}
	}
	return sqltype.OwnedArrayValue(n.usage.Type, dimensions, elements)
}

func appendBlockingPid(values []sqltype.Value, typ *sqltype.Descriptor, token SessionToken, prepared bool, domain *Domain) ([]sqltype.Value, error) {
	if !prepared && (token.domain != domain || token.private != nil || token.id == 0) {
		return values, nil
	}
	var pid int64
	if !prepared {
		if token.id > math.MaxInt32 {
			return nil, fmt.Errorf("stuntgres: blocking session identity exceeds integer range")
		}
		pid = int64(token.id)
	}
	value, err := sqltype.Integer(typ, pid)
	if err != nil {
		return nil, err
	}
	return append(values, value), nil
}

// Caller holds Engine.mu. These flags describe an unresolved acquisition,
// unlike possible/ready alone, which also exist for nondeferrable readers.
func (e *Engine) safeSnapshotBlockingPidsLocked(pid SessionID, typ *sqltype.Descriptor) ([]sqltype.Value, error) {
	for ref, node := range e.serializable.nodes {
		if ref.domain != e.domain || ref.epoch != e.state.epoch || ref.session != (SessionToken{domain: e.domain, id: pid}) || node.prepared || !node.captured || !node.readOnly || !node.deferrable || node.aborted || node.ended || node.committed || node.resolved || node.ready == nil {
			continue
		}
		var values []sqltype.Value
		for writer := range node.possible {
			if writer.ref.domain != e.domain || writer.ref.epoch != ref.epoch || e.serializable.nodes[writer.ref] != writer || writer.aborted || writer.ended || writer.committed {
				continue
			}
			var err error
			values, err = appendBlockingPid(values, typ, writer.ref.session, writer.prepared, e.domain)
			if err != nil {
				return nil, err
			}
		}
		return values, nil
	}
	return nil, nil
}

func (m *relationLockManager) validInquiryRequest(r *relationRequest, entry *relationEntry, resource lockResource) bool {
	if r == nil || r.terminal || r.entry != entry || r.ref != resource || !m.validRequest(r) {
		return false
	}
	if _, journaled := r.ticket.resources[resource]; !journaled {
		return false
	}
	if r.ticket.persistent {
		_, journaled := m.owners[r.provenance].resources[resource]
		return journaled
	}
	return true
}

func (m *relationLockManager) pendingInquiryRequest(t *relationTicket) *relationRequest {
	if !m.valid(t) || t.detached {
		return nil
	}
	for resource := range t.resources {
		entry := m.entries[resource]
		if entry == nil {
			continue
		}
		for _, r := range entry.pending {
			if r.ticket == t && m.validInquiryRequest(r, entry, resource) {
				return r
			}
		}
	}
	return nil
}

// Only manager metadata is held. Inspect the authoritative journal and resource
// queue without granting, settling, copying the graph, or invoking callbacks.
func (m *relationLockManager) blockingPids(pid SessionID, typ *sqltype.Descriptor) ([]sqltype.Value, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	token := SessionToken{domain: m.domain, id: pid}
	r := m.pendingInquiryRequest(m.sessions[token])
	if r == nil {
		for _, t := range m.owners {
			if t.session == token && !t.detached {
				r = m.pendingInquiryRequest(t)
				if r != nil {
					break
				}
			}
		}
	}
	if r == nil {
		return nil, nil
	}
	var values []sqltype.Value
	// Deduplicate by participant, never by projected PID: prepared owners with
	// different TxRefs must retain separate zero entries. Scanning the small
	// resource-local prefixes avoids allocating a second identity registry.
	appendParticipant := func(t *relationTicket) error {
		var err error
		values, err = appendBlockingPid(values, typ, t.session, t.detached, m.domain)
		return err
	}
	for t, held := range r.entry.holders {
		if !m.valid(t) || sameLockSession(t, r.ticket) || !lockConflict(r.mode, held.mode) {
			continue
		}
		// A participant can have session and transaction tickets. Pick one
		// canonical ticket without sorting/copying the holder map.
		duplicate := false
		for other, hold := range r.entry.holders {
			if other == t || !m.valid(other) || !sameLockSession(t, other) || !lockConflict(r.mode, hold.mode) {
				continue
			}
			if other.persistent && !t.persistent || other.persistent == t.persistent && other.ref.id < t.ref.id {
				duplicate = true
				break
			}
		}
		if !duplicate {
			if err := appendParticipant(t); err != nil {
				return nil, err
			}
		}
	}
	if r.ref.completion != nil || r.ref.row != 0 && r.mode == noKeyUpdate {
		return values, nil
	}
	for i, earlier := range r.entry.pending {
		if earlier == r {
			break
		}
		if !m.validInquiryRequest(earlier, r.entry, r.ref) || sameLockSession(earlier.ticket, r.ticket) || !lockConflict(r.mode, earlier.mode) {
			continue
		}
		duplicate := false
		for t, hold := range r.entry.holders {
			if m.valid(t) && sameLockSession(t, earlier.ticket) && lockConflict(r.mode, hold.mode) {
				duplicate = true
				break
			}
		}
		if !duplicate {
			for _, previous := range r.entry.pending[:i] {
				if m.validInquiryRequest(previous, r.entry, r.ref) && sameLockSession(previous.ticket, earlier.ticket) && lockConflict(r.mode, previous.mode) {
					duplicate = true
					break
				}
			}
		}
		if !duplicate {
			if err := appendParticipant(earlier.ticket); err != nil {
				return nil, err
			}
		}
	}
	return values, nil
}
