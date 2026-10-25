package core

import (
	"fmt"
	"slices"
	"time"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/regobj"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/view"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func (r *cursorRegistry) hasCurrentHold(ref ids.TxRef) bool {
	if r == nil {
		return false
	}
	for _, c := range r.Entries {
		if c.hold && !c.promoted && c.tx == ref && c.Place != exec.CursorFailed {
			return true
		}
	}
	return false
}

// Admission is already held; the transaction/engine/session mutexes are not.
// Ordinary commits avoid scope allocation and presentation capture entirely.
func (e *Engine) holdPrecommit(op *session.Operation, owner *session.TransactionOwner, start time.Time, settlement *StatementScope) error {
	if !PayloadOf(op.Session).Cursors.hasCurrentHold(owner.Ref) {
		return nil
	}
	if start.IsZero() {
		start = op.StatementStart()
	}
	if !op.CommandAdmission {
		op.SnapshotNoticeThreshold()
	}
	scope := newStatementScope(op, owner, start)
	owner.Mu.Lock()
	scope.attachTransactionLocked()
	owner.Mu.Unlock()
	scope.setForeignStatements(settlement.foreignTablesOrZero().statements)
	settlement.foreignTablesSlot.clearStatements()
	defer func() {
		settlement.setForeignStatements(scope.foreignTablesOrZero().statements)
		scope.foreignTablesSlot.clearStatements()
		settlement.setForeignHoldPromotions(scope.foreignTablesOrZero().holdPromotions)
		scope.foreignTablesSlot.clearHoldPromotions()
		scope.RoutineAuth = AuthorizationView{}
		scope.RoutineDependencies = nil
		scope.routinePrograms = nil
		scope.checkPrograms = nil
		scope.Domains.endAdmission()
		scope.partitionsSlot.DropPrograms()
		scope.Finished = true
	}()
	return scope.materializeHeldCursors()
}

func (s *StatementScope) materializeHeldCursors() error {
	r := PayloadOf(s.Op.Session).Cursors
	if !r.hasCurrentHold(s.TxRef) {
		return nil
	}
	// Keep one bounded precommit visitation vector, not a second registry or
	// repeated map scans. Declaration ordinal is native's deterministic order;
	// tests assert promotion independently of PostgreSQL's hash traversal order.
	cursors := make([]*sqlCursor, 0, len(r.Entries))
	for _, c := range r.Entries {
		if c.hold && !c.promoted && c.tx == s.TxRef && c.Place != exec.CursorFailed {
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
	defer func() { s.QuerySources, s.QueryLockContext = nil, nil }()
	// Reuse the visitation vector as the exact successful-promotion list.
	// A failed current promotion is disposed by materializeHeldCursor itself.
	promotions := cursors[:0]
	defer func() {
		s.setForeignHoldPromotions(promotions)
		clear(cursors[len(promotions):])
	}()
	for _, c := range cursors {
		if err := s.materializeHeldCursor(c); err != nil {
			return err
		}
		promotions = append(promotions, c)
	}
	return nil
}

func (s *StatementScope) authorizeSQLCursor(c *sqlCursor) error {
	// A live portal continues its initialized executor. Current catalog identity
	// and original lifetimes still fence it; ACL edits do not reinitialize it.
	e := s.Engine()
	e.Mu.Lock()
	auth, err := e.AuthorizationLocked(s.Op)
	if err == nil {
		err = s.ValidateLockedRelation(e)
	}
	if err == nil && !s.protocolOrZero().resume {
		err = analyze.CheckQuerySources(auth, c.leaves)
	}
	if err == nil {
		err = s.CaptureIdentityLocked(auth)
	}
	if err == nil {
		for _, dependency := range c.routineDependencies {
			switch dependency.Ref.Address.ClassID {
			case ids.ProcedureClassID, ids.TypeClassID:
				// These are the original callable and typed binding lifetimes.
			case ids.RelationClassID:
				original, _ := c.routineAuth.Catalog.Relation(dependency.Ref.Address)
				if original == nil || original.View == nil {
					// Base source shape/storage is fenced by the existing query
					// source guards, not unrelated metadata revision equality.
					continue
				}
			default:
				continue
			}
			var descriptor catalog.Descriptor
			descriptor, err = auth.Object(dependency.Ref)
			if err != nil {
				break
			}
			if descriptor.Revision() != dependency.Revision {
				err = catalog.Conflict{}
				break
			}
		}
	}
	e.Mu.Unlock()
	if err != nil {
		return err
	}
	if err := s.readmitPreparedPrograms(auth); err != nil {
		return err
	}
	evaluation := c.state.Evaluation(s.Op.Context())
	return view.ValidateQueryJoinExecution(&evaluation, c.root, auth.Catalog, 0)
}

func (c *sqlCursor) openProducer(s *StatementScope) error {
	if c.stage != nil {
		return nil
	}
	c.authority.Expression = &exec.CursorExpressionData{Identity: s.Identity, StatementStart: s.statementStart}
	c.state.Retain = c.scroll
	stage, err := exec.OpenQueryProjectStage(s.Op.Context(), &c.state, c.root)
	if err != nil {
		return err
	}
	c.stage = &stage
	return nil
}

func (s *StatementScope) materializeHeldCursor(c *sqlCursor) (err error) {
	if c.epoch != s.TxRef.Epoch() {
		return ids.ErrCatalogReplaced
	}
	c.admit(s)
	authority := c.authority
	defer func() {
		s.RoutineAuth = AuthorizationView{}
		s.RoutineDependencies = nil
		authority.Host = nil
		if err != nil {
			c.Place = exec.CursorFailed
			_ = c.dispose(s.Op.Context())
		}
	}()
	if err = s.authorizeSQLCursor(c); err != nil {
		return err
	}
	var values []sqltype.Value
	count := 0
	savedOrdinal, savedPosition, savedEnd := c.OrdinalPosition, c.Place, c.AtEnd
	offset := savedOrdinal
	if c.scroll {
		if err = c.Rewind(c.motionInput(s)); err != nil {
			return err
		}
		offset = 0
	}
	if c.Place != exec.CursorAfter {
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
			present, pullErr := c.stage.Next(s.Op.Context(), values[start:])
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
	if err = c.releaseProducer(s.Op.Context()); err != nil {
		return err
	}
	c.values, c.count, c.storeOffset = values, count, offset
	c.OrdinalPosition, c.Place, c.AtEnd = savedOrdinal, savedPosition, savedEnd
	c.authority = nil
	c.tx = ids.TxRef{}
	c.promoted = true
	return nil
}

// Live producer output retains DECLARE interpretation. Exported output instead
// uses the ordinary current-catalog capture seam at each stored FETCH.
func (s *StatementScope) captureSQLCursorOutput(rows *exec.FinalRows, catalog catalog.Read) error {
	e := s.Engine()
	e.Mu.Lock()
	auth, err := e.AuthorizationLocked(s.Op)
	var path []ids.ObjectAddress
	if err == nil {
		path, err = ReferencePath(auth)
	}
	var collationPath []ids.ObjectAddress
	if err == nil {
		for _, value := range rows.Values {
			if !value.IsNull() && value.Type().ContainsRegcollation() {
				collationPath, err = LookupPath(auth, analyze.CollationLookup)
				break
			}
		}
	}
	e.Mu.Unlock()
	if err != nil {
		return err
	}
	for _, value := range rows.Values {
		if err := regobj.CaptureRoleOutputValue(rows, regobj.RoleOutput{Owner: &e.Catalog.Owner, Roles: auth.Roles, QuoteAll: rows.Output.QuoteAllIdentifiers}, value); err != nil {
			return err
		}
		if err := regobj.CaptureObjectOutputValue(rows, catalog, collationPath, value); err != nil {
			return err
		}
		if err := regobj.CaptureACLItemOutputValue(rows, regobj.ACLItemOutput{Owner: &e.Catalog.Owner, Roles: auth.Roles}, value); err != nil {
			return err
		}
		if err := regobj.CaptureClassOutputValue(rows, catalog, path, value); err != nil {
			return err
		}
	}
	return nil
}

func (s *StatementScope) fetchHeldCursor(c *sqlCursor, fetch *parser.FetchCursor) (result Result, err error) {
	defer func() {
		if err != nil {
			c.Place = exec.CursorFailed
			_ = c.dispose(s.Op.Context())
		}
	}()
	var rows *exec.FinalRows
	if !fetch.Move {
		rows = exec.NewFinalRows(c.schema, 0)
		rows.Output = s.Output
	}
	moved, err := c.Move(fetch, rows, c.motionInput(s))
	if err != nil {
		return Result{}, err
	}
	if rows != nil {
		if err = s.Engine().captureRegclassOutput(s.Op, rows); err != nil {
			return Result{}, err
		}
	}
	if err = s.sessionPublicationPreflight(); err != nil {
		return Result{}, err
	}
	result.Count = moved
	if rows != nil {
		result.Rows = rows
	}
	return result, nil
}
