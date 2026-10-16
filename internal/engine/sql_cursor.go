package engine

import (
	"context"
	"fmt"
	"sort"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// Admission, not a second mutex hierarchy, protects the one session registry.
type cursorRegistry struct {
	entries map[string]*sqlCursor
	ordinal uint64
	token   SessionToken
}
type cursorPosition uint8

const (
	cursorBefore cursorPosition = iota
	cursorOn
	cursorAfter
	cursorFailed
)

type sqlCursor struct {
	tx                                          TxRef
	ordinal                                     uint64
	epoch                                       CatalogEpoch
	hold, promoted                              bool
	statement                                   string
	created                                     sqltype.Value
	values                                      []sqltype.Value
	count                                       int
	storeOffset                                 int64
	root                                        *projectNode
	schema                                      []ColumnDescriptor
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
	catalog                                     CatalogRead
	roles                                       authorization.Read
	routineDependencies                         []expressionDependency // Original immutable binding lifetimes, not permissions.
	routineAuth                                 AuthorizationView      // DECLARE's initialized executor, not cached permission results.
	deparseAuth                                 AuthorizationView
	deparsePath                                 []ObjectAddress
	collationPath                               []ObjectAddress
	visibilityCatalog                           CatalogRead
	visibilityRelationPath, visibilityOtherPath []ObjectAddress
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
		if c.authority != nil && c.authority.scope != nil && c.authority.scope.replacementRead {
			scope := c.authority.scope
			scope.op.session.Engine().replacementMu.RUnlock()
			scope.replacementRead = false
		}
		if err := c.foreignOwner.finish(ctx); failure == nil {
			failure = err
		}
		c.foreignOwner = nil
	}
	if c.source != nil {
		c.source.close()
		c.source = nil
	}
	c.state.releaseMemberships()
	c.state = queryState{}
	c.root = nil
	c.leaves, c.locks = nil, nil
	c.catalog = CatalogRead{}
	c.roles = authorization.Read{}
	c.deparseAuth = AuthorizationView{}
	c.deparsePath, c.visibilityRelationPath, c.visibilityOtherPath = nil, nil, nil
	c.collationPath = nil
	c.visibilityCatalog = CatalogRead{}
	c.routineDependencies = nil
	c.routineAuth = AuthorizationView{}
	if c.authority != nil {
		c.authority.scope = nil
		c.authority.expression = nil
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
func (r *cursorRegistry) endTransaction(ref TxRef) []*sqlCursor {
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
func (r *cursorRegistry) EndSession(token SessionToken, _ EndReason) error {
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

func (s *StatementScope) installSQLCursor(declaration *parser.DeclareCursor, root LogicalNode, source *querySource, parameters []sqltype.Value) (Result, error) {
	keep := false
	defer func() {
		s.routineAuth = AuthorizationView{}
		s.routineDependencies = nil
		s.authority.scope = nil
		if !keep {
			source.close()
		}
	}()
	project, ok := root.(*projectNode)
	if !ok {
		return Result{}, fmt.Errorf("stuntgres: cursor query lacks final projection")
	}
	r := s.op.session.cursors
	if r.ordinal == ^uint64(0) {
		return Result{}, fmt.Errorf("stuntgres: cursor declaration counter exhausted")
	}
	timestampType, ok := s.commentCatalog.LookupType(sqltype.Timestamptz)
	if !ok {
		return Result{}, fmt.Errorf("stuntgres: missing cursor creation timestamp type")
	}
	created, err := sqltype.TimestampFromTime(timestampType, s.statementStart)
	if err != nil {
		return Result{}, err
	}
	c := &sqlCursor{tx: s.txRef, epoch: s.txRef.epoch, hold: declaration.Hold, ordinal: r.ordinal + 1, root: project, schema: project.Schema(), source: source, authority: s.authority,
		leaves: s.querySources, locks: s.queryLocks, catalog: s.commentCatalog, roles: s.commentRoles,
		routineDependencies: s.routineDependencies, routineAuth: s.routineAuth,
		deparseAuth: s.deparseAuth, deparsePath: s.deparsePath, collationPath: s.collationPath, visibilityCatalog: s.visibilityCatalog,
		visibilityRelationPath: s.visibilityRelationPath, visibilityOtherPath: s.visibilityOtherPath}
	c.statement, c.created = declaration.Source, created
	c.scroll = declaration.Scroll == parser.CursorScrollEnabled
	c.state = queryState{source: source, parameters: parameters, input: s.input, output: s.output, locking: source.locks.rowMarked(), hashFilter: hashQueryFilter(root)}
	c.state.input.LockedDomainInput, c.state.input.DomainChecks = nil, nil
	// Presentation readers stored in query frames must not retain this admission.
	c.state.output.Regclass, c.state.output.Regtype = nil, nil
	c.state.output.Regproc = nil
	c.state.output.Regrole = nil
	c.state.output.Operator = nil
	c.state.output.Regcollation, c.state.output.Regnamespace = nil, nil
	c.state.output.RegTS = nil
	c.deparseAuth.session, c.deparseAuth.settings = nil, nil
	if reader, ok := c.state.input.Regtype.(capturedRegtypeInput); ok {
		reader.op, reader.authority = nil, c.authority
		reader.qualified = nil
		c.state.input.Regtype = reader
	}
	if reader, ok := c.state.input.Operator.(capturedOperatorInput); ok {
		reader.types.op, reader.types.authority = nil, c.authority
		reader.types.qualified, reader.qualified = nil, nil
		c.state.input.Operator = reader
	}
	if reader, ok := c.state.input.Regproc.(capturedRoutineInput); ok {
		reader.types.op, reader.types.authority = nil, c.authority
		reader.types.qualified = nil
		c.state.input.Regproc = reader
	}
	if reader, ok := c.state.input.RegTS.(capturedRegTSInput); ok {
		reader.qualified, reader.resolved = nil, nil
		c.state.input.RegTS = reader
	}
	if err := s.sessionPublicationPreflight(); err != nil {
		return Result{}, err
	}
	foreignOwner := s.foreignOwner
	s.foreignOwner = nil
	c.foreignOwner = foreignOwner
	r.entries[declaration.Name.Text] = c
	r.ordinal = c.ordinal
	s.completed, s.sessionCompleted, keep = !(declaration.Hold && s.implicit), true, true
	return Result{}, nil
}

// The operation, ticket, owner, command and write roots belong to the freshly
// admitted FETCH; the initialized producer retains DECLARE's routine authority.
func (c *sqlCursor) admit(s *StatementScope) {
	s.querySources, s.queryLocks = c.leaves, c.locks
	s.commentCatalog, s.commentRoles = c.catalog, c.roles
	s.routineDependencies = c.routineDependencies
	s.routineAuth = c.routineAuth
	s.deparseAuth, s.deparsePath = c.deparseAuth, c.deparsePath
	s.collationPath = c.collationPath
	s.deparseAuth.session, s.deparseAuth.settings = s.op.session, s.op.session.settings
	s.visibilityCatalog = c.visibilityCatalog
	s.visibilityRelationPath, s.visibilityOtherPath = c.visibilityRelationPath, c.visibilityOtherPath
	c.authority.scope = s
	s.foreignCursor = c
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
	if s.txRef.epoch != e.state.epoch {
		return ErrCatalogReplaced
	}
	if err := s.validateLockedRelation(e); err != nil {
		return err
	}
	if s.queryLocks != nil {
		catalog := e.privateCatalogLocked(s.owner.catalog)
		for _, entry := range s.queryLocks.leaves {
			if _, err := currentQuerySourceLocked(e, catalog, s.owner.catalog, entry.leaf); err != nil {
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
	if c.epoch != s.txRef.epoch {
		return Result{}, ErrCatalogReplaced
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
		c.authority.scope = nil
	}()
	if err := s.authorizeSQLCursor(c); err != nil {
		return Result{}, err
	}
	var rows *finalRows
	if !fetch.Move {
		rows = newFinalRows(c.schema, 0)
		rows.output = c.state.output
	}
	moved, motionErr := c.motion(fetch, rows, s)
	if motionErr != nil {
		return Result{}, motionErr
	}
	// HOLD commit continues from the same live producer's next occurrence.
	if c.stage != nil {
		c.stage.position = int(c.ordinalPosition)
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
	if !close.All && c == nil {
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
	} else {
		delete(r.entries, close.Name.Text)
		cleanupErr = c.dispose(s.op.Context())
	}
	if cleanupErr != nil {
		return Result{}, cleanupErr
	}
	s.completed, s.sessionCompleted = true, true
	return Result{}, nil
}
