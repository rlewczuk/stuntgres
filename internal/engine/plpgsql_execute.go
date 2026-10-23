package engine

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/proc"
	"stuntgres/internal/engine/regobj"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// All three finite handler routes reach this executor/compiler, not a scalar
// pseudo-interface. The caller owns routine admission, depth and root operation.
func (s *StatementScope) invokePLRoutine(d *catalog.Procedure, input *proc.Frame, read catalog.Read, collation sqltype.CollationRef) (sqltype.Value, error) {
	if d.SQL.PL == nil {
		return sqltype.Value{}, fmt.Errorf("stuntgres: missing checked PL program")
	}
	if collation.ID != 0 && !collation.Valid() {
		return sqltype.Value{}, fmt.Errorf("stuntgres: invalid PL input collation")
	}
	f := proc.NewPLFrame(d.SQL.PL, d.SQL, input, collation)
	defer f.Close()
	result, returned, err := s.executePLBlock(d, f, d.SQL.PL.Root, read)
	if err != nil {
		return sqltype.Value{}, err
	}
	if d.SQL.PL.Procedure {
		return sqltype.Value{}, nil
	}
	if len(d.SQL.OutputFormals) == 1 {
		return f.Values[d.SQL.OutputFormals[0]], nil
	}
	if d.SQL.Void() {
		return sqltype.VoidValue(d.SQL.Result.Type), nil
	}
	if !returned {
		diagnostic := sqlError("2F005", "control reached end of function without RETURN").(*sqlstate.Error)
		diagnostic.Context = "PL/pgSQL function " + plRoutineDiagnosticIdentity(read, d)
		return sqltype.Value{}, diagnostic
	}
	return result, nil
}

func (s *StatementScope) executePLBlock(d *catalog.Procedure, f *proc.PLFrame, block *proc.PLBlock, read catalog.Read) (sqltype.Value, bool, error) {
	f.Enter(block)
	defer f.Leave(block)
	for _, initializer := range block.Declarations {
		if err := s.op.Context().Err(); err != nil {
			return sqltype.Value{}, false, err
		}
		variable := f.Program.Variables[initializer.Slot]
		f.Values[initializer.Slot] = sqltype.Value{}
		if variable.Record && initializer.Expression == nil {
			continue
		}
		value := sqltype.Value{}
		var err error
		if initializer.Expression != nil {
			f.Scopes[len(f.Scopes)-1] = initializer.Scope
			value, err = s.evaluatePLExpression(f, initializer.Query, read)
			f.Scopes[len(f.Scopes)-1] = block
		} else {
			value = sqltype.Null(variable.Usage.Type.BaseType())
		}
		if err == nil {
			value, err = s.assignPLVariable(f, proc.PLTarget{Slot: initializer.Slot}, value)
		}
		if err != nil {
			return sqltype.Value{}, false, plRuntimeError(err, read, d, initializer.Expression, "during statement block local variable initialization", false)
		}
	}
	for _, instruction := range block.Statements {
		if err := s.op.Context().Err(); err != nil {
			return sqltype.Value{}, false, err
		}
		if instruction.Block != nil {
			value, returned, err := s.executePLBlock(d, f, instruction.Block, read)
			if err != nil || returned {
				return value, returned, err
			}
			continue
		}
		statement := instruction.Syntax
		if statement.Return {
			if statement.Expression == nil {
				return sqltype.Value{}, true, nil
			}
			value, err := s.evaluatePLExpression(f, instruction.Query, read)
			expressionError := err != nil
			if err == nil {
				value, err = s.convertPLValue(value, d.SQL.Result)
			}
			if err != nil {
				return sqltype.Value{}, false, plRuntimeError(err, read, d, statement.Expression, "at RETURN", expressionError)
			}
			return value, true, nil
		}
		value, err := s.evaluatePLExpression(f, instruction.Query, read)
		expressionError := err != nil
		if err == nil {
			value, err = s.assignPLVariable(f, instruction.Target, value)
		}
		if err != nil {
			var diagnostic *sqlstate.Error
			var domainError *plCompositeDomainError
			if !errors.As(err, &domainError) && errors.As(err, &diagnostic) && strings.HasPrefix(diagnostic.Context, "SQL function ") {
				copy := *diagnostic
				start := statement.Pos.Offset
				end := parser.ExpressionStart(statement.Expression).Offset + len(parser.PLExpressionSource(d.SQL.Source, statement.Expression))
				copy.Context += "\nPL/pgSQL assignment \"" + strings.TrimSpace(d.SQL.Source[start:end]) + "\""
				err = &copy
			}
			return sqltype.Value{}, false, plRuntimeError(err, read, d, statement.Expression, "at assignment", expressionError)
		}
	}
	return sqltype.Value{}, false, nil
}

