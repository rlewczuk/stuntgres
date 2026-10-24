package engine

import (
	"context"
	"fmt"
	"sort"
	"strconv"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/regobj"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// Admission, not a second mutex hierarchy, protects the one session registry.
type cursorRegistry struct {
	entries map[string]*sqlCursor
	ordinal uint64
	token   ids.SessionToken
}

type sqlCursor struct {
	exec.CursorPosition
	tx                                          ids.TxRef
	ordinal                                     uint64
	epoch                                       ids.CatalogEpoch
	hold, promoted                              bool
	pinned                                      bool // Active cursor FOR owns user-drop protection.
	statement                                   string
	created                                     sqltype.Value
	values                                      []sqltype.Value
	count                                       int
	storeOffset                                 int64
	root                                        *plan.ProjectNode
	schema                                      []catalog.Column
	recordShape                                 *sqltype.Descriptor // Lazy FETCH record descriptor, immutable after publication.
	source                                      *exec.Source
	state                                       exec.State
	stage                                       *exec.Stage
	authority                                   *exec.Authority
	currentSnapshot                             *sqltype.Snapshot
	snapshotFrontier                            ids.CommitSeq
	foreignOwner                                *exec.ForeignScanOwner
	rlsPrograms                                 []*rowSecurityProgram // Admitted clause state and nested source pins.
	scroll                                      bool
	cleanupError                                error
	leaves                                      []exec.Leaf
	locks                                       *exec.LockContext
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
	c.CurrentSource, c.CurrentIndex = nil, 0
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
	failure = c.state.FinishSources(failure)
	if c.source != nil {
		c.source.Close()
		c.source = nil
	}
	for _, program := range c.rlsPrograms {
		failure = program.FinishSources(failure)
		program.Close()
	}
	c.rlsPrograms = nil
	c.state.ReleaseMemberships()
	c.state = exec.State{}
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
	c.currentSnapshot, c.snapshotFrontier = nil, 0
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
	// Terminal/owner cleanup must retire even a protected loop portal.
	c.pinned = false
	err := c.releaseProducer(ctx)
	c.values = nil
	c.schema, c.recordShape = nil, nil
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
		alreadyFailed := cursor.Place == exec.CursorFailed
		if err := cursor.dispose(ctx); failure == nil && !alreadyFailed {
			failure = err
		}
	}
	return failure
}
func (r *cursorRegistry) EndSession(token ids.SessionToken, _ session.EndReason) error {
	if token != r.token {
		return fmt.Errorf("stuntgres: foreign cursor attachment")
	}
	return disposeSQLCursors(context.Background(), r.clear())
}
func missingSQLCursor(name string) error {
	return sqlError("34000", fmt.Sprintf("cursor \"%s\" does not exist", name))
}

func (s *StatementScope) installSQLCursor(declaration *parser.DeclareCursor, root plan.Node, source *exec.Source, parameters []sqltype.Value) (Result, error) {
	_, _, err := s.installCursor(declaration, &declaration.Name.Text, root, source, parameters, declaration.Scroll == parser.CursorScrollEnabled)
	if err == nil {
		s.completed, s.sessionCompleted = !(declaration.Hold && s.implicit), true
	}
	return Result{}, err
}

