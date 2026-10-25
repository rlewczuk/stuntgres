package engine

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/copyfmt"
	"stuntgres/internal/engine/dml"
	"stuntgres/internal/engine/fdw"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/regobj"
	"stuntgres/internal/engine/view"
	"stuntgres/internal/sqlstate"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
)

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
	var options copyfmt.Options
	if syntax.Query != nil {
		options, err = copyfmt.ParseOptions(syntax)
		if err != nil {
			return 0, err
		}
	}
	if err = s.snapshotExecutionAdmission(syntax); err != nil {
		return
	}
	if err = s.acquireRelation(syntax, true); err != nil {
		return
	}
	if syntax.Query == nil {
		if err = s.snapshotPhase(syntax); err != nil {
			return
		}
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
		if err = options.BindForces(nil, nil, rows.Schema()); err != nil {
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
	} else if err = dml.CopyRelationKind(b.Relation, false); err != nil {
		return 0, err
	}
	if err := access.CheckTemporaryStorageAccess(auth.Catalog, b.Relation, s.op.Session.SessionToken); err != nil {
		return 0, err
	}
	options, err = copyfmt.ParseOptions(syntax)
	if err != nil {
		return 0, err
	}
	if err = options.BindForces(b.Relation, b.Targets, nil); err != nil {
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
	var foreign *fdw.Modification
	if b.Relation.RelationKind == catalog.ForeignRelation {
		e.mu.Lock()
		target, captureErr := e.captureForeignSourceLocked(s, b.Ref, b.Relation, authorization.PrincipalRef{}, nil, plan.RowMark{})
		e.mu.Unlock()
		if captureErr != nil {
			return 0, captureErr
		}
		s.foreignUnlocked()
		foreign, err = fdw.PlanModification(s.op.Context(), target.Foreign, catalog.ForeignInsert, true)
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
	if b.Predicate != nil {
		b.Predicate, _, err = exec.PreflightCoercionsWithPolicy(&evaluation, b.Predicate, true)
		if err == nil {
			err = view.ValidateExpressionJoinExecution(&evaluation, b.Predicate, auth.Catalog)
		}
		if err != nil {
			return 0, err
		}
	}
	selected := make([]bool, len(b.Relation.Columns))
	for _, target := range b.Targets {
		selected[target] = true
	}
	binaryInput := options.Format == copyfmt.Binary && (s.protocolOrZero().command || s.protocolOrZero().simple)
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
		if selected[i] && !options.HasDefault {
			b.Insert.Defaults[i] = nil
			continue
		}
		folded, planErr := dml.FoldDefault(&evaluation, &b, i, expression, auth.Catalog, view.ValidateExpressionJoinExecution)
		if planErr != nil {
			return 0, planErr
		}
		b.Insert.Defaults[i] = folded
		if constant, ok := folded.(plan.LiteralExpr); ok && constant.Value.IsNull() && view.BindingDefaultOrigin(&b, i).Definition.Expression == nil {
			b.Insert.Defaults[i] = nil
		}
	}
	if err = dml.CopyRelationKind(b.Relation, true); err != nil {
		return 0, err
	}
	e.mu.Lock()
	if err = s.validateLockedRelation(e); err == nil {
		auth, err = e.statementAuthorizationLocked(s.op, syntax)
	}
	if err == nil {
		err = auth.CheckColumns(b.Ref, b.Relation, authorization.InsertPrivilege, b.Targets)
		if err == nil && len(b.UsedColumns) != 0 {
			err = auth.CheckColumns(b.Ref, b.Relation, authorization.SelectPrivilege, b.UsedColumns)
		}
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
		e.markTemporaryAccessLocked(s, b.Ref, temporaryOwnAccess)
	}
	e.mu.Unlock()
	if err != nil {
		return
	}
	if err = s.readmitPreparedPrograms(auth); err != nil {
		return 0, err
	}
	unique := &index.UniqueWriteBinding{}
	if foreign == nil {
		unique, err = dml.BindUniqueWrite(s.writeHost(), b.Ref)
		if err != nil {
			return 0, err
		}
	}
	if err = s.reserveCopy(); err != nil {
		return
	}
	h := s.writeHost()
	state := &dml.CopyFromState{Options: options, Predicate: b.Predicate}
	if options.OnError == copyfmt.Ignore {
		state.InputChecks = exec.InputDomainChecks(s.input.DomainChecks, s.domains.inputCatalog)
	}
	s.CopyFrom = state
	var cursor interface {
		exec.RowCursor
		Diagnostic(error) error
	}
	if options.Format == copyfmt.Binary {
		cursor = dml.NewCopyBinaryCursor(h, b, copyfmt.NewBinaryReader(bufio.NewReader(copyReader{s, source})))
	} else {
		cursor = dml.NewCopyCursor(h, b, copyfmt.NewLineReader(options, bufio.NewReader(copyReader{s, source}), dml.CopyLineTargets(b)), state)
	}
	rows, err := func() (index.WriteBatch, error) {
		if s.protocolOrZero().command || s.protocolOrZero().simple {
			s.op.CopyInput.Active.Store(true)
			// CopyInResponse permits immediate frontend cancellation. Install
			// the first-record hold before its external start hook can send it.
			s.op.CopyInput.Decoding.Store(true)
			defer s.op.StopCopyInput()
		}
		if err := s.startProtocolCopy(hooks, true, options, len(b.Targets)); err != nil {
			return index.WriteBatch{}, err
		}
		rows, err := dml.AssembleInsertRows(s.op.Context(), b, cursor, -1, schema, s.input, s.output, h, len(unique.Keys) != 0 || unique.Checked, nil, nil)
		if err == nil {
			err = s.op.PendingCopyInputError()
		}
		if err != nil {
			err = cursor.Diagnostic(err)
		}
		return rows, err
	}()
	if err != nil {
		return 0, err
	}
	if hooks.FinishInput != nil {
		_, err = s.copyIO(func() (int, error) { return 0, hooks.FinishInput() })
		if err != nil {
			return 0, cursor.Diagnostic(err)
		}
	}
	if foreign != nil {
		// Terminal input/FinishInput acceptance precedes all provider mutation.
		if err := foreign.Begin(s.op.Context(), s.foreignWrites()); err != nil {
			return 0, err
		}
		n, err := foreign.InsertRows(s.op.Context(), s.foreignWrites(), rows.Values, false, nil)
		if err != nil {
			return 0, cursor.Diagnostic(err)
		}
		return n, nil
	}
	n, err := dml.ApplyInsertValidated(h, b.Ref, rows)
	if err != nil {
		return 0, cursor.Diagnostic(err)
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
	b.Storage = auth.StorageLocked(b.Ref.Address)
	if syntax.Where != nil {
		binder := newExpressionBinder(s.op.Context(), e, s.input, s.op, nil)
		binder.Query.Auth = &auth
		binder.Target.Relation, binder.Target.Ref = b.Relation, b.Ref
		binder.Target.Name = b.Relation.Name()
		// Read actual pre-generation row values. Generated references are
		// rejected using captured uses before any preflight folding.
		binder.Consumer.CopyWhere = true
		binder.Query.Frame = 1
		namespace := analyze.RelationNamespace(auth.Catalog, b.Relation, b.Ref, analyze.QueryTargetLeaf)
		binder.Query.Namespace = &namespace
		b.Predicate, err = binder.BindBoolean(syntax.Where, analyze.FilterContext, "WHERE")
		if err != nil {
			return
		}
		for _, slot := range binder.Target.Uses {
			if _, generated := b.Relation.ColumnMeta.Generations[b.Relation.Attributes[slot]]; generated {
				record := sqlError("42P10", "generated columns are not supported in COPY FROM WHERE conditions").(*sqlstate.Error)
				record.Detail = fmt.Sprintf("Column \"%s\" is a generated column.", b.Relation.Columns[slot].Name)
				err = record
				return
			}
		}
		b.UsedColumns, b.Dependencies = binder.Target.Uses, binder.Dependencies
		s.input = binder.Input
		s.routineDependencies = b.Dependencies
	}
	b.Targets, err = copyfmt.ResolveColumns(b.Relation, syntax.Columns)
	if err != nil {
		return
	}
	privilege := authorization.SelectPrivilege
	if syntax.From {
		privilege = authorization.InsertPrivilege
	}
	err = auth.CheckColumns(b.Ref, b.Relation, privilege, b.Targets)
	if err == nil && len(b.UsedColumns) != 0 {
		err = auth.CheckColumns(b.Ref, b.Relation, authorization.SelectPrivilege, b.UsedColumns)
	}
	return
}

func (e *Engine) initCopyInputLocked(s *StatementScope, b *analyze.Binding, auth AuthorizationView) error {
	binder := newExpressionBinder(s.op.Context(), e, s.input, s.op, nil)
	binder.Query.Auth = &auth
	binder.Target.InsertTarget = b.Relation
	analyze.BindInsertDefaults(&e.catalog.Owner, b, auth.Catalog, parser.OverrideSystem)
	if err := binder.CaptureRetainedWriteInput(b.Insert.Defaults, b.Relation.ColumnMeta.Generations, b.Relation.Constraints.Check); err != nil {
		return err
	}
	s.input = binder.Input
	for _, target := range b.Targets {
		var err error
		usage := b.Relation.Columns[target].Usage
		if usage.Type.ContainsRegclass() && s.input.Regclass == nil {
			s.input.Regclass, err = captureRegclassInput(auth)
		}
		if err == nil && (usage.Type.ContainsRegtype() || usage.Type.ContainsRegproc()) && s.input.Regtype == nil {
			s.input.Regtype, err = captureRegtypeInput(auth, s.op)
		}
		if err == nil && usage.Type.ContainsRegrole() && s.input.Regrole == nil {
			s.input.Regrole = regobj.RoleInput{Roles: auth.Roles}
		}
		if err == nil && usage.Type.ContainsOperatorAlias() && s.input.Operator == nil {
			s.input.Operator, err = captureOperatorInput(auth, s.op)
		}
		if err == nil && usage.Type.ContainsRegproc() && s.input.Regproc == nil {
			s.input.Regproc, err = captureRoutineInput(auth, s.input.Regtype.(regobj.TypeInput))
		}
		if err == nil && usage.Type.ContainsRegObject() && (s.input.Regcollation == nil || s.input.Regnamespace == nil) {
			var input regobj.ObjectInput
			input, err = captureRegObjectInput(auth)
			if err == nil {
				s.input.Regcollation, s.input.Regnamespace = input, input
			}
		}
		if err == nil && usage.Type.ContainsRegTS() && s.input.RegTS == nil {
			s.input.RegTS, err = captureRegTSInput(auth)
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
		e.markTemporaryAccessLocked(s, b.Ref, temporaryOwnAccess)
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
