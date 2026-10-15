package engine

import (
	"fmt"
	"slices"
	"time"

	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func (r *cursorRegistry) hasCurrentHold(ref TxRef) bool {
	if r == nil {
		return false
	}
	for _, c := range r.entries {
		if c.hold && !c.promoted && c.tx == ref && c.position != cursorFailed {
			return true
		}
	}
	return false
}

// Admission is already held; the transaction/engine/session mutexes are not.
// Ordinary commits avoid scope allocation and presentation capture entirely.
func (e *Engine) holdPrecommit(op *Operation, owner *transactionOwner, start time.Time) error {
	if !op.session.cursors.hasCurrentHold(owner.ref) {
		return nil
	}
	if start.IsZero() {
		start = statementOperationStart(op)
	}
	if !op.commandAdmission {
		op.snapshotNoticeThreshold()
	}
	scope := newStatementScope(op, owner, start)
	owner.mu.Lock()
	scope.attachTransactionLocked()
	owner.mu.Unlock()
	defer func() { scope.finished = true }()
	return scope.materializeHeldCursors()
}

func (s *StatementScope) materializeHeldCursors() error {
	r := s.op.session.cursors
	if !r.hasCurrentHold(s.txRef) {
		return nil
	}
	// Keep one bounded precommit visitation vector, not a second registry or
	// repeated map scans. Declaration ordinal is native's deterministic order;
	// tests assert promotion independently of PostgreSQL's hash traversal order.
	cursors := make([]*sqlCursor, 0, len(r.entries))
	for _, c := range r.entries {
		if c.hold && !c.promoted && c.tx == s.txRef && c.position != cursorFailed {
			cursors = append(cursors, c)
		}
	}
	slices.SortFunc(cursors, func(a, b *sqlCursor) int {
		if a.ordinal < b.ordinal {
			return -1
		}
		if a.ordinal > b.ordinal {
			return 1
		}
		return 0
	})
	defer func() { s.querySources, s.queryLocks = nil, nil }()
	for _, c := range cursors {
		if err := s.materializeHeldCursor(c); err != nil {
			return err
		}
	}
	return nil
}

func (s *StatementScope) authorizeSQLCursor(c *sqlCursor) error {
	e := s.op.session.Engine()
	e.mu.Lock()
	defer e.mu.Unlock()
	auth, err := e.authorizationLocked(s.op)
	if err == nil {
		err = s.validateLockedRelation(e)
	}
	if err == nil {
		err = auth.checkQuerySources(c.leaves)
	}
	if err == nil {
		err = s.captureIdentityLocked(auth)
	}
	return err
}

func (c *sqlCursor) openProducer(s *StatementScope) error {
	if c.stage != nil {
		return nil
	}
	c.authority.expression = &cursorExpressionData{identity: s.identity, statementStart: s.statementStart}
	c.state.retain = c.scroll
	stage, err := openQueryProjectStage(s.op.Context(), &c.state, c.root)
	if err != nil {
		return err
	}
	c.stage = &stage
	return nil
}

func (s *StatementScope) materializeHeldCursor(c *sqlCursor) (err error) {
	if c.epoch != s.txRef.epoch {
		return ErrCatalogReplaced
	}
	c.admit(s)
	authority := c.authority
	defer func() {
		authority.scope = nil
		if err != nil {
			c.position = cursorFailed
			_ = c.dispose(s.op.Context())
		}
	}()
	if err = s.authorizeSQLCursor(c); err != nil {
		return err
	}
	var values []sqltype.Value
	count := 0
	savedOrdinal, savedPosition, savedEnd := c.ordinalPosition, c.position, c.atEnd
	offset := savedOrdinal
	if c.scroll {
		if err = c.rewind(s.op.Context()); err != nil {
			return err
		}
		offset = 0
	}
	if c.position != cursorAfter {
		if err = c.openProducer(s); err != nil {
			return err
		}
		width := len(c.schema)
		for {
			max := int(^uint(0) >> 1)
			if count == max || len(values) > max-width {
				return fmt.Errorf("stuntgres: cursor materialization cardinality exhausted")
			}
			start := len(values)
			values = slices.Grow(values, width)
			values = values[:start+width]
			present, pullErr := c.stage.Next(s.op.Context(), values[start:])
			if pullErr != nil {
				return pullErr
			}
			if !present {
				values = values[:start]
				break
			}
			count++
		}
	}
	// Typed values/schema are already owned. Rendering maps belong to the
	// eventual FETCH's current presentation, never to commit materialization.
	if err = s.sessionPublicationPreflight(); err != nil {
		return err
	}
	if err = c.releaseProducer(s.op.Context()); err != nil {
		return err
	}
	c.values, c.count, c.storeOffset = values, count, offset
	c.ordinalPosition, c.position, c.atEnd = savedOrdinal, savedPosition, savedEnd
	c.authority = nil
	c.tx = TxRef{}
	c.promoted = true
	return nil
}

// Live producer output retains DECLARE interpretation. Exported output instead
// uses the ordinary current-catalog capture seam at each stored FETCH.
func (s *StatementScope) captureSQLCursorOutput(rows *finalRows, catalog CatalogRead) error {
	e := s.op.session.Engine()
	e.mu.Lock()
	auth, err := e.authorizationLocked(s.op)
	var path []ObjectAddress
	if err == nil {
		path, err = e.referencePath(auth)
	}
	var collationPath []ObjectAddress
	if err == nil {
		for _, value := range rows.values {
			if !value.IsNull() && value.Type().ContainsRegcollation() {
				collationPath, err = e.lookupPath(auth, collationLookup)
				break
			}
		}
	}
	e.mu.Unlock()
	if err != nil {
		return err
	}
	for _, value := range rows.values {
		if err := captureRegObjectOutputValue(rows, catalog, collationPath, value); err != nil {
			return err
		}
		if err := captureACLItemOutputValue(rows, capturedACLItemOutput{owner: e, roles: auth.roles}, value); err != nil {
			return err
		}
		if err := captureRegclassOutputValue(rows, catalog, path, value); err != nil {
			return err
		}
	}
	return nil
}

func (s *StatementScope) fetchHeldCursor(c *sqlCursor, fetch *parser.FetchCursor) (result Result, err error) {
	defer func() {
		if err != nil {
			c.position = cursorFailed
			_ = c.dispose(s.op.Context())
		}
	}()
	var rows *finalRows
	if !fetch.Move {
		rows = newFinalRows(c.schema, 0)
		rows.output = s.output
	}
	moved, err := c.motion(fetch, rows, s)
	if err != nil {
		return Result{}, err
	}
	if rows != nil {
		if err = s.op.session.Engine().captureRegclassOutput(s.op, rows); err != nil {
			return Result{}, err
		}
	}
	if err = s.sessionPublicationPreflight(); err != nil {
		return Result{}, err
	}
	s.completed, s.sessionCompleted = true, true
	result.Count = moved
	if rows != nil {
		result.Rows = rows
	}
	return result, nil
}
