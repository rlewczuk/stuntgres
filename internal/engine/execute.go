package engine

import (
	"fmt"
	"strconv"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func (e *Engine) executeStatement(scope *StatementScope, stmt parser.Statement, count int, importFrame FrameImporter) (Result, error) {
	op := scope.op
	if err := e.validateOperation(op); err != nil {
		return Result{}, err
	}
	if !op.commandAdmission {
		op.snapshotNoticeThreshold()
	}
	ctx := op.Context()
	switch stmt.(type) {
	case *parser.CreateView:
		return e.executeCreateView(scope, stmt.(*parser.CreateView), count)
	case *parser.CreateSequence, *parser.AlterSequence, *parser.DropSequence:
		return e.executeSequenceDDL(scope, stmt, count)
	case *parser.Comment:
		if c := stmt.(*parser.Comment); c.Kind == parser.CommentSequence {
			return e.executeSequenceComment(scope, c, count)
		}
		result, err := e.executeComment(scope, stmt.(*parser.Comment), count)
		if err == nil {
			scope.completed = true
		}
		return result, err
	case *parser.CreateSchema, *parser.AlterSchema, *parser.DropSchema:
		result, err := e.executeSchema(scope, op, stmt, count, scope.input)
		if err == nil {
			scope.completed = true
		}
		return result, err
	case *parser.CreateRole, *parser.DropRole, *parser.SessionAuthorization, *parser.IdentitySelect:
		result, err := e.executeRole(op, stmt, count)
		if _, identity := stmt.(*parser.IdentitySelect); err == nil && !identity {
			scope.completed = true
		}
		return result, err
	case *parser.Set, *parser.Show, *parser.Reset:
		result, err := e.executeSetting(op, stmt, count, scope.implicit)
		if err == nil {
			scope.completed = true
		}
		return result, err
	case *parser.SettingCall:
		result, err := e.executeSettingCall(op, stmt.(*parser.SettingCall), count, importFrame, scope.input, scope.output)
		if err == nil {
			scope.completed = true
		}
		return result, err
	}
	e.mu.Lock()
	if err := scope.refreshRelationGrantsLocked(e); err != nil {
		e.mu.Unlock()
		return Result{}, err
	}
	if err := ctx.Err(); err != nil {
		e.mu.Unlock()
		return Result{}, err
	}
	auth, err := e.statementAuthorizationLocked(op, stmt)
	if err != nil {
		e.mu.Unlock()
		return Result{}, err
	}
	if s, ok := stmt.(*parser.AlterTable); ok {
		return e.alterTableLocked(scope, op, auth, s, count)
	}
	if s, ok := stmt.(*parser.AlterTableDefault); ok {
		return e.alterDefaultLocked(scope, op, auth, s, count)
	}
	switch s := stmt.(type) {
	case *parser.AlterTableSetSchema:
		return e.alterSchemaLocked(scope, op, auth, s, count)
	case *parser.AlterTableRenameColumn:
		return e.renameRelationLocked(scope, op, auth, s.Name, s.Column.Text, s.NewName.Text, s.IfExists, true, count)
	case *parser.AlterTableRenameTable:
		return e.renameRelationLocked(scope, op, auth, s.Name, "", s.NewName.Text, s.IfExists, false, count)
	}
	if s, ok := stmt.(*parser.DropTable); ok {
		return e.dropTableLocked(scope, op, auth, s, count)
	}
	if s, ok := stmt.(*parser.CreateTable); ok {
		if count != 0 {
			e.mu.Unlock()
			return Result{}, fmt.Errorf("stuntgres: got %d arguments, want 0", count)
		}
		nsref, er := e.creationNamespace(auth, s.Name)
		if er != nil {
			e.mu.Unlock()
			return Result{}, creationError(er, s.Name)
		}
		if er = checkTableCreationNamespace(auth.catalog, nsref.Address); er != nil {
			e.mu.Unlock()
			return Result{}, er
		}
		if op.restoreTableDestination {
			bootstrap, er := auth.roles.Descriptor(auth.state.Authenticated)
			if er != nil || !bootstrap.Superuser || e.importTargets == nil {
				e.mu.Unlock()
				return Result{}, fmt.Errorf("stuntgres: invalid trusted restore destination authority")
			}
		}
		if er = auth.CheckCreate(nsref); er != nil {
			denied, ok := er.(*sqlstate.Error)
			if !op.restoreTableDestination || !ok || denied.Code != "42501" {
				e.mu.Unlock()
				return Result{}, creationError(er, s.Name)
			}
		}
		ns := nsref.Address
		key := NameKey{ClassID: RelationClassID, Namespace: ns, Name: s.Name.Parts[len(s.Name.Parts)-1].Text}
		if _, exists := e.state.names[key]; exists {
			e.mu.Unlock()
			if s.IfNotExists {
				op.existingRelationNotice(key.Name)
				return Result{}, nil
			}
			return Result{}, sqlError("42P07", fmt.Sprintf(`relation "%s" already exists`, key.Name))
		}
		base := CatalogRead{owner: e, state: e.state}
		stage := e.BeginCatalogStage(base)
		stage.authSession = op.session
		stage.authRevision = auth.revision
		stage.roleRevision = auth.roles.state.revision
		er = e.schemaTableLocked(op, auth, stage, s, ns, auth.state.CurrentUser, scope.input)
		e.mu.Unlock()
		if er != nil {
			return Result{}, er
		}
		prepared, er := e.ValidateCatalogStage(ctx, stage)
		if er != nil {
			e.DiscardCatalogStage(stage)
			return Result{}, er
		}
		_, er = e.publishCatalogOperation(op, prepared, PublicationToken{Epoch: base.Epoch()})
		if er == nil {
			scope.completed = true
		}
		if _, conflict := er.(CatalogConflict); conflict {
			if err := ctx.Err(); err != nil {
				return Result{}, err
			}
			if s.IfNotExists {
				e.mu.Lock()
				current := CatalogRead{owner: e, state: e.state}
				currentAuth, authErr := e.authorizationLocked(op)
				if authErr != nil {
					contextErr := ctx.Err()
					e.mu.Unlock()
					if contextErr != nil {
						return Result{}, contextErr
					}
					return Result{}, authErr
				}
				resolved, resolveErr := e.creationNamespace(currentAuth, s.Name)
				var destinationErr error
				if resolveErr == nil {
					destinationErr = checkTableCreationNamespace(currentAuth.catalog, resolved.Address)
				}
				_, exists := e.state.names[key]
				same := resolveErr == nil && destinationErr == nil && resolved.Address == ns && current.Epoch() == base.Epoch()
				var accessErr error
				if same && exists {
					accessErr = currentAuth.CheckCreate(resolved)
				}
				contextErr := ctx.Err()
				e.mu.Unlock()
				if contextErr != nil {
					return Result{}, contextErr
				}
				if resolveErr != nil {
					return Result{}, creationError(resolveErr, s.Name)
				}
				if destinationErr != nil {
					return Result{}, destinationErr
				}
				if accessErr != nil {
					return Result{}, creationError(accessErr, s.Name)
				}
				if same && exists {
					op.existingRelationNotice(key.Name)
					return Result{}, nil
				}
			}
			return Result{}, sqlError("42P07", fmt.Sprintf(`relation "%s" already exists`, key.Name))
		}
		return Result{}, er
	}
	expected := scope.Expected
	var candidate StatementDescription
	valid := true
	if expected != nil {
		valid, err = e.sequenceAnalysisValidLocked(auth, expected.sequences)
		if err != nil {
			e.mu.Unlock()
			return Result{}, err
		}
		candidate = *expected
		if !valid {
			candidate.sequences = nil
		}
		expected = &candidate
	}
	b, err := e.describe(ctx, stmt, auth, scope.lockedRef, scope.input, op, expected)
	if err != nil {
		e.mu.Unlock()
		return Result{}, err
	}
	if err := scope.validateQuerySources(b.querySources); err != nil {
		e.mu.Unlock()
		return Result{}, err
	}
	if scope.Expected != nil {
		var results []ColumnDescriptor
		if b.query != nil && !b.commandQuery {
			results = b.query.Root.Schema()
		}
		if b.returning != nil {
			results = b.returning.schema
		}
		if err := checkStatementSignature(*scope.Expected, StatementDescription{Parameters: b.parameters, Results: results}); err != nil {
			e.mu.Unlock()
			return Result{}, err
		}
		if err = e.pinSequenceReplacementLocked(auth, scope.Expected.sequences, b.sequenceConstants); err != nil {
			e.mu.Unlock()
			return Result{}, err
		}
		if !valid {
			analysis, err := e.sequenceAnalysisLocked(auth, b.sequenceConstants)
			if err != nil {
				e.mu.Unlock()
				return Result{}, err
			}
			*scope.Expected.sequences = analysis
		}
	}
	if count != b.parameters.Count {
		e.mu.Unlock()
		return Result{}, fmt.Errorf("stuntgres: got %d arguments, want %d", count, b.parameters.Count)
	}
	canonical := make([]sqltype.Value, count)
	if count != 0 {
		if importFrame == nil {
			e.mu.Unlock()
			return Result{}, fmt.Errorf("stuntgres: missing parameter importer")
		}
		if err = importFrame(ctx, b.parameters, canonical, scope.input); err != nil {
			e.mu.Unlock()
			return Result{}, err
		}
		for i, v := range canonical {
			if v.Type() != b.parameters.Types[i].Type {
				e.mu.Unlock()
				return Result{}, fmt.Errorf("stuntgres: invalid parameter frame")
			}
			if !v.IsNull() && v.Type().ID() == sqltype.Regclass {
				text, inputErr := v.Text()
				if inputErr != nil {
					e.mu.Unlock()
					return Result{}, inputErr
				}
				ref, inputErr := e.regclassLocked(auth, text)
				if inputErr != nil {
					e.mu.Unlock()
					return Result{}, sequenceInputError(inputErr, parser.Position{}, "", i+1)
				}
				canonical[i] = sqltype.String(v.Type(), strconv.FormatUint(uint64(ref.Address.ObjectID), 10))
			}
		}
	}
	expressions := b.expressions
	expressionsChanged := false
	evaluation := EvalContext{Context: ctx, Parameters: canonical, statementScope: scope, Input: scope.input, Output: scope.output}
	_, inserting := stmt.(*parser.Insert)
	var queryRoot LogicalNode
	_, deleting := stmt.(*parser.Delete)
	if inserting || b.update != nil || b.query != nil || (deleting && (b.predicate != nil || b.returning != nil)) || len(b.defaults) != 0 {
		// Planning-owned conversions precede executor ACLs, without retaining
		// Engine.mu or acquiring a data view. Analysis/namespace checks remain
		// earlier, and only this execution's imported frame is materialized.
		e.mu.Unlock()
		if inserting {
			// Supplied values retain priority over defaults and executor ACLs.
			// Constant planning and frame conversions both run unlocked; only
			// changed rows and the outer slice need execution-private copies.
			for i, row := range b.expressions {
				var privateRow []BoundExpr
				for j, expression := range row {
					folded, changed, foldErr := preflightCoercions(&evaluation, expression)
					if foldErr != nil {
						return Result{}, insertValueError(auth.catalog, foldErr, b.relation.columns[b.targets[j]], stmt.(*parser.Insert).Rows[i][j], nil)
					}
					if changed {
						if privateRow == nil {
							privateRow = append([]BoundExpr(nil), row...)
						}
						privateRow[j] = folded
					}
				}
				if privateRow != nil {
					if !expressionsChanged {
						expressions = append([][]BoundExpr(nil), b.expressions...)
						expressionsChanged = true
					}
					expressions[i] = privateRow
				}
			}
		}
		if b.update != nil {
			b.update, err = preflightUpdate(&evaluation, b.update)
		} else if b.query != nil {
			queryRoot, err = preflightQuery(&evaluation, b.query.Root)
		} else if deleting {
			if b.predicate != nil {
				b.predicate, _, err = preflightCoercions(&evaluation, b.predicate)
			}
			if err == nil && b.returning != nil {
				b.returning, err = preflightDeleteReturning(&evaluation, b.returning)
			}
		}
		if err != nil {
			return Result{}, err
		}
		// Both INSERT paths plan only defaults this binding actually consumes.
		// Demand immutable conversions even for an empty query source, while
		// keeping the stored definition and any runtime-only producer unfolded.
		var privateDefaults []BoundExpr
		for i, expression := range b.defaults {
			if expression == nil {
				continue
			}
			folded, changed, foldErr := preflightCoercionsWithPolicy(&evaluation, expression, true)
			if foldErr != nil {
				return Result{}, foldErr
			}
			if changed {
				if privateDefaults == nil {
					privateDefaults = append([]BoundExpr(nil), b.defaults...)
				}
				privateDefaults[i] = folded
			}
		}
		if privateDefaults != nil {
			b.defaults = privateDefaults
		}
		e.mu.Lock()
		if err = ctx.Err(); err != nil {
			e.mu.Unlock()
			return Result{}, err
		}
		if err = scope.validateLockedRelation(e); err != nil {
			e.mu.Unlock()
			return Result{}, err
		}
		// Do not authorize source capture with the pre-unlock authority view.
		// Keep the original binding's actual-column manifest and exact ref.
		auth, err = e.statementAuthorizationLocked(op, stmt)
		if err != nil {
			e.mu.Unlock()
			return Result{}, err
		}
	}
	switch stmt.(type) {
	case *parser.Insert, *parser.Update, *parser.Delete:
		if err := tableKind(b.relation, "change"); err != nil {
			e.mu.Unlock()
			return Result{}, err
		}
	}
	if err = auth.checkBinding(b, stmt); err != nil {
		e.mu.Unlock()
		return Result{}, err
	}
	tag := modifyingTag(stmt)
	if _, selecting := stmt.(*parser.Select); selecting && tag == "" && scope.owner.readOnly && b.query != nil {
		mode, modeErr := e.firstQueryRowModeLocked(scope, queryRoot, b.querySources)
		if modeErr != nil {
			e.mu.Unlock()
			return Result{}, modeErr
		}
		if mode != 0 {
			tag = "SELECT"
		}
	}
	if scope.owner.readOnly && tag != "" {
		e.mu.Unlock()
		return Result{}, sqlError("25006", "cannot execute "+tag+" in a read-only transaction")
	}
	if b.query != nil {
		if err = e.initializeQueryLocksLocked(scope, queryRoot, b.querySources); err != nil {
			e.mu.Unlock()
			return Result{}, err
		}
	}
	switch s := stmt.(type) {
	case *parser.Update:
		view, err := scope.acquireDataViewLocked(e, b.ref)
		if err != nil {
			e.mu.Unlock()
			return Result{}, err
		}
		source, err := captureQuerySource(ctx, view, b.ref)
		if err != nil {
			e.mu.Unlock()
			return Result{}, err
		}
		schema := auth.catalog.state.descriptors[b.relation.namespace].Name()
		e.mu.Unlock()
		return e.executeUpdate(ctx, scope, b, source, canonical, schema)
	case *parser.Insert:
		if s.Source != nil {
			source, sourceErr := e.captureQuerySourcesLocked(scope, b.querySources, s.Source.Select != nil && len(b.querySources) == 0)
			if sourceErr != nil {
				e.mu.Unlock()
				return Result{}, sourceErr
			}
			schema := auth.catalog.state.descriptors[b.relation.namespace].Name()
			e.mu.Unlock()
			if err := selectQuerySources(ctx, source); err != nil {
				return Result{}, err
			}
			b.query = &BoundQuery{Root: queryRoot}
			rows, queryErr := evaluateInsertQuery(ctx, b, source, canonical, schema, scope.input, scope.output)
			if queryErr != nil {
				return Result{}, queryErr
			}
			if err := ctx.Err(); err != nil {
				return Result{}, err
			}
			if err := scope.Writes().Apply(b.ref, rows); err != nil {
				return Result{}, err
			}
			return Result{Count: int64(len(rows))}, nil
		}
		e.mu.Unlock()
		rows := make([][]sqltype.Value, len(s.Rows))
		for i, source := range s.Rows {
			if err = ctx.Err(); err != nil {
				return Result{}, err
			}
			row := make([]sqltype.Value, len(b.relation.columns))
			for j, c := range b.relation.columns {
				row[j] = sqltype.Null(c.Usage.Type)
			}
			if len(b.defaults) != 0 {
				supplied := b.expressions[i]
				for j, target := range b.targets {
					if j < len(supplied) && supplied[j] != nil {
						row[target] = sqltype.Value{}
					}
				}
				for j, expression := range b.defaults {
					if expression != nil && row[j].Type() != nil {
						row[j], err = expression.Eval(&evaluation)
						if err != nil {
							return Result{}, err
						}
					}
				}
			}
			for j, v := range source {
				if b.expressions[i][j] == nil {
					continue
				}
				target := b.targets[j]
				c := b.relation.columns[target]
				row[target], err = expressions[i][j].Eval(&evaluation)
				if err != nil {
					return Result{}, insertValueError(auth.catalog, err, c, v, nil)
				}
			}
			rows[i] = row
		}
		// Supplied frame-only conversions have already folded before ACL
		// checks; executor constraints must not mask their failures. Defaults
		// execute per row after authorization, before staged write publication.
		for _, row := range rows {
			if err = ctx.Err(); err != nil {
				return Result{}, err
			}
			for j, c := range b.relation.columns {
				if row[j].IsNull() && !c.Nullable {
					schema := auth.catalog.state.descriptors[b.relation.namespace].Name()
					return Result{}, notNullError(schema, b.relation.name, c.Name, row, scope.output)
				}
			}
		}
		err = ctx.Err()
		if err == nil {
			err = scope.Writes().Apply(b.ref, rows)
		}
		if err != nil {
			return Result{}, err
		}
		return Result{Count: int64(len(rows))}, nil
	case *parser.Delete:
		view, viewErr := scope.acquireDataViewLocked(e, b.ref)
		if viewErr == nil && b.returning != nil {
			e.captureCommentSnapshotLocked(scope)
		}
		e.mu.Unlock()
		if viewErr != nil {
			return Result{}, viewErr
		}
		return scope.executeDelete(b, view, canonical)
	case *parser.Select:
		source, sourceErr := e.captureQuerySourcesLocked(scope, b.querySources, s.IsSingleton())
		e.mu.Unlock()
		if sourceErr != nil {
			return Result{}, sourceErr
		}
		if err := selectQuerySources(ctx, source); err != nil {
			return Result{}, err
		}
		rows, count, queryErr := finalizeQuery(ctx, queryRoot, source, canonical)
		if queryErr != nil {
			return Result{}, queryErr
		}
		if b.query.regclassOutput && count != 0 {
			if err := e.captureRegclassOutput(op, rows.(*finalRows)); err != nil {
				rows.Close()
				return Result{}, err
			}
		}
		return Result{Count: int64(count), Rows: rows}, nil
	}
	e.mu.Unlock()
	return Result{}, sqlError("0A000", "unsupported statement")
}
