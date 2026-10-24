package engine

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/regobj"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func (s *StatementScope) CallProtocolFunction(input session.ProtocolInput) (session.ProtocolField, error) {
	if err := s.active(); err != nil {
		return session.ProtocolField{}, err
	}
	t := s.owner
	t.Mu.Lock()
	if t.State == session.TxAborted {
		t.Mu.Unlock()
		return session.ProtocolField{}, session.AbortedError()
	}
	if c := t.ProtocolCycle; c != nil {
		c.Active = c.Executed
	}
	err := s.admitTransactionLocked(s.engine())
	t.Mu.Unlock()
	if err != nil {
		return session.ProtocolField{}, err
	}
	s.worked = true
	if err = s.snapshotDataPhase(); err != nil {
		return session.ProtocolField{}, err
	}
	rawOID, err := input.Int32()
	if err != nil {
		return session.ProtocolField{}, err
	}
	oid := uint32(rawOID)
	e := s.engine()
	e.mu.Lock()
	auth, err := e.authorizationLocked(s.op)
	if err != nil {
		e.mu.Unlock()
		return session.ProtocolField{}, err
	}
	d, ok := auth.Catalog.Procedure(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: ids.OID(oid)})
	if !ok {
		identity, found := regobj.RoutineIdentityByOID(auth.Catalog, ids.OID(oid))
		e.mu.Unlock()
		if found {
			return session.ProtocolField{}, sqlError("0A000", fmt.Sprintf("function \"%s\" is not supported", identity.Name))
		}
		return session.ProtocolField{}, sqlError("42883", fmt.Sprintf("function with OID %d does not exist", oid))
	}
	if d.RoutineKind != catalog.FunctionRoutine || procedureReturnsSet(d) {
		e.mu.Unlock()
		return session.ProtocolField{}, sqlError("0A000", fmt.Sprintf("cannot call function \"%s\" via fastpath interface", d.Name()))
	}
	ns := auth.Catalog.Get(d.Namespace())
	nsRef := auth.Catalog.Owner().Domain.Ref(auth.Catalog.Epoch(), d.Namespace())
	ref := auth.Catalog.Owner().Domain.Ref(auth.Catalog.Epoch(), d.Address())
	if err = auth.Check(access.Requirement{Object: nsRef, Privileges: authorization.UsagePrivilege}); err == nil {
		err = auth.Check(access.Requirement{Object: ref, Privileges: authorization.ExecutePrivilege})
	}
	if err != nil {
		e.mu.Unlock()
		return session.ProtocolField{}, err
	}
	var usages []sqltype.Usage
	if d.Signature != "" {
		for _, word := range strings.Split(string(d.Signature), ",") {
			id, parseErr := strconv.ParseUint(word, 10, 32)
			if parseErr != nil {
				e.mu.Unlock()
				return session.ProtocolField{}, parseErr
			}
			typ, found := auth.Catalog.LookupType(sqltype.ID(id))
			if !found {
				e.mu.Unlock()
				return session.ProtocolField{}, sqlError("0A000", "function argument type is unavailable")
			}
			usages = append(usages, typ.Usage())
		}
	}
	formal := analyze.ParameterInfo{Count: len(usages), Types: usages}
	if err = s.captureFormalInputLocked(auth, usages); err == nil {
		err = e.captureCommentSnapshotLocked(s)
	}
	if err == nil {
		err = s.captureIdentityLocked(auth)
	}
	if err != nil {
		e.mu.Unlock()
		return session.ProtocolField{}, err
	}
	binder := newExpressionBinder(s.op.Context(), e, s.input, s.op, &StatementDescription{Parameters: formal})
	binder.Query.Auth = &auth
	args := make([]parser.Expr, len(usages))
	for i := range args {
		args[i] = parser.ParameterExpr{Number: i + 1}
	}
	node, err := binder.BindFunctionProvider(parser.FunctionExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: ns.Name()}, {Text: d.Name()}}}, Args: args})
	if err == nil {
		selected := expr.InfoOf(node).RoutineReference
		if selected == nil || selected.RoutineOID() != oid {
			err = sqlError("0A000", fmt.Sprintf("function \"%s\" is not executable via fastpath interface", d.Name()))
		}
	}
	if err != nil {
		e.mu.Unlock()
		return session.ProtocolField{}, err
	}
	s.routineAuth, s.routineDependencies = auth, binder.Dependencies
	s.input = binder.Input
	s.domains.inputCatalog = auth.Catalog
	evaluation := exec.Context{EvalStatement: exec.EvalStatement{Context: s.op.Context(), Parameters: make([]sqltype.Value, len(usages)), Input: s.input, Output: s.output, Host: hostOf(s), Catalog: auth.Catalog}}
	evaluation.Input.DomainChecks = &evaluation
	e.mu.Unlock()
	formats, err := session.ProtocolFormats(input)
	if err != nil {
		return session.ProtocolField{}, err
	}
	count, err := input.Uint16()
	if err != nil {
		return session.ProtocolField{}, err
	}
	if int(count) != formal.Count {
		return session.ProtocolField{}, sqlError("08P01", fmt.Sprintf("function call message contains %d arguments but function requires %d", count, formal.Count))
	}
	if len(formats) > 1 && len(formats) != int(count) {
		return session.ProtocolField{}, sqlError("08P01", fmt.Sprintf("function call message contains %d argument formats but %d arguments", len(formats), count))
	}
	var log session.ProtocolContext
	if err = session.ImportProtocolParameters(s.op.Context(), s.output, input, formats, formal.Count, formal.Types, evaluation.Parameters, evaluation.Input, &log, session.ProtocolFunctionInput); err != nil {
		var diagnostic *sqlstate.Error
		if errors.As(err, &diagnostic) && diagnostic.Code == "22P03" {
			copy := *diagnostic
			copy.Message = fmt.Sprintf("incorrect binary data format in function argument %d", len(log.Parameters))
			err = &copy
		}
		return session.ProtocolField{}, err
	}
	format, err := input.Uint16()
	if err != nil {
		return session.ProtocolField{}, err
	}
	if err = input.End(); err != nil {
		return session.ProtocolField{}, err
	}
	node, _, err = exec.PreflightCoercionsWithPolicy(&evaluation, node, true)
	if err != nil {
		return session.ProtocolField{}, err
	}
	value, err := node.Eval(&evaluation)
	if err != nil {
		return session.ProtocolField{}, err
	}
	if value.IsNull() {
		return session.ProtocolField{Null: true}, nil
	}
	if format != 0 && format != 1 {
		return session.ProtocolField{}, sqlError("22023", fmt.Sprintf("unsupported format code: %d", int16(format)))
	}
	rows := exec.NewFinalRows(nil, 1)
	rows.Values = []sqltype.Value{value}
	rows.Output = s.output
	if err = s.captureSQLCursorOutput(rows, auth.Catalog); err != nil {
		return session.ProtocolField{}, err
	}
	output := rows.Output
	output.Regclass, output.Regtype, output.Regproc, output.Regrole = rows, rows, rows, rows
	output.Operator, output.Regcollation, output.Regnamespace, output.RegTS = rows, rows, rows, rows
	output.ACLItem = rows
	fields, _, err := session.EncodeProtocolRow(s.op.Context(), rows.Values, []int16{int16(format)}, output, nil, nil)
	if err != nil {
		return session.ProtocolField{}, err
	}
	return fields[0], nil
}
