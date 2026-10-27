package core

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/regobj"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

type protocolPortalState uint8

const (
	protocolPortalReady protocolPortalState = iota
	protocolPortalExhausted
	protocolPortalDone
	protocolPortalFailed
)

type protocolPortal struct {
	PreparedBinding
	resource          PreparedResource
	context           session.ProtocolContext
	columns           []catalog.Column
	formats           []int16
	values            []sqltype.Value
	producer          *sqlCursor
	rows              exec.RowResult
	started           bool
	state             protocolPortalState
	tag               string
	completionBase    string // Actual executed command, independent of outer syntax/resources.
	completionCounted bool
	completionInsert  bool
	row               []sqltype.Value
	fields            []session.ProtocolField
	scratch           []byte
	outputRows        *exec.FinalRows
}
type ProtocolExecution struct {
	CommandTag string
	Count      int64
	Suspended  bool
	Empty      bool
}

func newProtocolPortal(entry PreparedDefinition, tx ids.TxRef) *protocolPortal {
	portal := &protocolPortal{PreparedBinding: PreparedBinding{Entry: entry, Transaction: tx}}
	portal.resource.Closer = portal
	portal.Resource = &portal.resource
	return portal
}
func protocolPortalOf(binding *PreparedBinding) *protocolPortal {
	if binding == nil || binding.Resource == nil {
		return nil
	}
	portal, _ := binding.Resource.Closer.(*protocolPortal)
	return portal
}
func (p *protocolPortal) close(ctx context.Context) error { return p.PreparedBinding.Close(ctx) }

// Close implements the real accepted resource's terminal cleanup capability.
func (p *protocolPortal) Close(ctx context.Context) error { return p.releaseExecutor(ctx) }

// Executor release does not remove the portal's name, descriptor or birth.
func (p *protocolPortal) releaseExecutor(ctx context.Context) error {
	var err error
	if p.producer != nil {
		err = p.producer.dispose(ctx)
		p.producer = nil
	}
	if p.rows != nil {
		if e := p.rows.Close(); err == nil {
			err = e
		}
		p.rows = nil
	}
	p.values = nil
	p.Folds = nil
	p.row, p.fields, p.scratch, p.outputRows = nil, nil, nil, nil
	return err
}

