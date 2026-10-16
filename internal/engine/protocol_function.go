package engine

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func (s *StatementScope) CallProtocolFunction(input ProtocolInput) (ProtocolField, error) {
	if err := s.active(); err != nil {
		return ProtocolField{}, err
	}
	t := s.owner
	t.mu.Lock()
	if t.state == TxAborted {
		t.mu.Unlock()
		return ProtocolField{}, sqlError("25P02", "current transaction is aborted, commands ignored until end of transaction block")
	}
	if c := t.protocolCycle; c != nil {
		c.active = c.executed
	}
	err := s.admitTransactionLocked(s.op.session.Engine())
	t.mu.Unlock()
	if err != nil {
		return ProtocolField{}, err
	}
	s.worked = true
	if err = s.snapshotDataPhase(); err != nil {
		return ProtocolField{}, err
	}
	rawOID, err := input.Int32()
	if err != nil {
		return ProtocolField{}, err
	}
	oid := uint32(rawOID)
	e := s.op.session.Engine()
	e.mu.Lock()
	auth, err := e.authorizationLocked(s.op)
	if err != nil {
		e.mu.Unlock()
		return ProtocolField{}, err
	}
	d, ok := auth.catalog.state.descriptors[ObjectAddress{ClassID: ProcedureClassID, ObjectID: OID(oid)}].(*procedureDescriptor)
	if !ok {
		identity, found := routineIdentityByOID(auth.catalog, OID(oid))
		e.mu.Unlock()
		if found {
			return ProtocolField{}, sqlError("0A000", fmt.Sprintf("function \"%s\" is not supported", identity.name))
		}
		return ProtocolField{}, sqlError("42883", fmt.Sprintf("function with OID %d does not exist", oid))
	}
	if d.routineKind != functionRoutine || d.returnsSet() {
		e.mu.Unlock()
		return ProtocolField{}, sqlError("0A000", fmt.Sprintf("cannot call function \"%s\" via fastpath interface", d.name))
	}
	ns := auth.catalog.state.descriptors[d.namespace]
	nsRef := ObjectRef{owner: auth.catalog.owner.owner, Epoch: auth.catalog.Epoch(), Address: d.namespace}
	ref := ObjectRef{owner: auth.catalog.owner.owner, Epoch: auth.catalog.Epoch(), Address: d.address}
	if err = auth.Check(AccessRequirement{Object: nsRef, Privileges: authorization.UsagePrivilege}); err == nil {
		err = auth.Check(AccessRequirement{Object: ref, Privileges: authorization.ExecutePrivilege})
	}
	if err != nil {
		e.mu.Unlock()
		return ProtocolField{}, err
	}
	var usages []sqltype.Usage
	if d.signature != "" {
		for _, word := range strings.Split(string(d.signature), ",") {
			id, parseErr := strconv.ParseUint(word, 10, 32)
			if parseErr != nil {
				e.mu.Unlock()
				return ProtocolField{}, parseErr
			}
			typ, found := auth.catalog.LookupType(sqltype.ID(id))
			if !found {
				e.mu.Unlock()
				return ProtocolField{}, sqlError("0A000", "function argument type is unavailable")
			}
			usages = append(usages, typ.Usage())
		}
	}
	formal := ParameterInfo{Count: len(usages), Types: usages}
	if err = s.captureFormalInputLocked(auth, usages); err == nil {
		err = e.captureCommentSnapshotLocked(s)
	}
	if err == nil {
		err = s.captureIdentityLocked(auth)
	}
	if err != nil {
		e.mu.Unlock()
		return ProtocolField{}, err
	}
	binder := newExpressionBinder(s.op.Context(), e, s.input, s.op, &StatementDescription{Parameters: formal})
	binder.queryAuth = &auth
	args := make([]parser.Expr, len(usages))
	for i := range args {
		args[i] = parser.ParameterExpr{Number: i + 1}
	}
	node, err := binder.bindFunctionProvider(parser.FunctionExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: ns.Name()}, {Text: d.name}}}, Args: args})
	if err == nil {
		selected := boundExpressionInfo(node).routineReference
		if selected == nil || selected.RoutineOID() != oid {
			err = sqlError("0A000", fmt.Sprintf("function \"%s\" is not executable via fastpath interface", d.name))
		}
	}
	if err != nil {
		e.mu.Unlock()
		return ProtocolField{}, err
	}
	s.routineAuth, s.routineDependencies = auth, binder.dependencies
	s.input = binder.input
	s.domainInputCatalog = auth.catalog
	evaluation := EvalContext{Context: s.op.Context(), Parameters: make([]sqltype.Value, len(usages)), Input: s.input, Output: s.output, statementScope: s, catalog: auth.catalog}
	evaluation.Input.DomainChecks = &evaluation
	e.mu.Unlock()
	formats, err := protocolFormats(input)
	if err != nil {
		return ProtocolField{}, err
	}
	count, err := input.Uint16()
	if err != nil {
		return ProtocolField{}, err
	}
	if int(count) != formal.Count {
		return ProtocolField{}, sqlError("08P01", fmt.Sprintf("function call message contains %d arguments but function requires %d", count, formal.Count))
	}
	if len(formats) > 1 && len(formats) != int(count) {
		return ProtocolField{}, sqlError("08P01", fmt.Sprintf("function call message contains %d argument formats but %d arguments", len(formats), count))
	}
	var log ProtocolContext
	if err = s.protocolImport(input, formats, formal, evaluation.Parameters, evaluation.Input, &log, protocolFunctionInput); err != nil {
		var diagnostic *sqlstate.Error
		if errors.As(err, &diagnostic) && diagnostic.Code == "22P03" {
			copy := *diagnostic
			copy.Message = fmt.Sprintf("incorrect binary data format in function argument %d", len(log.Parameters))
			err = &copy
		}
		return ProtocolField{}, err
	}
	format, err := input.Uint16()
	if err != nil {
		return ProtocolField{}, err
	}
	if err = input.End(); err != nil {
		return ProtocolField{}, err
	}
	node, _, err = preflightCoercionsWithPolicy(&evaluation, node, true)
	if err != nil {
		return ProtocolField{}, err
	}
	value, err := node.Eval(&evaluation)
	if err != nil {
		return ProtocolField{}, err
	}
	if value.IsNull() {
		return ProtocolField{Null: true}, nil
	}
	if format != 0 && format != 1 {
		return ProtocolField{}, sqlError("22023", fmt.Sprintf("unsupported format code: %d", int16(format)))
	}
	rows := newFinalRows(nil, 1)
	rows.values = []sqltype.Value{value}
	rows.output = s.output
	if err = s.captureSQLCursorOutput(rows, auth.catalog); err != nil {
		return ProtocolField{}, err
	}
	output := rows.output
	output.Regclass, output.Regtype, output.Regproc, output.Regrole = rows, rows, rows, rows
	output.Operator, output.Regcollation, output.Regnamespace, output.RegTS = rows, rows, rows, rows
	output.ACLItem = rows
	fields, _, err := encodeProtocolRow(s.op.Context(), rows.values, []int16{int16(format)}, output, nil, nil)
	if err != nil {
		return ProtocolField{}, err
	}
	return fields[0], nil
}
