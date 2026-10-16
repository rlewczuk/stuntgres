package engine

import (
	"fmt"
	"slices"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

type ProtocolStatement struct {
	Description    StatementDescription
	ParameterOIDs  []uint32
	Context        ProtocolContext
	Empty, HasRows bool
}
type ProtocolPortalDescription struct {
	Columns        []ColumnDescriptor
	Formats        []int16
	Context        ProtocolContext
	Empty, HasRows bool
}

func (s *StatementScope) ParseProtocol(name, source string, hints []uint32) (ProtocolStatement, error) {
	if err := s.active(); err != nil {
		return ProtocolStatement{}, err
	}
	r := s.op.session.preparedSQL
	if name == "" {
		delete(r.entries, "")
	}
	s.op.session.settings.mu.Lock()
	standard := s.op.session.settings.value("standard_conforming_strings") == "on"
	s.op.session.settings.mu.Unlock()
	statements, err := parser.ParseWithOptions(source, parser.Options{Context: s.op.Context(), StandardConformingStrings: standard, Notice: s.op.parseNotice})
	if err != nil {
		return ProtocolStatement{}, err
	}
	if len(statements) > 1 {
		return ProtocolStatement{}, sqlError("42601", "cannot insert multiple commands into a prepared statement")
	}
	var body parser.Statement
	if len(statements) != 0 {
		body = statements[0]
	}
	if err = s.gate(body); err != nil {
		return ProtocolStatement{}, err
	}
	if body == nil {
		s.owner.mu.Lock()
		err = s.admitTransactionLocked(s.op.session.Engine())
		s.owner.mu.Unlock()
		if err != nil {
			return ProtocolStatement{}, err
		}
		s.worked = true
		description := StatementDescription{Parameters: ParameterInfo{Count: len(hints)}}
		if err = s.publishProtocolStatement(name, &sqlPreparedStatement{source: source, description: description, parameterOIDs: slices.Clone(hints)}); err != nil {
			return ProtocolStatement{}, err
		}
		s.completed, s.sessionCompleted = true, true
		return ProtocolStatement{Description: description, ParameterOIDs: slices.Clone(hints), Context: ProtocolContext{Source: source}, Empty: true}, nil
	}
	types := make([]sqltype.Usage, len(hints))
	e := s.op.session.Engine()
	e.mu.Lock()
	auth, err := e.authorizationLocked(s.op)
	if err == nil {
		for i, oid := range hints {
			if oid == 0 || oid == uint32(sqltype.Unknown) {
				continue
			}
			typ, ok := auth.catalog.LookupType(sqltype.ID(oid))
			if !ok {
				err = sqlError("42704", fmt.Sprintf("type with OID %d does not exist", oid))
				break
			}
			if typ.ID() == 2249 {
				// RECORD has an existing occurrence-owned NULL/input-boundary
				// provider; the catalog identity alone is lookup-only.
				typ = sqltype.DeferredRecord()
			}
			if !typ.Concrete() {
				err = sqlError("0A000", fmt.Sprintf("type %s is not supported", typ.DiagnosticName()))
				break
			}
			types[i] = typ.Usage()
		}
	}
	e.mu.Unlock()
	if err != nil {
		return ProtocolStatement{}, err
	}
	s.initialParameters = types
	var description StatementDescription
	if body != nil {
		description, err = s.describeProtocolBody(body)
	} else {
		description.Parameters = ParameterInfo{Count: len(hints), Types: types}
	}
	if err != nil {
		return ProtocolStatement{}, err
	}
	if err = s.publishProtocolStatement(name, &sqlPreparedStatement{body: body, source: source, description: description}); err != nil {
		return ProtocolStatement{}, err
	}
	s.completed, s.sessionCompleted = true, true
	return ProtocolStatement{Description: description, ParameterOIDs: protocolParameterOIDs(description.Parameters), Context: ProtocolContext{Source: source}, Empty: body == nil, HasRows: protocolHasRows(body, description.Results)}, nil
}

func (s *StatementScope) publishProtocolStatement(name string, entry *sqlPreparedStatement) error {
	if err := s.sessionPublicationPreflight(); err != nil {
		return err
	}
	r := s.op.session.preparedSQL
	if name != "" && r.entries[name] != nil {
		return sqlError("42P05", fmt.Sprintf("prepared statement \"%s\" already exists", name))
	}
	r.entries[name] = entry
	return nil
}
func (s *StatementScope) DescribeProtocolStatement(name string) (ProtocolStatement, error) {
	if err := s.active(); err != nil {
		return ProtocolStatement{}, err
	}
	entry := s.op.session.preparedSQL.entries[name]
	if entry == nil {
		return ProtocolStatement{}, missingSQLPrepared(name)
	}
	description := entry.description
	if s.op.session.TransactionState() == TxAborted {
		if protocolHasRows(entry.body, description.Results) {
			return ProtocolStatement{}, protocolAbortedError()
		}
		oids := entry.parameterOIDs
		if entry.body != nil {
			oids = protocolParameterOIDs(description.Parameters)
		}
		return ProtocolStatement{Description: description, ParameterOIDs: oids, Context: ProtocolContext{Source: entry.source}, Empty: entry.body == nil}, nil
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
		oids = protocolParameterOIDs(description.Parameters)
	}
	return ProtocolStatement{Description: description, ParameterOIDs: oids, Context: ProtocolContext{Source: entry.source}, Empty: entry.body == nil, HasRows: protocolHasRows(entry.body, description.Results)}, nil
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
	description := StatementDescription{catalogOwner: s.op.session.Engine(), catalogEpoch: s.txRef.epoch}
	if executeBody {
		var err error
		description, err = s.Describe(body)
		if err != nil {
			return StatementDescription{}, err
		}
	}
	for i, usage := range s.initialParameters {
		if usage.Type == nil {
			return StatementDescription{}, sqlError("42P18", fmt.Sprintf("could not determine data type of parameter $%d", i+1))
		}
	}
	description.Parameters = ParameterInfo{Count: len(s.initialParameters), Types: slices.Clone(s.initialParameters)}
	return description, nil
}
func (s *StatementScope) CloseProtocolStatement(name string) error {
	if err := s.active(); err != nil {
		return err
	}
	delete(s.op.session.preparedSQL.entries, name)
	return nil
}
func (s *StatementScope) InvalidateUnnamedProtocolStatement() error {
	return s.CloseProtocolStatement("")
}
func (s *StatementScope) ProtocolStatementContext(name string) ProtocolContext {
	if e := s.op.session.preparedSQL.entries[name]; e != nil {
		return ProtocolContext{Source: e.source}
	}
	return ProtocolContext{}
}
func (s *StatementScope) ProtocolPortalContext(name string) ProtocolContext {
	if p := s.op.session.preparedSQL.portals[name]; p != nil {
		return p.context
	}
	return ProtocolContext{}
}
func protocolPlainSelect(stmt parser.Statement) bool {
	q, ok := stmt.(*parser.Select)
	if !ok {
		return false
	}
	for _, cte := range q.With {
		if !protocolPlainSelect(cte.Query) {
			return false
		}
	}
	return true
}

func protocolParameterOIDs(formal ParameterInfo) []uint32 {
	ids := make([]uint32, formal.Count)
	for i, usage := range formal.Types {
		if usage.Type != nil {
			ids[i] = uint32(usage.Type.ID())
		}
	}
	return ids
}

func protocolPortalExists(session *Session, name string) bool {
	p := session.preparedSQL.portals[name]
	return p != nil && !p.retired
}

func missingProtocolPortal(name string) error {
	return sqlError("34000", fmt.Sprintf("portal \"%s\" does not exist", name))
}

func protocolHasRows(stmt parser.Statement, columns []ColumnDescriptor) bool {
	if columns != nil {
		return true
	}
	switch raw := stmt.(type) {
	case *parser.Select:
		return true
	case *parser.Insert:
		return raw.Returning != nil
	case *parser.Update:
		return raw.Returning != nil
	case *parser.Delete:
		return raw.Returning != nil
	case *parser.Merge:
		return raw.Returning != nil
	}
	return false
}