func (s *StatementScope) BindProtocolPortal(name, statement string, input session.ProtocolInput) (out session.ProtocolContext, err error) {
	entry := PayloadOf(s.Op.Session).PreparedSQL.Lookup(statement)
	if entry == nil {
		return out, missingSQLPrepared(statement)
	}
	out.Source = entry.Source
	formats, err := session.ProtocolFormats(input)
	if err != nil {
		return out, err
	}
	count, err := input.Uint16()
	if err != nil {
		return out, err
	}
	if len(formats) > 1 && len(formats) != int(count) {
		return out, SQLError("08P01", fmt.Sprintf("bind message has %d parameter formats but %d parameters", len(formats), count))
	}
	if int(count) != entry.Description.Parameters.Count {
		return out, SQLError("08P01", fmt.Sprintf("bind message supplies %d parameters, but prepared statement \"%s\" requires %d", count, statement, entry.Description.Parameters.Count))
	}
	if err = s.Gate(entry.Body); err != nil {
		return out, err
	}
	if entry.Body == nil {
		s.Owner.Mu.Lock()
		err = s.admitTransactionLocked(s.Engine())
		s.Owner.Mu.Unlock()
		if err != nil {
			return out, err
		}
	}
	r := PayloadOf(s.Op.Session).PreparedSQL
	if old := protocolPortalOf(r.Binding(name)); old != nil && !old.Retired {
		if name != "" {
			return out, SQLError("42P03", fmt.Sprintf("cursor \"%s\" already exists", name))
		}
		if err = s.protocolSimpleIO(func() error { return old.close(s.Op.Context()) }); err != nil {
			return out, err
		}
	}
	if PayloadOf(s.Op.Session).Cursors.Entries[name] != nil {
		return out, SQLError("42P03", fmt.Sprintf("cursor \"%s\" already exists", name))
	}
	births := PayloadOf(s.Op.Session).Cursors
	if births.ordinal == ^uint64(0) {
		return out, fmt.Errorf("stuntgres: portal creation counter exhausted")
	}
	births.ordinal++
	// This description copy shares only the definition's fold owner. Bind
	// produces a private graph/frame; later definition changes cannot replace
	// that graph, and no portal description is published back by name.
	p := newProtocolPortal(*entry, s.TxRef)
	p.Birth, p.context, p.columns = births.ordinal, out, entry.Description.Results
	r.Bind(name, &p.PreparedBinding)
	defer func() {
		if err == nil {
			err = s.sessionPublicationPreflight()
		}
		if err == nil {
			s.Completed, s.sessionCompleted = true, true
		}
		out = p.context
		if err != nil {
			_ = s.protocolTerminalIO(func() error { return p.close(s.Op.Context()) })
			r.RemoveBinding(name)
		}
	}()
	importer := func(ctx context.Context, formal analyze.ParameterInfo, frame []sqltype.Value, env sqltype.InputEnvironment) error {
		if e := session.ImportProtocolParameters(s.Op.Context(), s.Output, input, formats, formal.Count, formal.Types, frame, env, &p.context, session.ProtocolBindInput); e != nil {
			var diagnostic *sqlstate.Error
			if errors.As(e, &diagnostic) {
				copy := *diagnostic
				slot := len(p.context.Parameters)
				if slot == 0 || p.context.Parameters[slot-1].Decoded {
					slot++
				}
				if copy.Code == "22P03" {
					copy.Message = fmt.Sprintf("incorrect binary data format in bind parameter %d", slot)
				}
				label := "unnamed portal"
				if name != "" {
					label = fmt.Sprintf("portal \"%s\"", name)
				}
				context := fmt.Sprintf("%s parameter $%d", label, slot)
				if slot <= len(p.context.Parameters) {
					parameter := p.context.Parameters[slot-1]
					if parameter.Format == 0 && !parameter.Null && copy.Code != "22021" {
						if len(parameter.Bytes) == 0 {
							context += " = ''"
						} else {
							context += " = '...'"
						}
					}
				}
				if copy.Context != "" {
					copy.Context += "\n"
				}
				copy.Context += context
				return &copy
			}
			return e
		}
		var e error
		p.formats, e = session.ProtocolResultFormats(input, len(p.columns))
		return e
	}
	if entry.Body == nil {
		if count == 0 {
			p.formats, err = session.ProtocolResultFormats(input, 0)
			return out, err
		}
		if count != 0 {
			formal := analyze.ParameterInfo{Count: int(count), Types: make([]sqltype.Usage, int(count))}
			e := s.Engine()
			e.Mu.Lock()
			auth, lookupErr := e.AuthorizationLocked(s.Op)
			if lookupErr == nil {
				for i, oid := range entry.ParameterOIDs {
					typ, ok := auth.Catalog.LookupType(sqltype.ID(oid))
					if !ok {
						lookupErr = SQLError("42704", fmt.Sprintf("type with OID %d does not exist", oid))
						break
					}
					formal.Types[i] = typ.Usage()
				}
			}
			e.Mu.Unlock()
			if lookupErr != nil {
				return out, lookupErr
			}
			if err = s.prepareProtocolUtilityInput(formal.Types); err != nil {
				return out, err
			}
			p.values = make([]sqltype.Value, int(count))
			err = importer(s.Op.Context(), formal, p.values, s.Input)
		}
		return out, err
	}
	switch entry.Body.(type) {
	case *parser.Call, *parser.Select, *parser.Insert, *parser.Update, *parser.Delete, *parser.Merge:
		s.Expected = &p.Entry.Description
		s.protocol().bind = p
		_, err = s.Execute(entry.Body, int(count), importer)
		s.setProtocolBind(nil)
	default:
		if err = s.prepareProtocolUtilityInput(entry.Description.Parameters.Types); err != nil {
			return out, err
		}
		p.values = make([]sqltype.Value, int(count))
		err = importer(s.Op.Context(), entry.Description.Parameters, p.values, s.Input)
	}
	p.Transaction = s.TxRef
	return out, err
}
func (s *StatementScope) DescribeProtocolPortal(name string) (ProtocolPortalDescription, error) {
	if err := s.Active(); err != nil {
		return ProtocolPortalDescription{}, err
	}
	p := protocolPortalOf(PayloadOf(s.Op.Session).PreparedSQL.Binding(name))
	if p == nil || p.Retired {
		return ProtocolPortalDescription{}, missingProtocolPortal(name)
	}
	if session.ProtocolHasRows(p.Entry.Body, p.columns) && s.Op.Session.TransactionState() == session.TxAborted {
		return ProtocolPortalDescription{}, session.AbortedError()
	}
	return ProtocolPortalDescription{Columns: p.columns, Formats: p.formats, Context: p.context, Empty: p.Entry.Body == nil, HasRows: session.ProtocolHasRows(p.Entry.Body, p.columns)}, nil
}
func (s *StatementScope) CloseProtocolPortal(name string) error {
	r := PayloadOf(s.Op.Session).PreparedSQL
	if p := protocolPortalOf(r.Binding(name)); p != nil {
		return s.protocolSimpleIO(func() error {
			r.RemoveBinding(name)
			return p.close(s.Op.Context())
		})
	}
	if c := PayloadOf(s.Op.Session).Cursors.Entries[name]; c != nil {
		return s.protocolSimpleIO(func() error {
			delete(PayloadOf(s.Op.Session).Cursors.Entries, name)
			return c.dispose(s.Op.Context())
		})
	}
	return nil
}

