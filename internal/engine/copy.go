package engine

import (
	"errors"
	"fmt"
	"io"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

func copyTargets(relation *catalog.Relation, columns []parser.Identifier) ([]int, error) {
	targets := make([]int, 0, len(relation.Columns))
	if len(columns) == 0 {
		for i, attribute := range relation.Attributes {
			if _, generated := relation.ColumnMeta.Generations[attribute]; !generated {
				targets = append(targets, i)
			}
		}
		return targets, nil
	}
	for _, name := range columns {
		target := -1
		for i, c := range relation.Columns {
			if c.Name == name.Text {
				target = i
				break
			}
		}
		if target < 0 {
			return nil, sqlError("42703", fmt.Sprintf("column \"%s\" of relation \"%s\" does not exist", name.Text, relation.Name()))
		}
		for _, prior := range targets {
			if prior == target {
				return nil, sqlError("42701", fmt.Sprintf("column \"%s\" specified more than once", name.Text))
			}
		}
		if _, generated := relation.ColumnMeta.Generations[relation.Attributes[target]]; generated {
			d := sqlError("42P10", fmt.Sprintf("column \"%s\" is a generated column", name.Text)).(*sqlstate.Error)
			d.Detail = "Generated columns cannot be used in COPY."
			return nil, d
		}
		targets = append(targets, target)
	}
	return targets, nil
}

func (s *StatementScope) Copy(syntax *parser.Copy, source io.Reader, destination io.Writer) (count int64, err error) {
	return s.CopyProtocol(syntax, source, destination, CopyProtocolHooks{})
}

// CopyProtocol shares the native stream executor. Hooks run at reserved I/O
// boundaries, never while admission or publication locks are held.
func (s *StatementScope) CopyProtocol(syntax *parser.Copy, source io.Reader, destination io.Writer, hooks CopyProtocolHooks) (count int64, err error) {
	if err = s.gate(syntax); err != nil {
		return
	}
	if err = s.beginProtocolSimplePortal(); err != nil {
		return 0, err
	}
	if syntax.Endpoint != parser.CopyStdin && syntax.Endpoint != parser.CopyStdout {
		return 0, CopyBoundary(syntax)
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
		rows, queryErr := s.prepareCopyQueryRows(syntax.Query)
		if queryErr != nil {
			return 0, queryErr
		}
		if rows == nil {
			return 0, errors.New("stuntgres: COPY query has no row result")
		}
		defer func() {
			closeErr := rows.Close()
			if err == nil {
				err = closeErr
			}
		}()
		if err = options.bindForces(nil, nil, rows.Schema()); err != nil {
			return 0, err
		}
		if err = s.reserveCopy(); err != nil {
			return 0, err
		}
		if err = s.startProtocolCopy(hooks, false, options, len(rows.Schema())); err != nil {
			return 0, err
		}
		return s.copyOutput(rows, options, destination)
	}
	e := s.engine()
	s.input.LockedDomainInput = s
	e.mu.Lock()
	b, auth, bindErr := e.bindCopyLocked(s, syntax)
	e.mu.Unlock()
	s.input.DomainChecks = s
	s.domains.inputCatalog = auth.Catalog
	if bindErr != nil {
		return 0, bindErr
	}
	// Table COPY opens the target, validates columns and checks privileges
	// before BeginCopy processes options. Query COPY processes options first.
	if syntax.From {
		s.owner.Mu.Lock()
		readOnly := s.owner.ReadOnly
		s.owner.Mu.Unlock()
		if readOnly && !temporaryDataTarget(auth, &b, syntax) {
			return 0, sqlError("25006", "cannot execute COPY FROM in a read-only transaction")
		}
	} else if err = copyRelationKind(b.Relation, false); err != nil {
		return 0, err
	}
	if err := checkTemporaryStorageAccess(auth.Catalog, b.Relation, s.op.Session.SessionToken); err != nil {
		return 0, err
	}
	options, err = parseCopyOptions(syntax)
	if err != nil {
		return 0, err
	}
	if err = options.bindForces(b.Relation, b.Targets, nil); err != nil {
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
		if err = s.startProtocolCopy(hooks, false, options, len(rows.Schema())); err != nil {
			return 0, err
		}
		return s.copyOutput(rows, options, destination)
	}
	var foreign *foreignModification
	if b.Relation.RelationKind == catalog.ForeignRelation {
		e.mu.Lock()
		target, captureErr := e.captureForeignSourceLocked(s, b.Ref, b.Relation, authorization.PrincipalRef{}, nil, plan.RowMark{})
		e.mu.Unlock()
		if captureErr != nil {
			return 0, captureErr
		}
		foreign, err = s.planForeignModification(s.op.Context(), target.Foreign, catalog.ForeignInsert, true)
		if err != nil {
			return 0, err
		}
	}
	e.mu.Lock()
	if err = s.validateLockedRelation(e); err == nil {
		err = e.initCopyInputLocked(s, &b, auth)
	}
	e.mu.Unlock()
	if err != nil {
		return 0, err
	}
	evaluation := exec.Context{EvalStatement: exec.EvalStatement{Context: s.op.Context(), Input: s.input, Output: s.output, Host: hostOf(s), Catalog: auth.Catalog}}
	selected := make([]bool, len(b.Relation.Columns))
	for _, target := range b.Targets {
		selected[target] = true
	}
	binaryInput := options.format == copyFormatBinary && (s.protocolOrZero().command || s.protocolOrZero().simple)
	inputColumns := len(b.Insert.Defaults)
	if binaryInput {
		inputColumns = len(b.Relation.Columns)
	}
	for i := range inputColumns {
		if binaryInput {
			// The live descriptor excludes dropped attributes, but includes
			// omitted COPY targets. Resolve each receive provider before that
			// attribute's default planning and before CopyInResponse.
			if err = b.Relation.Columns[i].Usage.Type.CheckBinaryInputDeclaration(); err != nil {
				return 0, err
			}
		}
		if i >= len(b.Insert.Defaults) {
			continue
		}
		expression := b.Insert.Defaults[i]
		if expression == nil {
			continue
		}
		if selected[i] && !options.hasDefault {
			b.Insert.Defaults[i] = nil
			continue
		}
		defaultEvaluation := evaluation
		applyDefaultOrigin(bindingDefaultOrigin(&b, i), &defaultEvaluation)
		folded, _, planErr := preflightCoercionsWithPolicy(&defaultEvaluation, expression, true)
		if planErr != nil {
			return 0, planErr
		}
		if planErr = validateExpressionJoinExecution(&defaultEvaluation, folded, auth.Catalog); planErr != nil {
			return 0, planErr
		}
		b.Insert.Defaults[i] = folded
		if constant, ok := folded.(plan.LiteralExpr); ok && constant.Value.IsNull() && bindingDefaultOrigin(&b, i).Definition.Expression == nil {
			b.Insert.Defaults[i] = nil
		}
	}
	if err = copyRelationKind(b.Relation, true); err != nil {
		return 0, err
	}
	e.mu.Lock()
	if err = s.validateLockedRelation(e); err == nil {
		auth, err = e.statementAuthorizationLocked(s.op, syntax)
	}
	if err == nil {
		err = auth.CheckColumns(b.Ref, b.Relation, authorization.InsertPrivilege, b.Targets)
	}
	if err == nil {
		err = e.visibilityWriteLocked(s.txRef)
	}
	if err == nil {
		err = e.captureCheckCommentSnapshotLocked(s, b.Relation)
	}
	if err == nil && foreign == nil {
		err = s.capturePartitionInsertLocked(e, auth, b.Relation)
	}
	schema := ""
	if err == nil {
		schema = auth.Catalog.Get(b.Relation.Namespace()).Name()
		s.routineAuth = auth
		e.markTemporaryAccessLocked(s, b.Ref)
	}
	e.mu.Unlock()
	if err != nil {
		return
	}
	if err = s.readmitPreparedPrograms(auth); err != nil {
		return 0, err
	}
	unique := &uniqueWriteBinding{}
	if foreign == nil {
		unique, err = s.bindUniqueWrite(b.Ref)
		if err != nil {
			return 0, err
		}
	}
	if err = s.reserveCopy(); err != nil {
		return
	}
	var cursor interface {
		exec.RowCursor
		rowDefaults() []bool
		diagnostic(error) error
	}
	if options.format == copyFormatBinary {
		cursor = newCopyBinaryCursor(s, b, source)
	} else {
		cursor = newCopyCursor(s, b, options, source)
	}
	rows, err := func() (indexWriteBatch, error) {
		if s.protocolOrZero().command || s.protocolOrZero().simple {
			s.op.CopyInput.Active.Store(true)
			// CopyInResponse permits immediate frontend cancellation. Install
			// the first-record hold before its external start hook can send it.
			s.op.CopyInput.Decoding.Store(true)
			defer s.op.StopCopyInput()
		}
		if err := s.startProtocolCopy(hooks, true, options, len(b.Targets)); err != nil {
			return indexWriteBatch{}, err
		}
		rows, err := assembleInsertRows(s.op.Context(), b, cursor, -1, schema, s.input, s.output, s, len(unique.keys) != 0 || unique.checked, nil, nil)
		if err == nil {
			err = s.op.PendingCopyInputError()
		}
		if err != nil {
			err = cursor.diagnostic(err)
		}
		return rows, err
	}()
	if err != nil {
		return 0, err
	}
	if hooks.FinishInput != nil {
		_, err = s.copyIO(func() (int, error) { return 0, hooks.FinishInput() })
		if err != nil {
			return 0, cursor.diagnostic(err)
		}
	}
	if foreign != nil {
		// Terminal input/FinishInput acceptance precedes all provider mutation.
		if err := foreign.begin(s.op.Context(), s); err != nil {
			return 0, err
		}
		n, err := foreign.insertRows(s.op.Context(), s, rows.values, false, nil)
		if err != nil {
			return 0, cursor.diagnostic(err)
		}
		return n, nil
	}
	n, err := s.applyInsertValidated(b.Ref, rows)
	if err != nil {
		return 0, cursor.diagnostic(err)
	}
	return int64(n), nil
}

func (e *Engine) bindCopyLocked(s *StatementScope, syntax *parser.Copy) (b analyze.Binding, auth AuthorizationView, err error) {
	if err = s.refreshRelationGrantsLocked(e); err != nil {
		return
	}
	auth, err = e.statementAuthorizationLocked(s.op, syntax)
	if err != nil {
		return
	}
	s.routineAuth = auth
	b.Ref = s.lockedRef
	b.Relation, _ = auth.Catalog.Relation(b.Ref.Address)
	if b.Relation == nil {
		err = ids.ErrCatalogReplaced
		return
	}
	b.Storage = e.authorizationStorageLocked(auth, b.Ref.Address)
	b.Targets, err = copyTargets(b.Relation, syntax.Columns)
	if err != nil {
		return
	}
	privilege := authorization.SelectPrivilege
	if syntax.From {
		privilege = authorization.InsertPrivilege
	}
	err = auth.CheckColumns(b.Ref, b.Relation, privilege, b.Targets)
	return
}

func copyRelationKind(relation *catalog.Relation, from bool) error {
	if !from && relation.RelationKind == catalog.MaterializedViewRelation && !relation.Populated {
		d := sqlError("0A000", fmt.Sprintf("cannot copy from unpopulated materialized view \"%s\"", relation.Name())).(*sqlstate.Error)
		d.Hint = "Use the REFRESH MATERIALIZED VIEW command."
		return d
	}
	if relation.RelationKind != catalog.TableRelation && !(from && (relation.RelationKind == catalog.PartitionedRelation || relation.RelationKind == catalog.ForeignRelation)) && !(!from && relation.RelationKind == catalog.MaterializedViewRelation) {
		kind := "non-table relation"
		switch relation.RelationKind {
		case catalog.ViewRelation:
			kind = "view"
		case catalog.PartitionedRelation:
			kind = "partitioned table"
		case catalog.MaterializedViewRelation:
			kind = "materialized view"
		case catalog.SequenceRelation:
			kind = "sequence"
		}
		direction := "from"
		if from {
			direction = "to"
		}
		d := sqlError("42809", fmt.Sprintf("cannot copy %s %s \"%s\"", direction, kind, relation.Name())).(*sqlstate.Error)
		if !from && (relation.RelationKind == catalog.ViewRelation || relation.RelationKind == catalog.PartitionedRelation) {
			d.Hint = "Try the COPY (SELECT ...) TO variant."
		}
		if from && relation.RelationKind == catalog.ViewRelation {
			d.Hint = "To enable copying to a view, provide an INSTEAD OF INSERT trigger."
		}
		return d
	}
	return nil
}

func (e *Engine) initCopyInputLocked(s *StatementScope, b *analyze.Binding, auth AuthorizationView) error {
	binder := newExpressionBinder(s.op.Context(), e, s.input, s.op, nil)
	binder.Query.Auth = &auth
	binder.Target.InsertTarget = b.Relation
	analyze.BindInsertDefaults(e.catalog.owner(), b, auth.Catalog, parser.OverrideSystem)
	if err := binder.CaptureRetainedWriteInput(b.Insert.Defaults, b.Relation.ColumnMeta.Generations, b.Relation.Constraints.Check); err != nil {
		return err
	}
	s.input = binder.Input
	for _, target := range b.Targets {
		var err error
		usage := b.Relation.Columns[target].Usage
		if usage.Type.ContainsRegclass() && s.input.Regclass == nil {
			s.input.Regclass, err = e.captureRegclassInput(auth)
		}
		if err == nil && (usage.Type.ContainsRegtype() || usage.Type.ContainsRegproc()) && s.input.Regtype == nil {
			s.input.Regtype, err = e.captureRegtypeInput(auth, s.op)
		}
		if err == nil && usage.Type.ContainsRegrole() && s.input.Regrole == nil {
			s.input.Regrole = capturedRegroleInput{roles: auth.Roles}
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
			err = binder.CaptureTypeDiagnosticInput(usage.Type)
		}
		if err != nil {
			return err
		}
	}
	if binder.Input.ACLItem != nil {
		s.input.ACLItem = binder.Input.ACLItem
	}
	if binder.Input.FTS != nil {
		s.input.FTS = binder.Input.FTS
	}
	if binder.Input.TypeDiagnosticName != nil {
		s.input.TypeDiagnosticName = binder.Input.TypeDiagnosticName
	}
	return nil
}
func (s *StatementScope) copyTableRows(b analyze.Binding, auth AuthorizationView) (*exec.FinalRows, error) {
	e := s.engine()
	e.mu.Lock()
	view, err := s.acquireDataViewLocked(e, b.Ref)
	if err == nil {
		e.markTemporaryAccessLocked(s, b.Ref)
	}
	e.mu.Unlock()
	if err != nil {
		return nil, err
	}
	source, err := exec.CapturePrefix(view, b.Ref)
	if err != nil {
		return nil, err
	}
	borrowQueryAuthority(source, s)
	if err = exec.SelectQuerySources(s.op.Context(), source); err != nil {
		return nil, err
	}
	schema := make([]catalog.Column, len(b.Targets))
	for i, target := range b.Targets {
		schema[i] = b.Relation.Columns[target]
	}
	rows := exec.NewFinalRows(schema, source.Count())
	rows.Source = source
	rows.Slots = b.Targets
	rows.Output = s.output
	if err = e.captureRegclassOutput(s.op, rows); err != nil {
		rows.Close()
		return nil, err
	}
	return rows, nil
}
