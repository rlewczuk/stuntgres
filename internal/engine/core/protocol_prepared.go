package core

import (
	"fmt"
	"slices"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

type ProtocolStatement struct {
	Description    Description
	ParameterOIDs  []uint32
	Context        session.ProtocolContext
	Empty, HasRows bool
}
type ProtocolPortalDescription struct {
	Columns        []catalog.Column
	Formats        []int16
	Context        session.ProtocolContext
	Empty, HasRows bool
}

func (s *StatementScope) ParseProtocol(name, source string, hints []uint32) (ProtocolStatement, error) {
	if err := s.Active(); err != nil {
		return ProtocolStatement{}, err
	}
	s.Op.OriginalSQL, s.Op.SQLAvailable = source, true
	r := PayloadOf(s.Op.Session).PreparedSQL
	if name == "" {
		r.Remove("")
	}
	s.Op.EffectiveSettings().Mu.Lock()
	standard := s.Op.EffectiveSettings().Value("standard_conforming_strings") == "on"
	s.Op.EffectiveSettings().Mu.Unlock()
	statements, err := parser.ParseWithOptions(source, parser.Options{Context: s.Op.Context(), StandardConformingStrings: standard, Notice: s.Op.ParseNotice})
	if err != nil {
		return ProtocolStatement{}, err
	}
	if len(statements) > 1 {
		return ProtocolStatement{}, SQLError("42601", "cannot insert multiple commands into a prepared statement")
	}
	var body parser.Statement
	if len(statements) != 0 {
		body = statements[0]
	}
	if err = s.Gate(body); err != nil {
		return ProtocolStatement{}, err
	}
	if body == nil {
		s.Owner.Mu.Lock()
		err = s.admitTransactionLocked(s.Engine())
		s.Owner.Mu.Unlock()
		if err != nil {
			return ProtocolStatement{}, err
		}
		s.worked = true
		description := Description{Parameters: analyze.ParameterInfo{Count: len(hints)}}
		if err = s.publishProtocolStatement(name, &PreparedDefinition{Source: source, Description: description, ParameterOIDs: slices.Clone(hints)}); err != nil {
			return ProtocolStatement{}, err
		}
		s.Completed, s.sessionCompleted = true, true
		return ProtocolStatement{Description: description, ParameterOIDs: slices.Clone(hints), Context: session.ProtocolContext{Source: source}, Empty: true}, nil
	}
	types := make([]sqltype.Usage, len(hints))
	e := s.Engine()
	e.Mu.Lock()
	auth, err := e.AuthorizationLocked(s.Op)
	if err == nil {
		for i, oid := range hints {
			if oid == 0 || oid == uint32(sqltype.Unknown) {
				continue
			}
			typ, ok := auth.Catalog.LookupType(sqltype.ID(oid))
			if !ok {
				err = SQLError("42704", fmt.Sprintf("type with OID %d does not exist", oid))
				break
			}
			if typ.ID() == 2249 {
				// RECORD has an existing occurrence-owned NULL/input-boundary
				// provider; the catalog identity alone is lookup-only.
				typ = sqltype.DeferredRecord()
			}
			if !typ.Concrete() {
				err = SQLError("0A000", fmt.Sprintf("type %s is not supported", typ.DiagnosticName()))
				break
			}
			types[i] = typ.Usage()
		}
	}
	e.Mu.Unlock()
	if err != nil {
		return ProtocolStatement{}, err
	}
	s.initialParameters = types
	var description Description
	if body != nil {
		description, err = s.describeProtocolBody(body)
	} else {
		description.Parameters = analyze.ParameterInfo{Count: len(hints), Types: types}
	}
	if err != nil {
		return ProtocolStatement{}, err
	}
	if err = s.publishProtocolStatement(name, &PreparedDefinition{Body: body, Source: source, Description: description}); err != nil {
		return ProtocolStatement{}, err
	}
	s.Completed, s.sessionCompleted = true, true
	return ProtocolStatement{Description: description, ParameterOIDs: session.ProtocolParameterOIDs(description.Parameters.Count, description.Parameters.Types), Context: session.ProtocolContext{Source: source}, Empty: body == nil, HasRows: session.ProtocolHasRows(body, description.Results)}, nil
}

func (s *StatementScope) publishProtocolStatement(name string, entry *PreparedDefinition) error {
	if err := s.sessionPublicationPreflight(); err != nil {
		return err
	}
	r := PayloadOf(s.Op.Session).PreparedSQL
	if name != "" && r.Lookup(name) != nil {
		return SQLError("42P05", fmt.Sprintf("prepared statement \"%s\" already exists", name))
	}
	entry.Created, entry.FromSQL = s.Op.StatementStart(), false
	r.Install(name, entry)
	return nil
}
func (s *StatementScope) DescribeProtocolStatement(name string) (ProtocolStatement, error) {
	if err := s.Active(); err != nil {
		return ProtocolStatement{}, err
	}
	entry := PayloadOf(s.Op.Session).PreparedSQL.Lookup(name)
	if entry == nil {
		return ProtocolStatement{}, missingSQLPrepared(name)
	}
	description := entry.Description
	if s.Op.Session.TransactionState() == session.TxAborted {
		if session.ProtocolHasRows(entry.Body, description.Results) {
			return ProtocolStatement{}, session.AbortedError()
		}
		oids := entry.ParameterOIDs
		if entry.Body != nil {
			oids = session.ProtocolParameterOIDs(description.Parameters.Count, description.Parameters.Types)
		}
		return ProtocolStatement{Description: description, ParameterOIDs: oids, Context: session.ProtocolContext{Source: entry.Source}, Empty: entry.Body == nil}, nil
	}
	if entry.Body != nil {
		s.Expected = &entry.Description
		s.initialParameters = entry.Description.Parameters.Types
		var err error
		description, err = s.describeProtocolBody(entry.Body)
		if err != nil {
			return ProtocolStatement{}, err
		}
		if err = CheckStatementSignature(entry.Description, description); err != nil {
			return ProtocolStatement{}, err
		}
		description.Folds = entry.Description.Folds
		entry.Description = description
	}
	oids := entry.ParameterOIDs
	if entry.Body != nil {
		oids = session.ProtocolParameterOIDs(description.Parameters.Count, description.Parameters.Types)
	}
	return ProtocolStatement{Description: description, ParameterOIDs: oids, Context: session.ProtocolContext{Source: entry.Source}, Empty: entry.Body == nil, HasRows: session.ProtocolHasRows(entry.Body, description.Results)}, nil
}

// COPY and EXECUTE retain their own unused wire formals as untransformed
// utilities. COPY preflight still belongs to real execution; EXECUTE metadata
// describes its referenced result, not a shared argument namespace.
func (s *StatementScope) describeProtocolBody(body parser.Statement) (Description, error) {
	_, copyBody := body.(*parser.Copy)
	_, executeBody := body.(*parser.ExecuteSQL)
	if !copyBody && !executeBody {
		return s.Describe(body)
	}
	description := Description{CatalogOwner: &s.Engine().Catalog.Owner, CatalogEpoch: s.TxRef.Epoch()}
	if executeBody {
		var err error
		description, err = s.Describe(body)
		if err != nil {
			return Description{}, err
		}
	}
	for i, usage := range s.initialParameters {
		if usage.Type == nil {
			return Description{}, SQLError("42P18", fmt.Sprintf("could not determine data type of parameter $%d", i+1))
		}
	}
	description.Parameters = analyze.ParameterInfo{Count: len(s.initialParameters), Types: slices.Clone(s.initialParameters)}
	return description, nil
}
func (s *StatementScope) CloseProtocolStatement(name string) error {
	if err := s.Active(); err != nil {
		return err
	}
	PayloadOf(s.Op.Session).PreparedSQL.Remove(name)
	return nil
}
func (s *StatementScope) InvalidateUnnamedProtocolStatement() error {
	return s.CloseProtocolStatement("")
}
func (s *StatementScope) ProtocolStatementContext(name string) session.ProtocolContext {
	if e := PayloadOf(s.Op.Session).PreparedSQL.Lookup(name); e != nil {
		return session.ProtocolContext{Source: e.Source}
	}
	return session.ProtocolContext{}
}
func (s *StatementScope) ProtocolPortalContext(name string) session.ProtocolContext {
	if p := protocolPortalOf(PayloadOf(s.Op.Session).PreparedSQL.Binding(name)); p != nil {
		return p.context
	}
	return session.ProtocolContext{}
}

func protocolPortalExists(session *session.Session, name string) bool {
	p := protocolPortalOf(PayloadOf(session).PreparedSQL.Binding(name))
	return p != nil && !p.Retired
}

func missingProtocolPortal(name string) error {
	return SQLError("34000", fmt.Sprintf("portal \"%s\" does not exist", name))
}