func (s *StatementScope) evaluatePLExpression(f *proc.PLFrame, statement *parser.Select, read catalog.Read) (value sqltype.Value, err error) {
	restore := s.activateSQLRoutine(nil, read)
	defer func() { err = restore(err) }()
	s.sqlRoutine.lexical = f
	s.sqlRoutine.scalar = false
	if err = s.acquireRelation(statement, true); err != nil {
		return value, err
	}
	result, err := s.engine().executeStatement(s, statement, f.Program.FormalCount, func(_ context.Context, _ analyze.ParameterInfo, dst []sqltype.Value, _ sqltype.InputEnvironment) error {
		copy(dst, f.Values[:f.Program.FormalCount])
		return nil
	})
	if err != nil {
		return value, err
	}
	if result.Rows == nil {
		return value, fmt.Errorf("stuntgres: PL expression has no output")
	}
	defer func() {
		if closeErr := result.Rows.Close(); err == nil {
			err = closeErr
		}
	}()
	for _, rows := range result.RowSets {
		defer rows.Close()
	}
	schema := result.Rows.Schema()
	if len(schema) != 1 {
		return value, sqlError("42601", "query returned more than one column")
	}
	row := make([]sqltype.Value, 1)
	present, err := result.Rows.Next(s.op.Context(), row)
	if err != nil {
		return value, err
	}
	if !present {
		return sqltype.Null(schema[0].Usage.Type), nil
	}
	value = row[0]
	present, err = result.Rows.Next(s.op.Context(), row)
	if err != nil {
		return value, err
	}
	if present {
		return sqltype.Value{}, sqlError("21000", "query returned more than one row")
	}
	return value, nil
}

func (s *StatementScope) convertPLValue(value sqltype.Value, target sqltype.Usage) (sqltype.Value, error) {
	if !value.IsNull() && value.Type() == target.Type && target.Typmod < 0 && !target.Type.IsDomain() {
		return value, nil
	}
	if target.Type.BaseType().IsComposite() {
		shape := target.Type.BaseType()
		if value.IsNull() {
			return s.finishPLComposite(sqltype.Null(shape), target.Type)
		}
		if !value.Type().BaseType().IsComposite() {
			return sqltype.Value{}, sqlError("42804", "cannot assign non-composite value to a row variable")
		}
		fields := make([]sqltype.Value, shape.CompositeLen())
		for i := range fields {
			source := sqltype.Null(shape.CompositeMember(i).Usage.Type.BaseType())
			if i < value.CompositeLen() {
				source = value.CompositeField(i)
			}
			converted, err := s.convertPLValue(source, shape.CompositeMember(i).Usage)
			if err != nil {
				return sqltype.Value{}, err
			}
			fields[i] = converted
		}
		candidate, err := sqltype.OwnedCompositeValue(shape, fields)
		if err != nil {
			return sqltype.Value{}, err
		}
		return s.finishPLComposite(candidate, target.Type)
	}
	if value.IsNull() {
		value = sqltype.Null(target.Type.BaseType())
	}
	e := s.engine()
	e.mu.Lock()
	auth, err := e.authorizationLocked(s.op)
	if err != nil {
		e.mu.Unlock()
		return sqltype.Value{}, err
	}
	binder := newExpressionBinder(s.op.Context(), e, s.input, s.op, nil)
	binder.Query.Auth = &auth
	node, selected, err := binder.PLAssignment(value, target)
	e.mu.Unlock()
	if err != nil {
		return sqltype.Value{}, err
	}
	evaluation := exec.Context{EvalStatement: exec.EvalStatement{Context: s.op.Context(), Host: hostOf(s), Catalog: auth.Catalog, Input: binder.Input, Output: s.output}}
	evaluation.Input.DomainChecks = &evaluation
	if selected {
		return node.Eval(&evaluation)
	}
	text, err := value.Type().OutputWithEnvironment(s.op.Context(), value, s.output)
	if err != nil {
		return sqltype.Value{}, err
	}
	return target.Type.InputWithEnvironment(s.op.Context(), text, target.Typmod, evaluation.Input)
}

// A CHECK exception is not an exception from the assignment's selected cast.
// Preserve that producer through field reconstruction and diagnostic decoration.
type plCompositeDomainError struct{ error }

func (e *plCompositeDomainError) Unwrap() error { return e.error }

// The candidate has the physical base descriptor. Validate fresh reconstructed
// input before attaching a domain tag or publishing any activation slot.
func (s *StatementScope) finishPLComposite(candidate sqltype.Value, target *sqltype.Descriptor) (sqltype.Value, error) {
	if !target.IsDomain() {
		return candidate, nil
	}
	if target.HasDomainConstraints() {
		e := s.engine()
		e.mu.Lock()
		auth, err := e.authorizationLocked(s.op)
		if err != nil {
			e.mu.Unlock()
			return sqltype.Value{}, err
		}
		binder := newExpressionBinder(s.op.Context(), e, s.input, s.op, nil)
		binder.Query.Auth = &auth
		err = binder.CaptureTypeDiagnosticInput(target)
		e.mu.Unlock()
		if err != nil {
			return sqltype.Value{}, err
		}
		evaluation := exec.Context{EvalStatement: exec.EvalStatement{Context: s.op.Context(), Host: hostOf(s), Catalog: auth.Catalog, Input: binder.Input, Output: s.output}}
		evaluation.Input.DomainChecks = &evaluation
		if err := target.CheckDomainInput(s.op.Context(), candidate, evaluation.Input); err != nil {
			return sqltype.Value{}, &plCompositeDomainError{err}
		}
	}
	return target.Wrap(candidate)
}

