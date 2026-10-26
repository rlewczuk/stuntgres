package core

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
	Entries map[string]*sqlCursor
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
	Source                                      *exec.Source
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
	if c.Source != nil {
		c.Source.Close()
		c.Source = nil
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
	for name, c := range r.Entries {
		if match(c) {
			detached = append(detached, c)
			delete(r.Entries, name)
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
	return SQLError("34000", fmt.Sprintf("cursor \"%s\" does not exist", name))
}

func (s *StatementScope) installSQLCursor(declaration *parser.DeclareCursor, root plan.Node, source *exec.Source, parameters []sqltype.Value) (Result, error) {
	_, _, err := s.installCursor(declaration, &declaration.Name.Text, root, source, parameters, declaration.Scroll == parser.CursorScrollEnabled)
	if err == nil {
		s.Completed, s.sessionCompleted = !(declaration.Hold && s.implicit), true
	}
	return Result{}, err
}

// installCursor transfers captured producer ownership without completing the
// containing command. SQL DECLARE and PL OPEN have separate policy wrappers.
func (s *StatementScope) installCursor(declaration *parser.DeclareCursor, requestedName *string, root plan.Node, source *exec.Source, parameters []sqltype.Value, scroll bool) (*sqlCursor, string, error) {
	keep := false
	defer func() {
		s.RoutineAuth = AuthorizationView{}
		s.RoutineDependencies = nil
		s.authority.Host = nil
		if !keep {
			source.Close()
		}
	}()
	project, ok := root.(*plan.ProjectNode)
	if !ok {
		return nil, "", fmt.Errorf("stuntgres: cursor query lacks final projection")
	}
	r := PayloadOf(s.Op.Session).Cursors
	timestampType, ok := s.Inquiry.CommentCatalog.LookupType(sqltype.Timestamptz)
	if !ok {
		return nil, "", fmt.Errorf("stuntgres: missing cursor creation timestamp type")
	}
	created, err := sqltype.TimestampFromTime(timestampType, s.statementStart)
	if err != nil {
		return nil, "", err
	}
	c := &sqlCursor{tx: s.TxRef, epoch: s.TxRef.Epoch(), hold: declaration.Hold, root: project, schema: project.Schema(), Source: source, authority: s.authority,
		leaves: s.QuerySources, locks: s.QueryLockContext, catalog: s.Inquiry.CommentCatalog, roles: s.Inquiry.commentRoles,
		routineDependencies: s.RoutineDependencies, routineAuth: s.RoutineAuth,
		deparseAuth: s.Inquiry.deparseAuth, deparsePath: s.Inquiry.DeparsePath, collationPath: s.Inquiry.collationPath, visibilityCatalog: s.Inquiry.visibilityCatalog,
		visibilityRelationPath: s.Inquiry.visibilityRelationPath, visibilityOtherPath: s.Inquiry.visibilityOtherPath}
	c.currentSnapshot, c.snapshotFrontier = s.currentSnapshot, s.snapshotFrontier
	c.statement, c.created = declaration.Source, created
	c.scroll = scroll
	c.state = exec.State{Source: source, SourceMode: exec.SourceReusable, Parameters: parameters, Input: s.Input, Output: s.Output, Locking: source.Locks.RowMarked(), HashFilter: exec.HashQueryFilter(root)}
	c.state.CaptureAsyncAdmission()
	c.state.Input.LockedDomainInput, c.state.Input.DomainChecks = nil, nil
	// Presentation readers stored in query frames must not retain this admission.
	c.state.Output.Regclass, c.state.Output.Regtype = nil, nil
	c.state.Output.Regproc = nil
	c.state.Output.Regrole = nil
	c.state.Output.Operator = nil
	c.state.Output.Regcollation, c.state.Output.Regnamespace = nil, nil
	c.state.Output.RegTS = nil
	c.deparseAuth.Session, c.deparseAuth.settings = nil, nil
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
		return name == "" || r.Entries[name] != nil || protocolPortalExists(s.Op.Session, name)
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
			return nil, "", SQLError("42P03", fmt.Sprintf("cursor %q already exists", name))
		}
	}
	c.ordinal = r.ordinal + 1
	c.foreignOwner = s.foreignTablesSlot.takeOwner()
	s.takeRowSecurityPrograms(c)
	r.Entries[name] = c
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

func (p sqlCursorProducer) Context() context.Context { return p.scope.Op.Context() }
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
	s.QuerySources, s.QueryLockContext = c.leaves, c.locks
	s.Inquiry.CommentCatalog, s.Inquiry.commentRoles = c.catalog, c.roles
	s.RoutineDependencies = c.routineDependencies
	s.RoutineAuth = c.routineAuth
	s.Inquiry.deparseAuth, s.Inquiry.DeparsePath = c.deparseAuth, c.deparsePath
	s.Inquiry.collationPath = c.collationPath
	s.Inquiry.deparseAuth.Session, s.Inquiry.deparseAuth.settings = s.Op.Session, s.Op.Session.Settings
	s.Inquiry.visibilityCatalog = c.visibilityCatalog
	s.Inquiry.visibilityRelationPath, s.Inquiry.visibilityOtherPath = c.visibilityRelationPath, c.visibilityOtherPath
	c.authority.Host = HostOf(s)
	s.foreignTables().cursor = c
}
func (s *StatementScope) sessionPublicationPreflight() error {
	e := s.Engine()
	s.Owner.Mu.Lock()
	defer s.Owner.Mu.Unlock()
	e.Mu.Lock()
	defer e.Mu.Unlock()
	s.Op.Session.Mu.Lock()
	defer s.Op.Session.Mu.Unlock()
	if err := PublicationOperationLocked(e, s.Op); err != nil {
		return err
	}
	if err := s.Owner.Validate(s.TxRef); err != nil {
		return err
	}
	if s.TxRef.Epoch() != e.Catalog.State.Epoch() {
		return ids.ErrCatalogReplaced
	}
	if err := s.ValidateLockedRelation(e); err != nil {
		return err
	}
	if s.QueryLockContext != nil {
		catalog := e.PrivateCatalogLocked(s.PendingCatalog())
		for _, entry := range s.QueryLockContext.Leaves {
			if _, err := CurrentQuerySourceLocked(e, catalog, s.PendingCatalog(), entry.Leaf); err != nil {
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
		s.Completed, s.sessionCompleted = true, true
	}
	return result, err
}

func (s *StatementScope) fetchCursor(fetch *parser.FetchCursor) (result Result, err error) {
	return s.fetchCursorPortal(PayloadOf(s.Op.Session).Cursors.Entries[fetch.Name.Text], fetch)
}

// Identity movement is shared by named SQL/PL commands and implicit FOR NEXT.
// A retained handle must still be this physical session's exact registered
// portal; matching names cannot redirect it to a replacement.
func (s *StatementScope) validateCursorPortal(c *sqlCursor, name string) error {
	if c == nil || PayloadOf(s.Op.Session).Cursors.Entries[name] != c || (!c.promoted && c.tx != s.TxRef) {
		return missingSQLCursor(name)
	}
	if c.epoch != s.TxRef.Epoch() {
		return ids.ErrCatalogReplaced
	}
	if c.Place == exec.CursorFailed {
		return SQLError("55000", fmt.Sprintf("portal %q cannot be run", name))
	}
	return nil
}

// Resolve without admitting sources or moving the producer. PL counts execute
// only after this validation and retain this exact identity for later motion.
func (s *StatementScope) resolveCursorPortal(name string) (*sqlCursor, error) {
	c := PayloadOf(s.Op.Session).Cursors.Entries[name]
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
		s.RoutineAuth = AuthorizationView{}
		s.RoutineDependencies = nil
		if err != nil {
			c.Place = exec.CursorFailed
			_ = c.dispose(s.Op.Context())
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
	if err := c.foreignOwner.Quiesce(s.Op.Context()); err != nil {
		return Result{}, err
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
		s.Completed, s.sessionCompleted = true, true
	}
	return result, err
}

func (s *StatementScope) closeCursor(close *parser.CloseCursor) (Result, error) {
	r := PayloadOf(s.Op.Session).Cursors
	c := r.Entries[close.Name.Text]
	if !close.All && c == nil && !protocolPortalExists(s.Op.Session, close.Name.Text) {
		return Result{}, missingSQLCursor(close.Name.Text)
	}
	if close.All {
		for name, cursor := range r.Entries {
			if cursor.pinned {
				return Result{}, SQLError("24000", fmt.Sprintf("cannot drop pinned portal %q", name))
			}
		}
	} else if c != nil && c.pinned {
		return Result{}, SQLError("24000", fmt.Sprintf("cannot drop pinned portal %q", close.Name.Text))
	}
	if err := s.sessionPublicationPreflight(); err != nil {
		return Result{}, err
	}
	if s.ReplacementRead {
		s.Engine().ReplacementMu.RUnlock()
		s.ReplacementRead = false
	}
	var cleanupErr error
	if close.All {
		cleanupErr = disposeSQLCursors(s.Op.Context(), r.clear())
		for name := range PayloadOf(s.Op.Session).preparedSQL.portals {
			if err := s.CloseProtocolPortal(name); cleanupErr == nil {
				cleanupErr = err
			}
		}
	} else {
		delete(r.Entries, close.Name.Text)
		if c != nil {
			cleanupErr = c.dispose(s.Op.Context())
		} else {
			cleanupErr = s.CloseProtocolPortal(close.Name.Text)
		}
	}
	if cleanupErr != nil {
		return Result{}, cleanupErr
	}
	return Result{}, nil
}