// Savepoint publication only removes visibility; callbacks and producer
// disposal belong to the existing unlocked terminal cleanup path.
func retireProtocolPortalsAfter(t *session.TransactionOwner, frontier uint64) {
	if PayloadOf(t.Session).PreparedSQL == nil {
		return
	}
	for _, p := range PayloadOf(t.Session).PreparedSQL.Bindings() {
		if p.Transaction == t.Ref && p.Birth > frontier {
			p.Retired = true
		}
	}
}

func (s *StatementScope) installProtocolProducer(root plan.Node, source *exec.Source, values []sqltype.Value) (Result, error) {
	project, ok := root.(*plan.ProjectNode)
	if !ok {
		source.Close()
		return Result{}, fmt.Errorf("stuntgres: portal lacks projection")
	}
	bind := s.protocolOrZero().bind
	if err := bind.AcceptFolds(s.FoldCaptures); err != nil {
		source.Close()
		return Result{}, err
	}
	c := &sqlCursor{tx: s.TxRef, epoch: s.TxRef.Epoch(), PreparedResource: PreparedResource{Root: project, Closer: bind}, schema: project.Schema(), Source: source, authority: s.authority,
		leaves: s.QuerySources, locks: s.QueryLockContext, catalog: s.Inquiry.CommentCatalog, roles: s.Inquiry.commentRoles, routineDependencies: s.RoutineDependencies, routineAuth: s.RoutineAuth,
		deparseAuth: s.Inquiry.deparseAuth, deparsePath: s.Inquiry.DeparsePath, collationPath: s.Inquiry.collationPath, visibilityCatalog: s.Inquiry.visibilityCatalog, visibilityRelationPath: s.Inquiry.visibilityRelationPath, visibilityOtherPath: s.Inquiry.visibilityOtherPath}
	c.currentSnapshot, c.snapshotFrontier = s.currentSnapshot, s.snapshotFrontier
	c.state = exec.State{Source: source, SourceMode: exec.SourceReusable, Parameters: values, Input: s.Input, Output: s.Output, Locking: source.Locks != nil && source.Locks.RowMarked(), HashFilter: exec.HashQueryFilter(root)}
	c.state.Input.LockedDomainInput, c.state.Input.DomainChecks = nil, nil
	c.state.Output.Regclass, c.state.Output.Regtype = nil, nil
	c.state.Output.Regproc, c.state.Output.Regrole, c.state.Output.Operator = nil, nil, nil
	c.state.Output.Regcollation, c.state.Output.Regnamespace, c.state.Output.RegTS = nil, nil, nil
	c.deparseAuth.Session, c.deparseAuth.settings = nil, nil
	regobj.DetachCursorInput(&c.state.Input, c.authority)
	c.foreignOwner = s.foreignTablesSlot.takeOwner()
	s.takeRowSecurityPrograms(c)
	bind.Resource = &c.PreparedResource
	bind.producer = c
	bind.completionBase, bind.completionCounted = "SELECT", true
	s.authority.Host = nil
	s.RoutineAuth = AuthorizationView{}
	s.RoutineDependencies = nil
	return Result{}, nil
}

func (s *StatementScope) ExecuteProtocolPortal(name string, maxRows uint32, sink func([]session.ProtocolField) error, source io.Reader, destination io.Writer, hooks CopyProtocolHooks) (out ProtocolExecution, err error) {
	p := protocolPortalOf(PayloadOf(s.Op.Session).PreparedSQL.Binding(name))
	if p == nil || p.Retired {
		return out, missingProtocolPortal(name)
	}
	return s.executeProtocolPortal(p, name, maxRows, sink, source, destination, hooks, true)
}

