package engine

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/dml"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/fdw"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/engine/view"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// Called at the common physical-target dispatch, after binding/ACL/read-only
// admission but before any physical data view or row-version acquisition.
func (e *Engine) executeForeignCommandLocked(s *StatementScope, stmt parser.Statement, execution *preparedCommandExecution, parameters []sqltype.Value) (Result, error) {
	b := execution.binding
	if b.Insert.Conflict != nil {
		e.mu.Unlock()
		return Result{}, sqlError("0A000", "ON CONFLICT on foreign tables is unsupported in embedded mode")
	}
	if b.CurrentOf != "" {
		e.mu.Unlock()
		return Result{}, sqlError("0A000", "positioned foreign modification is unsupported in embedded mode")
	}
	op := catalog.ForeignInsert
	switch stmt.(type) {
	case *parser.Update:
		op = catalog.ForeignUpdate
	case *parser.Delete:
		op = catalog.ForeignDelete
	}
	invoker := execution.auth.State.CurrentUser
	if b.ViewWrite != nil {
		for _, layer := range view.ViewWriteOf(b.ViewWrite).Layers {
			invoker = layer.Relation.Owner
			if stored.ViewOf(layer.Relation).Invoker() {
				invoker = execution.auth.State.CurrentUser
			}
		}
	}
	target, err := e.captureForeignSourceLocked(s, b.Ref, b.Relation, invoker, nil, plan.RowMark{})
	if err != nil {
		e.mu.Unlock()
		return Result{}, err
	}
	if err = fdw.OperationSupported(target.Foreign.Wrapper, op, b.Relation.Name()); err != nil {
		e.mu.Unlock()
		return Result{}, err
	}
	inputs, err := e.captureQuerySourcesLocked(s, b.QuerySources, len(b.QuerySources) == 0)
	if err == nil && b.Returning != nil {
		err = e.captureCommentSnapshotLocked(s)
	}
	e.mu.Unlock()
	if inputs != nil {
		defer inputs.Close()
	}
	if err != nil {
		return Result{}, err
	}
	s.foreignUnlocked()
	outcome, err := dml.ExecuteForeign(s.writeHost(), dml.ForeignCommand{
		Binding: b, Op: op, Foreign: target.Foreign, Inputs: inputs, Parameters: parameters,
		Evaluation: execution.evaluation, QueryRoot: execution.queryRoot,
		Catalog: execution.auth.Catalog, Expressions: execution.expressions,
	})
	if err != nil {
		return Result{}, err
	}
	return outcomeResult(outcome), nil
}

// foreignTableScope is the statement's foreign-table slot: the foreign scan
// owner and cursor, the FDW participants and modifications of this statement,
// and the hold-cursor promotions owned by this completion.
type foreignTableScope struct {
	owner          *exec.ForeignScanOwner
	cursor         *sqlCursor
	statements     []*fdw.Participant
	modifications  []*fdw.Modification
	holdPromotions []*sqlCursor // Exact new promotions owned by this completion.
}

// foreignTables returns the statement's foreignTables slot, allocating it on first use.
func (s *StatementScope) foreignTables() *foreignTableScope {
	if s.foreignTablesSlot == nil {
		s.foreignTablesSlot = new(foreignTableScope)
	}
	return s.foreignTablesSlot
}

// foreignTablesOrZero reads the foreignTables slot; a nil slot reads as zero.
func (s *StatementScope) foreignTablesOrZero() foreignTableScope {
	if s.foreignTablesSlot == nil {
		return foreignTableScope{}
	}
	return *s.foreignTablesSlot
}

// takeOwner detaches the statement's foreign scan owner, if any.
func (f *foreignTableScope) takeOwner() *exec.ForeignScanOwner {
	if f == nil {
		return nil
	}
	owner := f.owner
	f.owner = nil
	return owner
}

// clearStatements forgets the statement's foreign participants.
func (f *foreignTableScope) clearStatements() {
	if f != nil {
		f.statements = nil
	}
}

// clearHoldPromotions forgets this completion's hold-cursor promotions.
func (f *foreignTableScope) clearHoldPromotions() {
	if f != nil {
		f.holdPromotions = nil
	}
}

// The setters below install a value, allocating the slot only for a
// non-nil value: a missing slot already reads as nil.

func (s *StatementScope) setForeignStatements(statements []*fdw.Participant) {
	if statements != nil || s.foreignTablesSlot != nil {
		s.foreignTables().statements = statements
	}
}

func (s *StatementScope) setForeignHoldPromotions(promotions []*sqlCursor) {
	if promotions != nil || s.foreignTablesSlot != nil {
		s.foreignTables().holdPromotions = promotions
	}
}

func (s *StatementScope) setForeignCursor(cursor *sqlCursor) {
	if cursor != nil || s.foreignTablesSlot != nil {
		s.foreignTables().cursor = cursor
	}
}

// setForeignLexical installs the scan owner and cursor saved around a SQL
// routine body.
func (s *StatementScope) setForeignLexical(owner *exec.ForeignScanOwner, cursor *sqlCursor) {
	if owner != nil || cursor != nil || s.foreignTablesSlot != nil {
		f := s.foreignTables()
		f.owner, f.cursor = owner, cursor
	}
}
