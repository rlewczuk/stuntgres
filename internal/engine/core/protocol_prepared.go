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
	Description    StatementDescription
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
	r := PayloadOf(s.Op.Session).preparedSQL
	if name == "" {
		delete(r.entries, "")
	}
	s.Op.Session.Settings.Mu.Lock()
	standard := s.Op.Session.Settings.Value("standard_conforming_strings") == "on"
	s.Op.Session.Settings.Mu.Unlock()
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
		description := StatementDescription{Parameters: analyze.ParameterInfo{Count: len(hints)}}
		if err = s.publishProtocolStatement(name, &sqlPreparedStatement{source: source, description: description, parameterOIDs: slices.Clone(hints)}); err != nil {
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
	var description StatementDescription
	if body != nil {
		description, err = s.describeProtocolBody(body)
	} else {
		description.Parameters = analyze.ParameterInfo{Count: len(hints), Types: types}
	}
	if err != nil {
		return ProtocolStatement{}, err
	}
	if err = s.publishProtocolStatement(name, &sqlPreparedStatement{body: body, source: source, description: description}); err != nil {
		return ProtocolStatement{}, err
	}
	s.Completed, s.sessionCompleted = true, true
	return ProtocolStatement{Description: description, ParameterOIDs: session.ProtocolParameterOIDs(description.Parameters.Count, description.Parameters.Types), Context: session.ProtocolContext{Source: source}, Empty: body == nil, HasRows: session.ProtocolHasRows(body, description.Results)}, nil
}

func (s *StatementScope) publishProtocolStatement(name string, entry *sqlPreparedStatement) error {
	if err := s.sessionPublicationPreflight(); err != nil {
		return err
	}
	r := PayloadOf(s.Op.Session).preparedSQL
	if name != "" && r.entries[name] != nil {
		return SQLError("42P05", fmt.Sprintf("prepared statement \"%s\" already exists", name))
	}
	r.entries[name] = entry
	return nil
}
func (s *StatementScope) DescribeProtocolStatement(name string) (ProtocolStatement, error) {
	if err := s.Active(); err != nil {
		return ProtocolStatement{}, err
	}
	entry := PayloadOf(s.Op.Session).preparedSQL.entries[name]
	if entry == nil {
		return ProtocolStatement{}, missingSQLPrepared(name)
	}
	description := entry.description
	if s.Op.Session.TransactionState() == session.TxAborted {
		if session.ProtocolHasRows(entry.body, description.Results) {
			return ProtocolStatement{}, session.AbortedError()
		}
		oids := entry.parameterOIDs
		if entry.body != nil {
			oids = session.ProtocolParameterOIDs(description.Parameters.Count, description.Parameters.Types)
		}
		return ProtocolStatement{Description: description, ParameterOIDs: oids, Context: session.ProtocolContext{Source: entry.source}, Empty: entry.body == nil}, nil
	}
	if entry.body != nil {
		s.Expected = &entry.description
		s.initialParameters = entry.description.Parameters.Types
		var err error
		description, err = s.describeProtocolBody(entry.body)
		if err != nil {
			return ProtocolStatement{}, err
		}
		if err = checkStatementSignature(entry.description, description); err != nil {
			return ProtocolStatement{}, err
		}
		entry.description = description
	}
	oids := entry.parameterOIDs
	if entry.body != nil {
		oids = session.ProtocolParameterOIDs(description.Parameters.Count, description.Parameters.Types)
	}
	return ProtocolStatement{Description: description, ParameterOIDs: oids, Context: session.ProtocolContext{Source: entry.source}, Empty: entry.body == nil, HasRows: session.ProtocolHasRows(entry.body, description.Results)}, nil
}

// COPY and EXECUTE retain their own unused wire formals as untransformed
// utilities. COPY preflight still belongs to real execution; EXECUTE metadata
// describes its referenced result, not a shared argument namespace.
func (s *StatementScope) describeProtocolBody(body parser.Statement) (StatementDescription, error) {
	_, copyBody := body.(*parser.Copy)
	_, executeBody := body.(*parser.ExecuteSQL)
	if !copyBody && !executeBody {
		return s.Describe(body)
	}
	description := StatementDescription{catalogOwner: &s.Engine().Catalog.Owner, catalogEpoch: s.TxRef.Epoch()}
	if executeBody {
		var err error
		description, err = s.Describe(body)
		if err != nil {
			return StatementDescription{}, err
		}
	}
	for i, usage := range s.initialParameters {
		if usage.Type == nil {
			return StatementDescription{}, SQLError("42P18", fmt.Sprintf("could not determine data type of parameter $%d", i+1))
		}
	}
	description.Parameters = analyze.ParameterInfo{Count: len(s.initialParameters), Types: slices.Clone(s.initialParameters)}
	return description, nil
}
func (s *StatementScope) CloseProtocolStatement(name string) error {
	if err := s.Active(); err != nil {
		return err
	}
	delete(PayloadOf(s.Op.Session).preparedSQL.entries, name)
	return nil
}
func (s *StatementScope) InvalidateUnnamedProtocolStatement() error {
	return s.CloseProtocolStatement("")
}
func (s *StatementScope) ProtocolStatementContext(name string) session.ProtocolContext {
	if e := PayloadOf(s.Op.Session).preparedSQL.entries[name]; e != nil {
		return session.ProtocolContext{Source: e.source}
	}
	return session.ProtocolContext{}
}
func (s *StatementScope) ProtocolPortalContext(name string) session.ProtocolContext {
	if p := PayloadOf(s.Op.Session).preparedSQL.portals[name]; p != nil {
		return p.context
	}
	return session.ProtocolContext{}
}

func protocolPortalExists(session *session.Session, name string) bool {
	p := PayloadOf(session).preparedSQL.portals[name]
	return p != nil && !p.retired
}

func missingProtocolPortal(name string) error {
	return SQLError("34000", fmt.Sprintf("portal \"%s\" does not exist", name))
}