func (s *StatementScope) executeProtocolPortal(p *protocolPortal, name string, maxRows uint32, sink func([]session.ProtocolField) error, source io.Reader, destination io.Writer, hooks CopyProtocolHooks, extended bool) (out ProtocolExecution, err error) {
	if p.Entry.Body == nil {
		out.Empty = true
		return out, nil
	}
	if err = s.Gate(p.Entry.Body); err != nil {
		return out, err
	}
	if p.Transaction != s.TxRef {
		return out, missingProtocolPortal(name)
	}
	if cycle := s.Owner.ProtocolCycle; extended && cycle != nil {
		cycle.Executed = true
		defer func() {
			if err == nil {
				s.Owner.Mu.Lock()
				cycle.Executed = s.Owner.State != session.TxIdle
				s.Owner.Mu.Unlock()
			}
		}()
	}
	if p.state == protocolPortalDone || p.state == protocolPortalFailed {
		return out, SQLError("55000", fmt.Sprintf("portal \"%s\" cannot be run", name))
	}
	defer func() {
		if err != nil {
			p.state = protocolPortalFailed
			_ = s.protocolTerminalIO(func() error { return p.releaseExecutor(s.Op.Context()) })
		}
	}()
	if p.state == protocolPortalExhausted {
		out.CommandTag = p.completionTag(0)
		return out, nil
	}
	if !p.started {
		p.started = true
		if p.producer == nil {
			s.Expected = &p.Entry.Description
			s.protocol().execute = p
			defer func() { s.protocol().execute = nil }()
			previousPortal := s.plExecutingPortal
			s.plExecutingPortal = p
			defer func() { s.plExecutingPortal = previousPortal }()
			stream := protocolCopyExecution{source: source, destination: destination, hooks: hooks}
			result, e := s.execute(p.Entry.Body, len(p.values), func(_ context.Context, _ analyze.ParameterInfo, frame []sqltype.Value, _ sqltype.InputEnvironment) error {
				copy(frame, p.values)
				return nil
			}, &stream)
			if e != nil {
				return out, e
			}
			p.rows, p.tag = result.Rows, result.CommandTag
			if p.rows == nil {
				p.state = protocolPortalDone
				out.CommandTag = p.tag
				out.Count = result.Count
				return out, nil
			}
			p.retainCompletion(result.CommandTag)
		}
	}
	var values []sqltype.Value
	var catalog catalog.Read
	if p.producer != nil {
		c := p.producer
		s.enterProtocolProducer(c)
		defer s.leaveProtocolProducer(c)
		if err = s.authorizeSQLCursor(c); err != nil {
			return out, err
		}
		if err = c.openProducer(s); err != nil {
			return out, err
		}
		catalog = c.catalog
	}
	if p.row == nil {
		p.row = make([]sqltype.Value, len(p.columns))
	}
	values = p.row
	fields, scratch := p.fields, p.scratch
	defer func() { p.fields, p.scratch = fields, scratch }()
	outputInitialized := len(p.formats) == 0
	catalogReady := p.producer != nil
	for int32(maxRows) <= 0 || out.Count < int64(maxRows) {
		var present bool
		present, err = p.next(s.Op.Context(), values)
		if err != nil {
			return out, err
		}
		if !present {
			p.state = protocolPortalExhausted
			break
		}
		if !outputInitialized {
			// Destination setup belongs to its first delivered tuple, not
			// Bind/Describe or an empty result. Reuse it for later tuples.
			for i, column := range p.columns {
				if session.ProtocolFormat(p.formats, i) == 1 {
					if err = column.Usage.Type.CheckBinaryOutput(); err != nil {
						return out, err
					}
				}
			}
			outputInitialized = true
		}
		if p.outputRows == nil {
			p.outputRows = exec.NewFinalRows(p.columns, 0)
		}
		if !catalogReady {
			// Stored typed rows remain immutable. This destination captures
			// current catalog presentation once, never the old Rows formatter.
			e := s.Engine()
			e.Mu.Lock()
			auth, captureErr := e.AuthorizationLocked(s.Op)
			e.Mu.Unlock()
			if captureErr != nil {
				return out, captureErr
			}
			catalog = auth.Catalog
			p.outputRows.OperatorGroups = nil
			catalogReady = true
		}
		if err = s.captureProtocolOutput(p.outputRows, values, catalog); err != nil {
			return out, err
		}
		fields, scratch, err = session.EncodeProtocolRow(s.Op.Context(), values, p.formats, p.outputRows.Output, fields, scratch)
		if err != nil {
			return out, err
		}
		if err = s.reserveCopy(); err != nil {
			return out, err
		}
		if p.producer != nil {
			p.producer.authority.Host = nil
		}
		_, err = s.copyIO(func() (int, error) { return 0, sink(fields) })
		s.releaseCopyReservation()
		if p.producer != nil {
			p.producer.admit(s)
		}
		if err != nil {
			return out, err
		}
		out.Count++
	}
	out.Suspended = p.state != protocolPortalExhausted
	if p.state == protocolPortalExhausted {
		out.CommandTag = p.completionTag(out.Count)
	}
	return out, nil
}

