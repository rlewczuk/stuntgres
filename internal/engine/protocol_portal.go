package engine

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
	tx                ids.TxRef
	born              uint64 // Shared canonical cursor/portal birth frontier.
	entry             sqlPreparedStatement
	context           ProtocolContext
	columns           []catalog.Column
	formats           []int16
	values            []sqltype.Value
	producer          *sqlCursor
	rows              exec.RowResult
	started, retired  bool
	state             protocolPortalState
	tag               string
	completionBase    string // Actual executed command, independent of outer syntax/resources.
	completionCounted bool
	completionInsert  bool
	row               []sqltype.Value
	fields            []ProtocolField
	scratch           []byte
	outputRows        *exec.FinalRows
}
type ProtocolExecution struct {
	CommandTag string
	Count      int64
	Suspended  bool
	Empty      bool
}

func (p *protocolPortal) close(ctx context.Context) error {
	p.retired = true
	return p.releaseExecutor(ctx)
}

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
	p.row, p.fields, p.scratch, p.outputRows = nil, nil, nil, nil
	return err
}
func (s *StatementScope) BindProtocolPortal(name, statement string, input ProtocolInput) (out ProtocolContext, err error) {
	entry := payloadOf(s.op.Session).preparedSQL.entries[statement]
	if entry == nil {
		return out, missingSQLPrepared(statement)
	}
	out.Source = entry.source
	formats, err := protocolFormats(input)
	if err != nil {
		return out, err
	}
	count, err := input.Uint16()
	if err != nil {
		return out, err
	}
	if len(formats) > 1 && len(formats) != int(count) {
		return out, sqlError("08P01", fmt.Sprintf("bind message has %d parameter formats but %d parameters", len(formats), count))
	}
	if int(count) != entry.description.Parameters.Count {
		return out, sqlError("08P01", fmt.Sprintf("bind message supplies %d parameters, but prepared statement \"%s\" requires %d", count, statement, entry.description.Parameters.Count))
	}
	if err = s.gate(entry.body); err != nil {
		return out, err
	}
	if entry.body == nil {
		s.owner.Mu.Lock()
		err = s.admitTransactionLocked(s.engine())
		s.owner.Mu.Unlock()
		if err != nil {
			return out, err
		}
	}
	r := payloadOf(s.op.Session).preparedSQL
	if old := r.portals[name]; old != nil && !old.retired {
		if name != "" {
			return out, sqlError("42P03", fmt.Sprintf("cursor \"%s\" already exists", name))
		}
		if err = s.protocolSimpleIO(func() error { return old.close(s.op.Context()) }); err != nil {
			return out, err
		}
	}
	if payloadOf(s.op.Session).cursors.entries[name] != nil {
		return out, sqlError("42P03", fmt.Sprintf("cursor \"%s\" already exists", name))
	}
	births := payloadOf(s.op.Session).cursors
	if births.ordinal == ^uint64(0) {
		return out, fmt.Errorf("stuntgres: portal creation counter exhausted")
	}
	births.ordinal++
	p := &protocolPortal{entry: *entry, context: out, columns: entry.description.Results, tx: s.txRef, born: births.ordinal}
	if r.portals == nil {
		r.portals = make(map[string]*protocolPortal)
	}
	r.portals[name] = p
	defer func() {
		if err == nil {
			err = s.sessionPublicationPreflight()
		}
		if err == nil {
			s.completed, s.sessionCompleted = true, true
		}
		out = p.context
		if err != nil {
			_ = s.protocolTerminalIO(func() error { return p.close(s.op.Context()) })
			delete(r.portals, name)
		}
	}()
	importer := func(ctx context.Context, formal analyze.ParameterInfo, frame []sqltype.Value, env sqltype.InputEnvironment) error {
		if e := s.protocolImport(input, formats, formal, frame, env, &p.context, protocolBindInput); e != nil {
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
		p.formats, e = protocolResultFormats(input, len(p.columns))
		return e
	}
	if entry.body == nil {
		if count == 0 {
			p.formats, err = protocolResultFormats(input, 0)
			return out, err
		}
		if count != 0 {
			formal := analyze.ParameterInfo{Count: int(count), Types: make([]sqltype.Usage, int(count))}
			e := s.engine()
			e.mu.Lock()
			auth, lookupErr := e.authorizationLocked(s.op)
			if lookupErr == nil {
				for i, oid := range entry.parameterOIDs {
					typ, ok := auth.Catalog.LookupType(sqltype.ID(oid))
					if !ok {
						lookupErr = sqlError("42704", fmt.Sprintf("type with OID %d does not exist", oid))
						break
					}
					formal.Types[i] = typ.Usage()
				}
			}
			e.mu.Unlock()
			if lookupErr != nil {
				return out, lookupErr
			}
			if err = s.prepareProtocolUtilityInput(formal.Types); err != nil {
				return out, err
			}
			p.values = make([]sqltype.Value, int(count))
			err = importer(s.op.Context(), formal, p.values, s.input)
		}
		return out, err
	}
	switch entry.body.(type) {
	case *parser.Select, *parser.Insert, *parser.Update, *parser.Delete, *parser.Merge:
		s.Expected = &p.entry.description
		s.protocol().bind = p
		_, err = s.Execute(entry.body, int(count), importer)
		s.setProtocolBind(nil)
	default:
		if err = s.prepareProtocolUtilityInput(entry.description.Parameters.Types); err != nil {
			return out, err
		}
		p.values = make([]sqltype.Value, int(count))
		err = importer(s.op.Context(), entry.description.Parameters, p.values, s.input)
	}
	p.tx = s.txRef
	return out, err
}
func (s *StatementScope) DescribeProtocolPortal(name string) (ProtocolPortalDescription, error) {
	if err := s.active(); err != nil {
		return ProtocolPortalDescription{}, err
	}
	p := payloadOf(s.op.Session).preparedSQL.portals[name]
	if p == nil || p.retired {
		return ProtocolPortalDescription{}, missingProtocolPortal(name)
	}
	if protocolHasRows(p.entry.body, p.columns) && s.op.Session.TransactionState() == session.TxAborted {
		return ProtocolPortalDescription{}, protocolAbortedError()
	}
	return ProtocolPortalDescription{Columns: p.columns, Formats: p.formats, Context: p.context, Empty: p.entry.body == nil, HasRows: protocolHasRows(p.entry.body, p.columns)}, nil
}
func (s *StatementScope) CloseProtocolPortal(name string) error {
	r := payloadOf(s.op.Session).preparedSQL
	if p := r.portals[name]; p != nil {
		return s.protocolSimpleIO(func() error {
			delete(r.portals, name)
			return p.close(s.op.Context())
		})
	}
	if c := payloadOf(s.op.Session).cursors.entries[name]; c != nil {
		return s.protocolSimpleIO(func() error {
			delete(payloadOf(s.op.Session).cursors.entries, name)
			return c.dispose(s.op.Context())
		})
	}
	return nil
}

func protocolAbortedError() error {
	return sqlError("25P02", "current transaction is aborted, commands ignored until end of transaction block")
}

// Savepoint publication only removes visibility; callbacks and producer
// disposal belong to the existing unlocked terminal cleanup path.
func retireProtocolPortalsAfter(t *session.TransactionOwner, frontier uint64) {
	if payloadOf(t.Session).preparedSQL == nil {
		return
	}
	for _, p := range payloadOf(t.Session).preparedSQL.portals {
		if p.tx == t.Ref && p.born > frontier {
			p.retired = true
		}
	}
}

func (s *StatementScope) installProtocolProducer(root plan.Node, source *exec.Source, values []sqltype.Value) (Result, error) {
	project, ok := root.(*plan.ProjectNode)
	if !ok {
		source.Close()
		return Result{}, fmt.Errorf("stuntgres: portal lacks projection")
	}
	c := &sqlCursor{tx: s.txRef, epoch: s.txRef.Epoch(), root: project, schema: project.Schema(), source: source, authority: s.authority,
		leaves: s.querySources, locks: s.queryLocks, catalog: s.inquiry.commentCatalog, roles: s.inquiry.commentRoles, routineDependencies: s.routineDependencies, routineAuth: s.routineAuth,
		deparseAuth: s.inquiry.deparseAuth, deparsePath: s.inquiry.deparsePath, collationPath: s.inquiry.collationPath, visibilityCatalog: s.inquiry.visibilityCatalog, visibilityRelationPath: s.inquiry.visibilityRelationPath, visibilityOtherPath: s.inquiry.visibilityOtherPath}
	c.state = exec.State{Source: source, Parameters: values, Input: s.input, Output: s.output, Locking: source.Locks != nil && source.Locks.RowMarked(), HashFilter: exec.HashQueryFilter(root)}
	c.state.Input.LockedDomainInput, c.state.Input.DomainChecks = nil, nil
	c.state.Output.Regclass, c.state.Output.Regtype = nil, nil
	c.state.Output.Regproc, c.state.Output.Regrole, c.state.Output.Operator = nil, nil, nil
	c.state.Output.Regcollation, c.state.Output.Regnamespace, c.state.Output.RegTS = nil, nil, nil
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
	c.foreignOwner = s.foreignTablesSlot.takeOwner()
	bind := s.protocolOrZero().bind
	bind.producer = c
	bind.completionBase, bind.completionCounted = "SELECT", true
	s.authority.Host = nil
	s.routineAuth = AuthorizationView{}
	s.routineDependencies = nil
	return Result{}, nil
}

func (s *StatementScope) ExecuteProtocolPortal(name string, maxRows uint32, sink func([]ProtocolField) error, source io.Reader, destination io.Writer, hooks CopyProtocolHooks) (out ProtocolExecution, err error) {
	p := payloadOf(s.op.Session).preparedSQL.portals[name]
	if p == nil || p.retired {
		return out, missingProtocolPortal(name)
	}
	return s.executeProtocolPortal(p, name, maxRows, sink, source, destination, hooks, true)
}

func (s *StatementScope) executeProtocolPortal(p *protocolPortal, name string, maxRows uint32, sink func([]ProtocolField) error, source io.Reader, destination io.Writer, hooks CopyProtocolHooks, extended bool) (out ProtocolExecution, err error) {
	if p.entry.body == nil {
		out.Empty = true
		return out, nil
	}
	if err = s.gate(p.entry.body); err != nil {
		return out, err
	}
	if p.tx != s.txRef {
		return out, missingProtocolPortal(name)
	}
	if cycle := s.owner.ProtocolCycle; extended && cycle != nil {
		cycle.Executed = true
	}
	if p.state == protocolPortalDone || p.state == protocolPortalFailed {
		return out, sqlError("55000", fmt.Sprintf("portal \"%s\" cannot be run", name))
	}
	defer func() {
		if err != nil {
			p.state = protocolPortalFailed
			_ = s.protocolTerminalIO(func() error { return p.releaseExecutor(s.op.Context()) })
		}
	}()
	if p.state == protocolPortalExhausted {
		out.CommandTag = p.completionTag(0)
		return out, nil
	}
	if !p.started {
		p.started = true
		if p.producer == nil {
			s.Expected = &p.entry.description
			stream := protocolCopyExecution{source: source, destination: destination, hooks: hooks}
			result, e := s.execute(p.entry.body, len(p.values), func(_ context.Context, _ analyze.ParameterInfo, frame []sqltype.Value, _ sqltype.InputEnvironment) error {
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
		present, err = p.next(s.op.Context(), values)
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
				if protocolFormat(p.formats, i) == 1 {
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
			e := s.engine()
			e.mu.Lock()
			auth, captureErr := e.authorizationLocked(s.op)
			e.mu.Unlock()
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
		fields, scratch, err = encodeProtocolRow(s.op.Context(), values, p.formats, p.outputRows.Output, fields, scratch)
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
	present, err := c.stage.ProjectOrdinal(ctx, int(c.ordinalPosition), values)
	if present && err == nil {
		c.ordinalPosition++
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
	s.routineAuth = AuthorizationView{}
	s.routineDependencies = nil
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
	rows.Values, rows.Output = values, s.output
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