// installCursor transfers captured producer ownership without completing the
// containing command. SQL DECLARE and PL OPEN have separate policy wrappers.
func (s *StatementScope) installCursor(declaration *parser.DeclareCursor, requestedName *string, root plan.Node, source *exec.Source, parameters []sqltype.Value, scroll bool) (*sqlCursor, string, error) {
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
		return nil, "", fmt.Errorf("stuntgres: cursor query lacks final projection")
	}
	r := payloadOf(s.op.Session).cursors
	timestampType, ok := s.inquiry.commentCatalog.LookupType(sqltype.Timestamptz)
	if !ok {
		return nil, "", fmt.Errorf("stuntgres: missing cursor creation timestamp type")
	}
	created, err := sqltype.TimestampFromTime(timestampType, s.statementStart)
	if err != nil {
		return nil, "", err
	}
	c := &sqlCursor{tx: s.txRef, epoch: s.txRef.Epoch(), hold: declaration.Hold, root: project, schema: project.Schema(), source: source, authority: s.authority,
		leaves: s.querySources, locks: s.queryLocks, catalog: s.inquiry.commentCatalog, roles: s.inquiry.commentRoles,
		routineDependencies: s.routineDependencies, routineAuth: s.routineAuth,
		deparseAuth: s.inquiry.deparseAuth, deparsePath: s.inquiry.deparsePath, collationPath: s.inquiry.collationPath, visibilityCatalog: s.inquiry.visibilityCatalog,
		visibilityRelationPath: s.inquiry.visibilityRelationPath, visibilityOtherPath: s.inquiry.visibilityOtherPath}
	c.currentSnapshot, c.snapshotFrontier = s.currentSnapshot, s.snapshotFrontier
	c.statement, c.created = declaration.Source, created
	c.scroll = scroll
	c.state = exec.State{Source: source, SourceMode: exec.SourceReusable, Parameters: parameters, Input: s.input, Output: s.output, Locking: source.Locks.RowMarked(), HashFilter: exec.HashQueryFilter(root)}
	c.state.Input.LockedDomainInput, c.state.Input.DomainChecks = nil, nil
	// Presentation readers stored in query frames must not retain this admission.
	c.state.Output.Regclass, c.state.Output.Regtype = nil, nil
	c.state.Output.Regproc = nil
	c.state.Output.Regrole = nil
	c.state.Output.Operator = nil
	c.state.Output.Regcollation, c.state.Output.Regnamespace = nil, nil
	c.state.Output.RegTS = nil
	c.deparseAuth.session, c.deparseAuth.settings = nil, nil
	regobj.DetachCursorInput(&c.state.Input, c.authority)
	if err := s.sessionPublicationPreflight(); err != nil {
		return nil, "", err
	}
	// All user expression, planning and publication callbacks have completed.
	// A NULL request gets its identity here, never a reservation made by OPEN.
	if r.ordinal == ^uint64(0) {
		return nil, "", fmt.Errorf("stuntgres: cursor declaration counter exhausted")
	}
	inUse := func(name string) bool {
		return name == "" || r.entries[name] != nil || protocolPortalExists(s.op.Session, name)
	}
	name := ""
	if requestedName == nil {
		for number := r.ordinal + 1; ; number++ {
			if number == 0 {
				return nil, "", fmt.Errorf("stuntgres: cursor name counter exhausted")
			}
			name = "<unnamed cursor " + strconv.FormatUint(number, 10) + ">"
			if !inUse(name) {
				break
			}
		}
	} else {
		name = *requestedName
		if inUse(name) {
			return nil, "", sqlError("42P03", fmt.Sprintf("cursor %q already exists", name))
		}
	}
	c.ordinal = r.ordinal + 1
	c.foreignOwner = s.foreignTablesSlot.takeOwner()
	s.takeRowSecurityPrograms(c)
	r.entries[name] = c
	r.ordinal = c.ordinal
	keep = true
	return c, name, nil
}

// sqlCursorProducer is a live cursor's producer for the motion its scope
// admitted.
type sqlCursorProducer struct {
	cursor *sqlCursor
	scope  *StatementScope
}

var _ exec.CursorProducer = sqlCursorProducer{}

func (p sqlCursorProducer) Context() context.Context { return p.scope.op.Context() }
func (p sqlCursorProducer) Open() (*exec.Stage, error) {
	if err := p.cursor.openProducer(p.scope); err != nil {
		return nil, err
	}
	return p.cursor.stage, nil
}
func (p sqlCursorProducer) Exhausted() error {
	return p.scope.finishForeignOwner(p.cursor.foreignOwner, nil)
}

// motionInput is the input of one motion admitted by s. A promoted cursor
// reads its store and has no producer.
func (c *sqlCursor) motionInput(s *StatementScope) exec.CursorInput {
	in := exec.CursorInput{Stage: c.stage, Store: c.values, Count: c.count, Offset: c.storeOffset, Width: len(c.schema), Scroll: c.scroll}
	if !c.promoted {
		in.Producer = sqlCursorProducer{cursor: c, scope: s}
	}
	return in
}

