package engine

import (
	"context"
	"fmt"
	"sort"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// Admission, not a second mutex hierarchy, protects the one session registry.
type cursorRegistry struct {
	entries map[string]*sqlCursor
	ordinal uint64
	token   ids.SessionToken
}
type cursorPosition uint8

const (
	cursorBefore cursorPosition = iota
	cursorOn
	cursorAfter
	cursorFailed
)

type sqlCursor struct {
	tx                                          ids.TxRef
	ordinal                                     uint64
	epoch                                       ids.CatalogEpoch
	hold, promoted                              bool
	statement                                   string
	created                                     sqltype.Value
	values                                      []sqltype.Value
	count                                       int
	storeOffset                                 int64
	root                                        *plan.ProjectNode
	schema                                      []catalog.Column
	source                                      *querySource
	state                                       queryState
	stage                                       *indexedStage
	authority                                   *queryAuthority
	foreignOwner                                *foreignScanOwner
	position                                    cursorPosition
	scroll                                      bool
	ordinalPosition                             int64
	atEnd                                       bool
	cleanupError                                error
	currentSource                               *querySource
	currentIndex                                int
	leaves                                      []queryLeaf
	locks                                       *queryLockContext
	catalog                                     catalog.Read
	roles                                       authorization.Read
	routineDependencies                         []catalog.ExpressionDependency // Original immutable binding lifetimes, not permissions.
	routineAuth                                 AuthorizationView              // DECLARE's initialized executor, not cached permission results.
	deparseAuth                                 AuthorizationView
	deparsePath                                 []ids.ObjectAddress
	collationPath                               []ids.ObjectAddress
	visibilityCatalog                           catalog.Read
	visibilityRelationPath, visibilityOtherPath []ids.ObjectAddress
}

// All cursor cleanup is detached from registries before application callbacks.
// cleanupError makes repeated disposal idempotent without hiding End failures.

// Producer teardown is distinct from disposing an exported result. Typed values
// and schema own their codecs; no declaration catalog or execution survives.
func (c *sqlCursor) releaseProducer(ctx context.Context) error {
	c.currentSource, c.currentIndex = nil, 0
	var failure error
	if c.stage != nil {
		failure = c.stage.Close()
		c.stage = nil
	}
	if c.foreignOwner != nil {
		if c.authority != nil && c.authority.Host != nil {
			c.authority.Host.ReleaseReplacementRead()
		}
		if err := c.foreignOwner.Finish(ctx); failure == nil {
			failure = err
		}
		c.foreignOwner = nil
	}
	if c.source != nil {
		c.source.Close()
		c.source = nil
	}
	c.state.ReleaseMemberships()
	c.state = queryState{}
	c.root = nil
	c.leaves, c.locks = nil, nil
	c.catalog = catalog.Read{}
	c.roles = authorization.Read{}
	c.deparseAuth = AuthorizationView{}
	c.deparsePath, c.visibilityRelationPath, c.visibilityOtherPath = nil, nil, nil
	c.collationPath = nil
	c.visibilityCatalog = catalog.Read{}
	c.routineDependencies = nil
	c.routineAuth = AuthorizationView{}
	if c.authority != nil {
		c.authority.Host = nil
		c.authority.Expression = nil
	}
	if c.cleanupError == nil {
		c.cleanupError = failure
	}
	return c.cleanupError
}
func (c *sqlCursor) dispose(ctx context.Context) error {
	err := c.releaseProducer(ctx)
	c.values = nil
	c.count, c.storeOffset = 0, 0
	return err
}

// Transaction end removes only its still-live producers. An exported portal's
// promotion is irreversible even if a later portal or write publication fails.
func (r *cursorRegistry) detach(match func(*sqlCursor) bool) []*sqlCursor {
	if r == nil {
		return nil
	}
	var detached []*sqlCursor
	for name, c := range r.entries {
		if match(c) {
			detached = append(detached, c)
			delete(r.entries, name)
		}
	}
	if len(detached) > 1 {
		sort.Slice(detached, func(i, j int) bool { return detached[i].ordinal < detached[j].ordinal })
	}
	return detached
}
func (r *cursorRegistry) endTransaction(ref ids.TxRef) []*sqlCursor {
	return r.detach(func(c *sqlCursor) bool { return !c.promoted && c.tx == ref })
}
func (r *cursorRegistry) clear() []*sqlCursor {
	return r.detach(func(*sqlCursor) bool { return true })
}
func (r *cursorRegistry) rollback(mark uint64) []*sqlCursor {
	return r.detach(func(c *sqlCursor) bool { return c.ordinal > mark })
}
func disposeSQLCursors(ctx context.Context, cursors []*sqlCursor) error {
	var failure error
	for _, cursor := range cursors {
		alreadyFailed := cursor.position == cursorFailed
		if err := cursor.dispose(ctx); failure == nil && !alreadyFailed {
			failure = err
		}
	}
	return failure
}
func (r *cursorRegistry) EndSession(token ids.SessionToken, _ EndReason) error {
	if token != r.token {
		return fmt.Errorf("stuntgres: foreign cursor attachment")
	}
	return disposeSQLCursors(context.Background(), r.clear())
}
func missingSQLCursor(name string) error {
	return sqlError("34000", fmt.Sprintf("cursor \"%s\" does not exist", name))
}
func forwardSQLCursorError() error {
	err := sqlError("55000", "cursor can only scan forward")
	err.(*sqlstate.Error).Hint = "Declare it with SCROLL option to enable backward scan."
	return err
}

func (s *StatementScope) installSQLCursor(declaration *parser.DeclareCursor, root plan.Node, source *querySource, parameters []sqltype.Value) (Result, error) {
	keep := false
	defer func() {
		s.routineAuth = AuthorizationView{}
		s.routineDependencies = nil
		s.authority.Host = nil
		if !keep {
			source.Close()
		}
	}()
	project, ok := root.(*plan.ProjectNode)
	if !ok {
		return Result{}, fmt.Errorf("stuntgres: cursor query lacks final projection")
	}
	r := s.op.session.cursors
	if r.ordinal == ^uint64(0) {
		return Result{}, fmt.Errorf("stuntgres: cursor declaration counter exhausted")
	}
	timestampType, ok := s.inquiry.commentCatalog.LookupType(sqltype.Timestamptz)
	if !ok {
		return Result{}, fmt.Errorf("stuntgres: missing cursor creation timestamp type")
	}
	created, err := sqltype.TimestampFromTime(timestampType, s.statementStart)
	if err != nil {
		return Result{}, err
	}
	c := &sqlCursor{tx: s.txRef, epoch: s.txRef.Epoch(), hold: declaration.Hold, ordinal: r.ordinal + 1, root: project, schema: project.Schema(), source: source, authority: s.authority,
		leaves: s.querySources, locks: s.queryLocks, catalog: s.inquiry.commentCatalog, roles: s.inquiry.commentRoles,
		routineDependencies: s.routineDependencies, routineAuth: s.routineAuth,
		deparseAuth: s.inquiry.deparseAuth, deparsePath: s.inquiry.deparsePath, collationPath: s.inquiry.collationPath, visibilityCatalog: s.inquiry.visibilityCatalog,
		visibilityRelationPath: s.inquiry.visibilityRelationPath, visibilityOtherPath: s.inquiry.visibilityOtherPath}
	c.statement, c.created = declaration.Source, created
	c.scroll = declaration.Scroll == parser.CursorScrollEnabled
	c.state = queryState{Source: source, Parameters: parameters, Input: s.input, Output: s.output, Locking: source.Locks.RowMarked(), HashFilter: exec.HashQueryFilter(root)}
	c.state.Input.LockedDomainInput, c.state.Input.DomainChecks = nil, nil
	// Presentation readers stored in query frames must not retain this admission.
	c.state.Output.Regclass, c.state.Output.Regtype = nil, nil
	c.state.Output.Regproc = nil
	c.state.Output.Regrole = nil
	c.state.Output.Operator = nil
	c.state.Output.Regcollation, c.state.Output.Regnamespace = nil, nil
	c.state.Output.RegTS = nil
	c.deparseAuth.session, c.deparseAuth.settings = nil, nil
	if reader, ok := c.state.Input.Regtype.(capturedRegtypeInput); ok {
		reader.op, reader.authority = nil, c.authority
		reader.qualified = nil
		c.state.Input.Regtype = reader
	}
	if reader, ok := c.state.Input.Operator.(capturedOperatorInput); ok {
		reader.types.op, reader.types.authority = nil, c.authority
		reader.types.qualified, reader.qualified = nil, nil
		c.state.Input.Operator = reader
	}
	if reader, ok := c.state.Input.Regproc.(capturedRoutineInput); ok {
		reader.types.op, reader.types.authority = nil, c.authority
		reader.types.qualified = nil
		c.state.Input.Regproc = reader
	}
	if reader, ok := c.state.Input.RegTS.(capturedRegTSInput); ok {
		reader.qualified, reader.resolved = nil, nil
		c.state.Input.RegTS = reader
	}
	if err := s.sessionPublicationPreflight(); err != nil {
		return Result{}, err
	}
	c.foreignOwner = s.foreignTablesSlot.takeOwner()
	r.entries[declaration.Name.Text] = c
	r.ordinal = c.ordinal
	s.completed, s.sessionCompleted, keep = !(declaration.Hold && s.implicit), true, true
	return Result{}, nil
}

// The operation, ticket, owner, command and write roots belong to the freshly
// admitted FETCH; the initialized producer retains DECLARE's routine authority.
func (c *sqlCursor) admit(s *StatementScope) {
	s.querySources, s.queryLocks = c.leaves, c.locks
	s.inquiry.commentCatalog, s.inquiry.commentRoles = c.catalog, c.roles
	s.routineDependencies = c.routineDependencies
	s.routineAuth = c.routineAuth
	s.inquiry.deparseAuth, s.inquiry.deparsePath = c.deparseAuth, c.deparsePath
	s.inquiry.collationPath = c.collationPath
	s.inquiry.deparseAuth.session, s.inquiry.deparseAuth.settings = s.op.session, s.op.session.settings
	s.inquiry.visibilityCatalog = c.visibilityCatalog
	s.inquiry.visibilityRelationPath, s.inquiry.visibilityOtherPath = c.visibilityRelationPath, c.visibilityOtherPath
	c.authority.Host = hostOf(s)
	s.foreignTables().cursor = c
}
func (s *StatementScope) sessionPublicationPreflight() error {
	e := s.op.session.Engine()
	s.owner.mu.Lock()
	defer s.owner.mu.Unlock()
	e.mu.Lock()
	defer e.mu.Unlock()
	s.op.session.mu.Lock()
	defer s.op.session.mu.Unlock()
	if err := publicationOperationLocked(e, s.op); err != nil {
		return err
	}
	if err := s.owner.validate(s.txRef); err != nil {
		return err
	}
	if s.txRef.Epoch() != e.catalog.state.Epoch() {
		return ids.ErrCatalogReplaced
	}
	if err := s.validateLockedRelation(e); err != nil {
		return err
	}
	if s.queryLocks != nil {
		catalog := e.privateCatalogLocked(s.owner.catalog)
		for _, entry := range s.queryLocks.Leaves {
			if _, err := currentQuerySourceLocked(e, catalog, s.owner.catalog, entry.Leaf); err != nil {
				return err
			}
		}
	}
	return nil
}
func (s *StatementScope) executeFetchCursor(fetch *parser.FetchCursor, count int) (result Result, err error) {
	if count != 0 {
		return Result{}, fmt.Errorf("stuntgres: got %d arguments, want 0", count)
	}
	c := s.op.session.cursors.entries[fetch.Name.Text]
	if c == nil || (!c.promoted && c.tx != s.txRef) {
		return Result{}, missingSQLCursor(fetch.Name.Text)
	}
	if c.epoch != s.txRef.Epoch() {
		return Result{}, ids.ErrCatalogReplaced
	}
	if c.position == cursorFailed {
		return Result{}, sqlError("55000", fmt.Sprintf("portal %q cannot be run", fetch.Name.Text))
	}
	if c.promoted {
		return s.fetchHeldCursor(c, fetch)
	}
	// Live movement restrictions use the effective direction in the shared owner.
	c.admit(s)
	defer func() {
		s.routineAuth = AuthorizationView{}
		s.routineDependencies = nil
		if err != nil {
			c.position = cursorFailed
			_ = c.dispose(s.op.Context())
		}
		c.authority.Host = nil
	}()
	if err := s.authorizeSQLCursor(c); err != nil {
		return Result{}, err
	}
	var rows *finalRows
	if !fetch.Move {
		rows = exec.NewFinalRows(c.schema, 0)
		rows.Output = c.state.Output
	}
	moved, motionErr := c.motion(fetch, rows, s)
	if motionErr != nil {
		return Result{}, motionErr
	}
	// HOLD commit continues from the same live producer's next occurrence.
	if c.stage != nil {
		c.stage.Position = int(c.ordinalPosition)
	}
	if rows != nil {
		if err := s.captureSQLCursorOutput(rows, c.catalog); err != nil {
			return Result{}, err
		}
	}
	if err := s.sessionPublicationPreflight(); err != nil {
		return Result{}, err
	}
	s.completed, s.sessionCompleted = true, true
	result.Count = moved
	if rows != nil {
		result.Rows = rows
	}
	return result, nil
}
func (s *StatementScope) executeCloseCursor(close *parser.CloseCursor, count int) (Result, error) {
	if count != 0 {
		return Result{}, fmt.Errorf("stuntgres: got %d arguments, want 0", count)
	}
	r := s.op.session.cursors
	c := r.entries[close.Name.Text]
	if !close.All && c == nil && !protocolPortalExists(s.op.session, close.Name.Text) {
		return Result{}, missingSQLCursor(close.Name.Text)
	}
	if err := s.sessionPublicationPreflight(); err != nil {
		return Result{}, err
	}
	if s.replacementRead {
		s.op.session.Engine().replacementMu.RUnlock()
		s.replacementRead = false
	}
	var cleanupErr error
	if close.All {
		cleanupErr = disposeSQLCursors(s.op.Context(), r.clear())
		for name := range s.op.session.preparedSQL.portals {
			if err := s.CloseProtocolPortal(name); cleanupErr == nil {
				cleanupErr = err
			}
		}
	} else {
		delete(r.entries, close.Name.Text)
		if c != nil {
			cleanupErr = c.dispose(s.op.Context())
		} else {
			cleanupErr = s.CloseProtocolPortal(close.Name.Text)
		}
	}
	if cleanupErr != nil {
		return Result{}, cleanupErr
	}
	s.completed, s.sessionCompleted = true, true
	return Result{}, nil
}