func (s *StatementScope) assignPLVariable(f *proc.PLFrame, target proc.PLTarget, value sqltype.Value) (sqltype.Value, error) {
	variable := f.Program.Variables[target.Slot]
	if len(target.Fields) > 0 {
		current := f.Values[target.Slot]
		if current.Type() == nil {
			d := sqlError("55000", fmt.Sprintf("record %q is not assigned yet", variable.Name)).(*sqlstate.Error)
			d.Detail = "The tuple structure of a not-yet-assigned record is indeterminate."
			return sqltype.Value{}, d
		}
		updated, err := s.assignPLField(current, target.Fields, value, variable.Name)
		if err != nil {
			return sqltype.Value{}, err
		}
		f.Values[target.Slot] = updated
		return updated, nil
	}
	var err error
	if variable.Record {
		if value.IsNull() {
			value = sqltype.Value{}
		} else if value.Type() == nil || !value.Type().BaseType().IsComposite() {
			return sqltype.Value{}, sqlError("0A000", "input of anonymous composite types is not implemented")
		}
	} else {
		value, err = s.convertPLValue(value, f.Usage(target.Slot))
	}
	if err != nil {
		return sqltype.Value{}, err
	}
	if variable.NotNull && (value.Type() == nil || value.IsNull()) {
		return sqltype.Value{}, sqlError("22004", fmt.Sprintf("null value cannot be assigned to variable %q declared NOT NULL", variable.Name))
	}
	f.Values[target.Slot] = value
	return value, nil
}
func (s *StatementScope) assignPLField(current sqltype.Value, path []string, value sqltype.Value, name string) (sqltype.Value, error) {
	shape := current.Type().BaseType()
	index := -1
	for i := range shape.CompositeLen() {
		if shape.CompositeMember(i).Name == path[0] {
			index = i
			break
		}
	}
	if index < 0 {
		return sqltype.Value{}, sqlError("42703", fmt.Sprintf("record %q has no field %q", name, path[0]))
	}
	fields := make([]sqltype.Value, shape.CompositeLen())
	for i := range fields {
		fields[i] = sqltype.Null(shape.CompositeMember(i).Usage.Type)
		if !current.IsNull() {
			fields[i] = current.CompositeField(i)
		}
	}
	var err error
	if len(path) == 1 {
		fields[index], err = s.convertPLValue(value, shape.CompositeMember(index).Usage)
	} else {
		fields[index], err = s.assignPLField(fields[index], path[1:], value, name)
	}
	if err != nil {
		return sqltype.Value{}, err
	}
	candidate, err := sqltype.OwnedCompositeValue(shape, fields)
	if err != nil {
		return sqltype.Value{}, err
	}
	return s.finishPLComposite(candidate, current.Type())
}

func plRoutineDiagnosticIdentity(read catalog.Read, d *catalog.Procedure) string {
	var text strings.Builder
	text.WriteString(catalog.DependencyIdentifier(d.Name()))
	text.WriteByte('(')
	regobj.WriteRoutineArguments(&text, read, nil, d.Signature, true)
	text.WriteByte(')')
	return text.String()
}

func plRuntimeError(err error, read catalog.Read, d *catalog.Procedure, raw parser.Expr, action string, expression bool) error {
	var original *sqlstate.Error
	if !errors.As(err, &original) {
		return err
	}
	copy := *original
	source := parser.PLExpressionSource(d.SQL.Source, raw)
	offset := 0
	characterOffset := 0
	if raw != nil {
		start := parser.ExpressionStart(raw)
		offset, characterOffset = start.Offset, start.Character-1
	}
	if expression && copy.Position != "" {
		if n, e := strconv.Atoi(copy.Position); e == nil {
			copy.InternalPosition = strconv.Itoa(n - characterOffset)
			copy.InternalQuery = source
			copy.Position = ""
		}
	}
	if expression && copy.Context == "" && (copy.Code == "55000" || copy.Code == "42703" || copy.Code == "22012" || copy.Code == "21000") {
		copy.Context = "PL/pgSQL expression \"" + source + "\""
	}
	line := strings.Count(d.SQL.Source[:offset], "\n") + 1
	context := fmt.Sprintf("PL/pgSQL function %s line %d %s", plRoutineDiagnosticIdentity(read, d), line, action)
	if copy.Context != "" {
		copy.Context += "\n"
	}
	copy.Context += context
	return &copy
}
