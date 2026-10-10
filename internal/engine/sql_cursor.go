package engine

import (
	"fmt"
	"slices"
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
	root                                        *projectNode
	schema                                      []ColumnDescriptor
	source                                      *querySource
	state                                       queryState
	stage                                       *indexedStage
	authority                                   *queryAuthority
	position                                    cursorPosition
	leaves                                      []queryLeaf
	locks                                       *queryLockContext
	catalog                                     CatalogRead
	roles                                       RoleRead
	deparseAuth                                 AuthorizationView
	deparsePath                                 []ObjectAddress
	visibilityCatalog                           CatalogRead
	visibilityRelationPath, visibilityOtherPath []ObjectAddress
}

func (c *sqlCursor) dispose() {
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
	c.catalog, c.roles = CatalogRead{}, RoleRead{}
	c.deparseAuth = AuthorizationView{}
	c.deparsePath, c.visibilityRelationPath, c.visibilityOtherPath = nil, nil, nil
	c.visibilityCatalog = CatalogRead{}
	if c.authority != nil {
		c.authority.scope = nil
		c.authority.expression = nil
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
	return sqlError("34000", fmt.Sprintf("cursor %q does not exist", name))
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
	c := &sqlCursor{tx: s.txRef, ordinal: r.ordinal + 1, root: project, schema: project.Schema(), source: source, authority: s.authority,
		leaves: s.querySources, locks: s.queryLocks, catalog: s.commentCatalog, roles: s.commentRoles,
		deparseAuth: s.deparseAuth, deparsePath: s.deparsePath, visibilityCatalog: s.visibilityCatalog,
		visibilityRelationPath: s.visibilityRelationPath, visibilityOtherPath: s.visibilityOtherPath}
	c.state = queryState{source: source, parameters: parameters, input: s.input, output: s.output, locking: source.locks != nil, hashFilter: hashQueryFilter(root)}
	// Presentation readers stored in query frames must not retain this admission.
	c.state.output.Regclass, c.state.output.Regtype = nil, nil
	c.deparseAuth.session, c.deparseAuth.settings = nil, nil
	if reader, ok := c.state.input.Regtype.(capturedRegtypeInput); ok {
		reader.op, reader.authority = nil, c.authority
		reader.qualified = nil
		c.state.input.Regtype = reader
	}
	if err := s.cursorPublicationPreflight(); err != nil {
		return Result{}, err
	}
	r.entries[declaration.Name.Text] = c
	r.ordinal = c.ordinal
	s.completed, s.cursorCompleted, keep = true, true, true
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
func (s *StatementScope) cursorPublicationPreflight() error {
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
	if c == nil || c.tx != s.txRef {
		return Result{}, missingSQLCursor(fetch.Name.Text)
	}
	if c.position == cursorFailed {
		return Result{}, sqlError("55000", fmt.Sprintf("portal %q cannot be run", fetch.Name.Text))
	}
	if fetch.Backward || fetch.Count < 0 || (!fetch.All && fetch.Count == 0 && c.position == cursorOn) {
		c.position = cursorFailed
		c.dispose()
		return Result{}, forwardSQLCursorError()
	}
	c.admit(s)
	defer func() {
		c.authority.scope = nil
		if err != nil {
			c.position = cursorFailed
			c.dispose()
		}
	}()
	e := s.op.session.Engine()
	e.mu.Lock()
	auth, authErr := e.authorizationLocked(s.op)
	if authErr == nil {
		authErr = s.validateLockedRelation(e)
	}
	if authErr == nil {
		authErr = auth.checkQuerySources(c.leaves)
	}
	if authErr == nil {
		authErr = s.captureIdentityLocked(auth)
	}
	e.mu.Unlock()
	if authErr != nil {
		return Result{}, authErr
	}
	rows := newFinalRows(c.schema, 0)
	rows.output = c.state.output
	if !fetch.All && fetch.Count == 0 || c.position == cursorAfter {
		if err := s.cursorPublicationPreflight(); err != nil {
			return Result{}, err
		}
		s.completed, s.cursorCompleted = true, true
		return Result{Rows: rows}, nil
	}
	if c.stage == nil {
		c.authority.expression = &cursorExpressionData{identity: s.identity, statementStart: s.statementStart}
		stage, openErr := openQueryProjectStage(c.authority, &c.state, c.root)
		if openErr != nil {
			return Result{}, openErr
		}
		c.stage = &stage
	}
	width := len(c.schema)
	// Project directly into geometrically growing detached flat storage. Distinct
	// zero-width occurrences still count; no extra projected tuple is probed.
	for fetch.All || int64(rows.count) < fetch.Count {
		max := int(^uint(0) >> 1)
		if rows.count == max || len(rows.values) > max-width {
			return Result{}, fmt.Errorf("stuntgres: cursor batch cardinality exhausted")
		}
		start := len(rows.values)
		rows.values = slices.Grow(rows.values, width)
		rows.values = rows.values[:start+width]
		present, pullErr := c.stage.Next(c.authority, rows.values[start:])
		if pullErr != nil {
			return Result{}, pullErr
		}
		if !present {
			rows.values = rows.values[:start]
			c.position = cursorAfter
			break
		}
		rows.count++
		c.position = cursorOn
	}
	// Finalized OID rendering is detached as well. Catalog data remains DECLARE's;
	// the current search path controls the normal public rendering boundary.
	e.mu.Lock()
	auth, authErr = e.authorizationLocked(s.op)
	var path []ObjectAddress
	if authErr == nil {
		path, authErr = e.referencePath(auth)
	}
	e.mu.Unlock()
	if authErr != nil {
		return Result{}, authErr
	}
	for _, value := range rows.values {
		if err := captureRegclassOutputValue(rows, c.catalog, path, value); err != nil {
			return Result{}, err
		}
	}
	if err := s.cursorPublicationPreflight(); err != nil {
		return Result{}, err
	}
	s.completed, s.cursorCompleted = true, true
	return Result{Count: int64(rows.count), Rows: rows}, nil
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
	if err := s.cursorPublicationPreflight(); err != nil {
		return Result{}, err
	}
	if close.All {
		r.clear()
	} else {
		c.dispose()
		delete(r.entries, close.Name.Text)
	}
	s.completed, s.cursorCompleted = true, true
	return Result{}, nil
}
