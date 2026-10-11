package engine

import (
	"fmt"
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
	values                                      []sqltype.Value
	count                                       int
	storeOffset                                 int64
	root                                        *projectNode
	schema                                      []ColumnDescriptor
	source                                      *querySource
	state                                       queryState
	stage                                       *indexedStage
	authority                                   *queryAuthority
	position                                    cursorPosition
	scroll                                      bool
	ordinalPosition                             int64
	atEnd                                       bool
	currentSource                               *querySource
	currentIndex                                int
	leaves                                      []queryLeaf
	locks                                       *queryLockContext
	catalog                                     CatalogRead
	roles                                       RoleRead
	deparseAuth                                 AuthorizationView
	deparsePath                                 []ObjectAddress
	visibilityCatalog                           CatalogRead
	visibilityRelationPath, visibilityOtherPath []ObjectAddress
}

// Producer teardown is distinct from disposing an exported result. Typed values
// and schema own their codecs; no declaration catalog or execution survives.
func (c *sqlCursor) releaseProducer() {
	c.currentSource, c.currentIndex = nil, 0
	if c.stage != nil {
		_ = c.stage.Close()
		c.stage = nil
	}
	if c.source != nil {
		c.source.close()
		c.source = nil
	}
	c.state = queryState{}
	c.root = nil
	c.leaves, c.locks = nil, nil
	c.catalog = CatalogRead{}
	c.roles = RoleRead{}
	c.deparseAuth = AuthorizationView{}
	c.deparsePath, c.visibilityRelationPath, c.visibilityOtherPath = nil, nil, nil
	c.visibilityCatalog = CatalogRead{}
	if c.authority != nil {
		c.authority.scope = nil
		c.authority.expression = nil
	}
}
func (c *sqlCursor) dispose() {
	c.releaseProducer()
	c.values = nil
	c.count, c.storeOffset = 0, 0
}

// Transaction end removes only its still-live producers. An exported portal's
// promotion is irreversible even if a later portal or write publication fails.
func (r *cursorRegistry) endTransaction(ref TxRef) {
	if r == nil {
		return
	}
	for name, c := range r.entries {
		if !c.promoted && c.tx == ref {
			c.dispose()
			delete(r.entries, name)
		}
	}
}
func (r *cursorRegistry) clear() {
	if r == nil {
		return
	}
	for name, c := range r.entries {
		c.dispose()
		delete(r.entries, name)
	}
}
func (r *cursorRegistry) rollback(mark uint64) {
	if r == nil {
		return
	}
	for name, c := range r.entries {
		if c.ordinal > mark {
			c.dispose()
			delete(r.entries, name)
		}
	}
}
func (r *cursorRegistry) EndSession(token SessionToken, _ EndReason) error {
	if token != r.token {
		return fmt.Errorf("stuntgres: foreign cursor attachment")
	}
	r.clear()
	return nil
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
	c := &sqlCursor{tx: s.txRef, epoch: s.txRef.epoch, hold: declaration.Hold, ordinal: r.ordinal + 1, root: project, schema: project.Schema(), source: source, authority: s.authority,
		leaves: s.querySources, locks: s.queryLocks, catalog: s.commentCatalog, roles: s.commentRoles,
		deparseAuth: s.deparseAuth, deparsePath: s.deparsePath, visibilityCatalog: s.visibilityCatalog,
		visibilityRelationPath: s.visibilityRelationPath, visibilityOtherPath: s.visibilityOtherPath}
	c.scroll = declaration.Scroll == parser.CursorScrollEnabled
	c.state = queryState{source: source, parameters: parameters, input: s.input, output: s.output, locking: source.locks.rowMarked(), hashFilter: hashQueryFilter(root)}
	// Presentation readers stored in query frames must not retain this admission.
	c.state.output.Regclass, c.state.output.Regtype = nil, nil
	c.deparseAuth.session, c.deparseAuth.settings = nil, nil
	if reader, ok := c.state.input.Regtype.(capturedRegtypeInput); ok {
		reader.op, reader.authority = nil, c.authority
		reader.qualified = nil
		c.state.input.Regtype = reader
	}
	if err := s.sessionPublicationPreflight(); err != nil {
		return Result{}, err
	}
	r.entries[declaration.Name.Text] = c
	r.ordinal = c.ordinal
	s.completed, s.sessionCompleted, keep = !(declaration.Hold && s.implicit), true, true
	return Result{}, nil
}

// Only data is restored. The operation, ticket, owner, command and write roots
// always belong to the freshly admitted FETCH.
func (c *sqlCursor) admit(s *StatementScope) {
	s.querySources, s.queryLocks = c.leaves, c.locks
	s.commentCatalog, s.commentRoles = c.catalog, c.roles
	s.deparseAuth, s.deparsePath = c.deparseAuth, c.deparsePath
	s.deparseAuth.session, s.deparseAuth.settings = s.op.session, s.op.session.settings
	s.visibilityCatalog = c.visibilityCatalog
	s.visibilityRelationPath, s.visibilityOtherPath = c.visibilityRelationPath, c.visibilityOtherPath
	c.authority.scope = s
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
		c.authority.scope = nil
		if err != nil {
			c.position = cursorFailed
			c.dispose()
		}
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
	if close.All {
		r.clear()
	} else {
		c.dispose()
		delete(r.entries, close.Name.Text)
	}
	s.completed, s.sessionCompleted = true, true
	return Result{}, nil
}
