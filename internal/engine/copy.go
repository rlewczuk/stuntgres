package engine

import (
	"errors"
	"fmt"
	"io"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

func copyTargets(relation *relationDescriptor, columns []parser.Identifier) ([]int, error) {
	targets := make([]int, 0, len(relation.columns))
	if len(columns) == 0 {
		for i, attribute := range relation.attributes {
			if _, generated := relation.generations[attribute]; !generated {
				targets = append(targets, i)
			}
		}
		return targets, nil
	}
	for _, name := range columns {
		target := -1
		for i, c := range relation.columns {
			if c.Name == name.Text {
				target = i
				break
			}
		}
		if target < 0 {
			return nil, sqlError("42703", fmt.Sprintf("column \"%s\" of relation \"%s\" does not exist", name.Text, relation.name))
		}
		for _, prior := range targets {
			if prior == target {
				return nil, sqlError("42701", fmt.Sprintf("column \"%s\" specified more than once", name.Text))
			}
		}
		if _, generated := relation.generations[relation.attributes[target]]; generated {
			d := sqlError("42P10", fmt.Sprintf("column \"%s\" is a generated column", name.Text)).(*sqlstate.Error)
			d.Detail = "Generated columns cannot be used in COPY."
			return nil, d
		}
		targets = append(targets, target)
	}
	return targets, nil
}

func (s *StatementScope) Copy(syntax *parser.Copy, source io.Reader, destination io.Writer) (count int64, err error) {
	if err = s.gate(syntax); err != nil {
		return
	}
	var options copyOptions
	if syntax.Query != nil {
		options, err = parseCopyOptions(syntax)
		if err != nil {
			return 0, err
		}
	}
	if err = s.snapshotPhase(syntax); err != nil {
		return
	}
	if err = s.acquireRelation(syntax, true); err != nil {
		return
	}
	if syntax.Query != nil {
		result, executeErr := s.op.session.Engine().executeStatement(s, syntax.Query, 0, nil)
		if executeErr != nil {
			return 0, executeErr
		}
		if result.Rows == nil {
			return 0, errors.New("stuntgres: COPY query has no row result")
		}
		defer func() {
			closeErr := result.Rows.Close()
			if err == nil {
				err = closeErr
			}
		}()
		if err = options.bindForces(nil, nil, result.Rows.Schema()); err != nil {
			return 0, err
		}
		if err = s.reserveCopy(); err != nil {
			return 0, err
		}
		return s.copyOutput(result.Rows, options, destination)
	}
	e := s.op.session.Engine()
	e.mu.Lock()
	b, auth, bindErr := e.bindCopyLocked(s, syntax)
	e.mu.Unlock()
	if bindErr != nil {
		return 0, bindErr
	}
	// Table COPY opens the target, validates columns and checks privileges
	// before BeginCopy processes options. Query COPY processes options first.
	if syntax.From {
		s.owner.mu.Lock()
		readOnly := s.owner.readOnly
		s.owner.mu.Unlock()
		if readOnly && !temporaryDataTarget(auth, &b, syntax) {
			return 0, sqlError("25006", "cannot execute COPY FROM in a read-only transaction")
		}
	} else if err = copyRelationKind(b.relation, false); err != nil {
		return 0, err
	}
	options, err = parseCopyOptions(syntax)
	if err != nil {
		return 0, err
	}
	if err = options.bindForces(b.relation, b.targets, nil); err != nil {
		return 0, err
	}
	if !syntax.From {
		rows, rowErr := s.copyTableRows(b, auth)
		if rowErr != nil {
			return 0, rowErr
		}
		defer func() {
			closeErr := rows.Close()
			if err == nil {
				err = closeErr
			}
		}()
		if err = s.reserveCopy(); err != nil {
			return 0, err
		}
		return s.copyOutput(rows, options, destination)
	}
	e.mu.Lock()
	if err = s.validateLockedRelation(e); err == nil {
		err = e.initCopyInputLocked(s, &b, auth)
	}
	e.mu.Unlock()
	if err != nil {
		return 0, err
	}
	evaluation := EvalContext{Context: s.op.Context(), Input: s.input, Output: s.output, statementScope: s}
	selected := make([]bool, len(b.relation.columns))
	for _, target := range b.targets {
		selected[target] = true
	}
	for i, expression := range b.defaults {
		if expression == nil {
			continue
		}
		if selected[i] && !options.hasDefault {
			b.defaults[i] = nil
			continue
		}
		folded, _, planErr := preflightCoercionsWithPolicy(&evaluation, expression, true)
		if planErr != nil {
			return 0, planErr
		}
		b.defaults[i] = folded
		if constant, ok := folded.(literalExpression); ok && constant.value.IsNull() {
			b.defaults[i] = nil
		}
	}
	if err = copyRelationKind(b.relation, true); err != nil {
		return 0, err
	}
	e.mu.Lock()
	if err = s.validateLockedRelation(e); err == nil {
		auth, err = e.statementAuthorizationLocked(s.op, syntax)
	}
	if err == nil {
		err = auth.checkColumns(b, authorization.InsertPrivilege, b.targets)
	}
	if err == nil {
		err = e.visibilityWriteLocked(s.txRef)
	}
	if err == nil {
		err = e.captureCheckCommentSnapshotLocked(s, b.relation)
	}
	if err == nil {
		err = s.capturePartitionInsertLocked(e, auth, b.relation)
	}
	schema := ""
	if err == nil {
		schema = auth.catalog.state.descriptors[b.relation.namespace].Name()
		e.markTemporaryAccessLocked(s, b.ref)
	}
	e.mu.Unlock()
	if err != nil {
		return
	}
	unique, err := s.bindUniqueWrite(b.ref)
	if err != nil {
		return 0, err
	}
	if err = s.reserveCopy(); err != nil {
		return
	}
	var cursor interface {
		RowCursor
		rowDefaults() []bool
		diagnostic(error) error
	}
	if options.format == copyFormatBinary {
		cursor = newCopyBinaryCursor(s, b, source)
	} else {
		cursor = newCopyCursor(s, b, options, source)
	}
	rows, err := assembleInsertRows(s.op.Context(), b, cursor, -1, schema, s.input, s.output, s, len(unique.keys) != 0, nil, nil)
	if err != nil {
		return 0, cursor.diagnostic(err)
	}
	n, err := s.applyInsertValidated(b.ref, rows)
	if err != nil {
		return 0, cursor.diagnostic(err)
	}
	return int64(n), nil
}

func (e *Engine) bindCopyLocked(s *StatementScope, syntax *parser.Copy) (b binding, auth AuthorizationView, err error) {
	if err = s.refreshRelationGrantsLocked(e); err != nil {
		return
	}
	auth, err = e.statementAuthorizationLocked(s.op, syntax)
	if err != nil {
		return
	}
	b.ref = s.lockedRef
	b.relation, _ = auth.catalog.state.descriptors[b.ref.Address].(*relationDescriptor)
	if b.relation == nil {
		err = ErrCatalogReplaced
		return
	}
	b.storage = e.authorizationStorageLocked(auth, b.ref.Address)
	b.targets, err = copyTargets(b.relation, syntax.Columns)
	if err != nil {
		return
	}
	privilege := authorization.SelectPrivilege
	if syntax.From {
		privilege = authorization.InsertPrivilege
	}
	err = auth.checkColumns(b, privilege, b.targets)
	return
}

func copyRelationKind(relation *relationDescriptor, from bool) error {
	if !from && relation.relationKind == materializedViewRelation && !relation.populated {
		d := sqlError("0A000", fmt.Sprintf("cannot copy from unpopulated materialized view \"%s\"", relation.name)).(*sqlstate.Error)
		d.Hint = "Use the REFRESH MATERIALIZED VIEW command."
		return d
	}
	if relation.relationKind != tableRelation && !(from && relation.relationKind == partitionedRelation) && !(!from && relation.relationKind == materializedViewRelation) {
		kind := "non-table relation"
		switch relation.relationKind {
		case viewRelation:
			kind = "view"
		case partitionedRelation:
			kind = "partitioned table"
		case materializedViewRelation:
			kind = "materialized view"
		case sequenceRelation:
			kind = "sequence"
		}
		direction := "from"
		if from {
			direction = "to"
		}
		d := sqlError("42809", fmt.Sprintf("cannot copy %s %s \"%s\"", direction, kind, relation.name)).(*sqlstate.Error)
		if !from && (relation.relationKind == viewRelation || relation.relationKind == partitionedRelation) {
			d.Hint = "Try the COPY (SELECT ...) TO variant."
		}
		if from && relation.relationKind == viewRelation {
			d.Hint = "To enable copying to a view, provide an INSTEAD OF INSERT trigger."
		}
		return d
	}
	return nil
}

func (e *Engine) initCopyInputLocked(s *StatementScope, b *binding, auth AuthorizationView) error {
	binder := newExpressionBinder(s.op.Context(), e, s.input, s.op, nil)
	binder.queryAuth = &auth
	binder.insertTarget = b.relation
	e.bindInsertDefaults(b, auth, parser.OverrideSystem)
	if err := binder.captureRetainedWriteInput(b.defaults, b.relation.generations, b.relation.checkConstraints); err != nil {
		return err
	}
	s.input = binder.input
	for _, target := range b.targets {
		var err error
		usage := b.relation.columns[target].Usage
		if usage.Type.ContainsRegclass() && s.input.Regclass == nil {
			s.input.Regclass, err = e.captureRegclassInput(auth)
		}
		if err == nil && (usage.Type.ContainsRegtype() || usage.Type.ContainsRegproc()) && s.input.Regtype == nil {
			s.input.Regtype, err = e.captureRegtypeInput(auth, s.op)
		}
		if err == nil && usage.Type.ContainsRegrole() && s.input.Regrole == nil {
			s.input.Regrole = capturedRegroleInput{roles: auth.roles}
		}
		if err == nil && usage.Type.ContainsOperatorAlias() && s.input.Operator == nil {
			s.input.Operator, err = e.captureOperatorInput(auth, s.op)
		}
		if err == nil && usage.Type.ContainsRegproc() && s.input.Regproc == nil {
			s.input.Regproc, err = e.captureRoutineInput(auth, s.input.Regtype.(capturedRegtypeInput))
		}
		if err == nil && usage.Type.ContainsRegObject() && (s.input.Regcollation == nil || s.input.Regnamespace == nil) {
			var input capturedRegObjectInput
			input, err = e.captureRegObjectInput(auth)
			if err == nil {
				s.input.Regcollation, s.input.Regnamespace = input, input
			}
		}
		if err == nil && usage.Type.ContainsRegTS() && s.input.RegTS == nil {
			s.input.RegTS, err = e.captureRegTSInput(auth)
		}
		if err == nil {
			err = binder.captureTypeDiagnosticInput(usage.Type)
		}
		if err != nil {
			return err
		}
	}
	if binder.input.ACLItem != nil {
		s.input.ACLItem = binder.input.ACLItem
	}
	if binder.input.TypeDiagnosticName != nil {
		s.input.TypeDiagnosticName = binder.input.TypeDiagnosticName
	}
	return nil
}
func (s *StatementScope) copyTableRows(b binding, auth AuthorizationView) (*finalRows, error) {
	e := s.op.session.Engine()
	e.mu.Lock()
	view, err := s.acquireDataViewLocked(e, b.ref)
	if err == nil {
		e.markTemporaryAccessLocked(s, b.ref)
	}
	e.mu.Unlock()
	if err != nil {
		return nil, err
	}
	source, err := captureQueryPrefix(view, b.ref)
	if err != nil {
		return nil, err
	}
	source.borrowAuthority(s)
	if err = selectQuerySources(s.op.Context(), source); err != nil {
		return nil, err
	}
	schema := make([]ColumnDescriptor, len(b.targets))
	for i, target := range b.targets {
		schema[i] = b.relation.columns[target]
	}
	rows := newFinalRows(schema, source.count())
	rows.source = source
	rows.slots = b.targets
	rows.output = s.output
	if err = e.captureRegclassOutput(s.op, rows); err != nil {
		rows.Close()
		return nil, err
	}
	return rows, nil
}