// The operation, ticket, owner, command and write roots belong to the freshly
// admitted FETCH; the initialized producer retains DECLARE's routine authority.
func (c *sqlCursor) admit(s *StatementScope) {
	s.currentSnapshot, s.snapshotFrontier = c.currentSnapshot, c.snapshotFrontier
	s.querySources, s.queryLocks = c.leaves, c.locks
	s.inquiry.commentCatalog, s.inquiry.commentRoles = c.catalog, c.roles
	s.routineDependencies = c.routineDependencies
	s.routineAuth = c.routineAuth
	s.inquiry.deparseAuth, s.inquiry.deparsePath = c.deparseAuth, c.deparsePath
	s.inquiry.collationPath = c.collationPath
	s.inquiry.deparseAuth.session, s.inquiry.deparseAuth.settings = s.op.Session, s.op.Session.Settings
	s.inquiry.visibilityCatalog = c.visibilityCatalog
	s.inquiry.visibilityRelationPath, s.inquiry.visibilityOtherPath = c.visibilityRelationPath, c.visibilityOtherPath
	c.authority.Host = hostOf(s)
	s.foreignTables().cursor = c
}
func (s *StatementScope) sessionPublicationPreflight() error {
	e := s.engine()
	s.owner.Mu.Lock()
	defer s.owner.Mu.Unlock()
	e.mu.Lock()
	defer e.mu.Unlock()
	s.op.Session.Mu.Lock()
	defer s.op.Session.Mu.Unlock()
	if err := publicationOperationLocked(e, s.op); err != nil {
		return err
	}
	if err := s.owner.Validate(s.txRef); err != nil {
		return err
	}
	if s.txRef.Epoch() != e.catalog.State.Epoch() {
		return ids.ErrCatalogReplaced
	}
	if err := s.validateLockedRelation(e); err != nil {
		return err
	}
	if s.queryLocks != nil {
		catalog := e.privateCatalogLocked(s.owner.Catalog)
		for _, entry := range s.queryLocks.Leaves {
			if _, err := currentQuerySourceLocked(e, catalog, s.owner.Catalog, entry.Leaf); err != nil {
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
	result, err = s.fetchCursor(fetch)
	if err == nil {
		s.completed, s.sessionCompleted = true, true
	}
	return result, err
}

func (s *StatementScope) fetchCursor(fetch *parser.FetchCursor) (result Result, err error) {
	return s.fetchCursorPortal(payloadOf(s.op.Session).cursors.entries[fetch.Name.Text], fetch)
}

// Identity movement is shared by named SQL/PL commands and implicit FOR NEXT.
// A retained handle must still be this physical session's exact registered
// portal; matching names cannot redirect it to a replacement.
func (s *StatementScope) validateCursorPortal(c *sqlCursor, name string) error {
	if c == nil || payloadOf(s.op.Session).cursors.entries[name] != c || (!c.promoted && c.tx != s.txRef) {
		return missingSQLCursor(name)
	}
	if c.epoch != s.txRef.Epoch() {
		return ids.ErrCatalogReplaced
	}
	if c.Place == exec.CursorFailed {
		return sqlError("55000", fmt.Sprintf("portal %q cannot be run", name))
	}
	return nil
}

// Resolve without admitting sources or moving the producer. PL counts execute
// only after this validation and retain this exact identity for later motion.
func (s *StatementScope) resolveCursorPortal(name string) (*sqlCursor, error) {
	c := payloadOf(s.op.Session).cursors.entries[name]
	if err := s.validateCursorPortal(c, name); err != nil {
		return nil, err
	}
	return c, nil
}

func (s *StatementScope) fetchCursorPortal(c *sqlCursor, fetch *parser.FetchCursor) (result Result, err error) {
	if err := s.validateCursorPortal(c, fetch.Name.Text); err != nil {
		return Result{}, err
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
			c.Place = exec.CursorFailed
			_ = c.dispose(s.op.Context())
		}
		c.authority.Host = nil
	}()
	if err := s.authorizeSQLCursor(c); err != nil {
		return Result{}, err
	}
	var rows *exec.FinalRows
	if !fetch.Move {
		rows = exec.NewFinalRows(c.schema, 0)
		rows.Output = c.state.Output
	}
	moved, motionErr := c.Move(fetch, rows, c.motionInput(s))
	if motionErr != nil {
		return Result{}, motionErr
	}
	// HOLD commit continues from the same live producer's next occurrence.
	if c.stage != nil {
		c.stage.Position = int(c.OrdinalPosition)
	}
	if rows != nil {
		if err := s.captureSQLCursorOutput(rows, c.catalog); err != nil {
			return Result{}, err
		}
	}
	if err := s.sessionPublicationPreflight(); err != nil {
		return Result{}, err
	}
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
	result, err := s.closeCursor(close)
	if err == nil {
		s.completed, s.sessionCompleted = true, true
	}
	return result, err
}

func (s *StatementScope) closeCursor(close *parser.CloseCursor) (Result, error) {
	r := payloadOf(s.op.Session).cursors
	c := r.entries[close.Name.Text]
	if !close.All && c == nil && !protocolPortalExists(s.op.Session, close.Name.Text) {
		return Result{}, missingSQLCursor(close.Name.Text)
	}
	if close.All {
		for name, cursor := range r.entries {
			if cursor.pinned {
				return Result{}, sqlError("24000", fmt.Sprintf("cannot drop pinned portal %q", name))
			}
		}
	} else if c != nil && c.pinned {
		return Result{}, sqlError("24000", fmt.Sprintf("cannot drop pinned portal %q", close.Name.Text))
	}
	if err := s.sessionPublicationPreflight(); err != nil {
		return Result{}, err
	}
	if s.replacementRead {
		s.engine().replacementMu.RUnlock()
		s.replacementRead = false
	}
	var cleanupErr error
	if close.All {
		cleanupErr = disposeSQLCursors(s.op.Context(), r.clear())
		for name := range payloadOf(s.op.Session).preparedSQL.portals {
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
	return Result{}, nil
}