func (p *protocolPortal) next(ctx context.Context, values []sqltype.Value) (bool, error) {
	if p.producer == nil {
		return p.rows.Next(ctx, values)
	}
	return p.producer.nextProtocolRow(ctx, values)
}

func (c *sqlCursor) nextProtocolRow(ctx context.Context, values []sqltype.Value) (bool, error) {
	present, err := c.stage.ProjectOrdinal(ctx, int(c.OrdinalPosition), values)
	if present && err == nil {
		c.OrdinalPosition++
	}
	return present, err
}

func (p *protocolPortal) retainCompletion(tag string) {
	p.completionBase, _, _ = strings.Cut(tag, " ")
	switch p.completionBase {
	case "SELECT", "INSERT", "UPDATE", "DELETE", "MERGE", "FETCH", "MOVE", "COPY":
		p.completionCounted = true
		p.completionInsert = p.completionBase == "INSERT"
	}
}

func (p *protocolPortal) completionTag(count int64) string {
	if !p.completionCounted {
		return p.tag
	}
	suffix := strconv.FormatInt(count, 10)
	if p.completionInsert {
		return p.completionBase + " 0 " + suffix
	}
	return p.completionBase + " " + suffix
}

func (s *StatementScope) enterProtocolProducer(c *sqlCursor) {
	c.admit(s)
	s.protocol().resume = true
}

func (s *StatementScope) leaveProtocolProducer(c *sqlCursor) {
	c.authority.Host = nil
	s.setForeignCursor(nil)
	s.RoutineAuth = AuthorizationView{}
	s.RoutineDependencies = nil
	s.protocol().resume = false
}

func (s *StatementScope) captureProtocolOutput(rows *exec.FinalRows, values []sqltype.Value, catalog catalog.Read) error {
	// Readers borrow only this row. Reuse their maps without retaining old
	// spellings or a query-sized history across demands/destinations. The ACL
	// identity stays engine-owned, and OperatorGroups belongs to the catalog.
	clear(rows.RegclassOutput)
	clear(rows.RegtypeOutput)
	clear(rows.RegprocOutput)
	clear(rows.RegprocedureOutput)
	clear(rows.RegroleOutput)
	clear(rows.OperatorOutput)
	clear(rows.RegobjectOutput)
	clear(rows.RegTSOutput)
	clear(rows.AclitemOutput)
	rows.Values, rows.Output = values, s.Output
	if err := s.captureSQLCursorOutput(rows, catalog); err != nil {
		return err
	}
	rows.Output.Regclass, rows.Output.Regtype, rows.Output.Regproc, rows.Output.Regrole = rows, rows, rows, rows
	rows.Output.Operator, rows.Output.Regcollation, rows.Output.Regnamespace, rows.Output.RegTS = rows, rows, rows, rows
	rows.Output.ACLItem = rows
	return nil
}

// protocolScope is the statement's wire-protocol slot: the bound or simple-query
// portal and the execution policy flags of the current protocol message.
type protocolScope struct {
	bind            *protocolPortal
	execute         *protocolPortal
	resume          bool
	simple          bool
	simpleCreated   bool
	simpleStreaming bool
	simplePortal    *protocolPortal
	command         bool // Explicit wire preparation/execution policy, not session state.
}

// protocol returns the statement's protocol slot, allocating it on first use.
func (s *StatementScope) protocol() *protocolScope {
	if s.protocolSlot == nil {
		s.protocolSlot = new(protocolScope)
	}
	return s.protocolSlot
}

// protocolOrZero reads the protocol slot; a nil slot reads as zero.
func (s *StatementScope) protocolOrZero() protocolScope {
	if s.protocolSlot == nil {
		return protocolScope{}
	}
	return *s.protocolSlot
}

// setProtocolBind stores the portal bound for execution; clearing it never
// allocates the slot.
func (s *StatementScope) setProtocolBind(p *protocolPortal) {
	if p != nil || s.protocolSlot != nil {
		s.protocol().bind = p
	}
}
