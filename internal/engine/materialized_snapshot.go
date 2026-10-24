package engine

import (
	"context"
	"fmt"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/typedef"
	"stuntgres/internal/engine/view"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// All rows and descriptors belong exclusively to Restore's isolated engine.
// No public INSERT execution, authority relaxation or definition evaluation is
// involved. The destination engine still receives one fenced replacement only.
func (e *Engine) importMaterializedSnapshots(ctx context.Context, op *session.Operation, records []view.MaterializedSnapshot) error {
	if len(records) == 0 {
		return nil
	}
	input, _ := op.Session.Settings.Presentation()
	input.ExactSnapshot = true
	e.mu.Lock()
	auth, err := e.authorizationLocked(op)
	if err == nil {
		var bootstrap authorization.RoleDescriptor
		bootstrap, err = auth.Roles.Descriptor(auth.State.Authenticated)
		if err == nil && (!bootstrap.Superuser || e.importTargets == nil) {
			err = fmt.Errorf("stuntgres: invalid materialized view import authority")
		}
		auth.State.CurrentUser = bootstrap.Ref
	}
	e.mu.Unlock()
	if err != nil {
		return err
	}
	type imported struct {
		descriptor *catalog.Relation
		rows       [][]sqltype.Value
		populated  bool
	}
	imports := make([]imported, 0, len(records))
	seen := make(map[ids.ObjectAddress]bool, len(records))
	for _, record := range records {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !record.Populated && record.Rows != "" {
			return fmt.Errorf("stuntgres: unpopulated snapshot contains rows")
		}
		e.mu.Lock()
		namespace := e.catalog.State.NameAddress(ids.NameKey{ClassID: ids.NamespaceClassID, Name: record.Schema})
		address := e.catalog.State.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: namespace, Name: record.Name})
		d, ok := e.catalog.State.Relation(address)
		valid := ok && d.RelationKind == catalog.MaterializedViewRelation && !d.Populated && !seen[address]
		e.mu.Unlock()
		if !valid {
			return fmt.Errorf("stuntgres: invalid materialized view snapshot target %s.%s", record.Schema, record.Name)
		}
		seen[address] = true
		statements, domainReplayPairs, err := view.ParseMaterializedSnapshot(ctx, record.Rows)
		if err != nil {
			return err
		}
		var rows [][]sqltype.Value
		for _, statement := range statements {
			s, err := view.MaterializedSnapshotInsert(record, d, statement)
			if err != nil {
				return err
			}
			if s.DefaultValues {
				rows = append(rows, []sqltype.Value{})
				continue
			}
			var replay *typedef.DomainReplayRecord
			if captured, ok := domainReplayPairs[s]; ok {
				replay = &captured
			}
			values, err := e.importMaterializedSnapshotRow(ctx, op, auth, d, s, input, replay)
			if err != nil {
				return err
			}
			rows = append(rows, values...)
		}
		imports = append(imports, imported{d, rows, record.Populated})
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	next := catalog.CloneState(e.catalog.State)
	for _, item := range imports {
		versions, err := e.storage.AllocateRowVersionsLocked(item.rows, nil)
		if err != nil {
			return err
		}
		for i := range versions {
			versions[i].Born = e.visibility.CommitSeq()
			versions[i].Frozen, versions[i].ImportFrozen = true, true
		}
		d := *item.descriptor
		d.ObjectRevision, err = e.catalog.RevisionLocked()
		if err != nil {
			return err
		}
		d.Populated = item.populated
		next.SetDescriptor(d.Address(), &d)
		e.storage.InstallRelationLocked(d.Address(), &storage.Relation{Versions: versions})
	}
	e.catalog.PublishLocked(next)
	return nil
}

// Captured cells use the same exact INSERT binder transport as ordinary private
// table replay. A real statement scope also retains normal CHECK providers for
// all other non-NULL conversions; no constraint suppression is introduced here.
func (e *Engine) importMaterializedSnapshotRow(ctx context.Context, op *session.Operation, auth AuthorizationView, d *catalog.Relation, s *parser.Insert, input sqltype.InputEnvironment, replay *typedef.DomainReplayRecord) (rows [][]sqltype.Value, err error) {
	scope, err := e.StartStatement(op)
	if err != nil {
		return nil, err
	}
	defer func() {
		err = scope.Finish(err)
		if err != nil {
			rows = nil
		}
	}()
	if err = scope.gate(s); err != nil {
		return nil, err
	}
	scope.input = input
	scope.routineAuth = auth
	if replay != nil {
		capture, captureErr := e.captureDomainReplay(scope, s, *replay, auth, d)
		if captureErr != nil {
			return nil, captureErr
		}
		if operationReplayOrZero(op).domainReplay != nil {
			return nil, fmt.Errorf("stuntgres: nested native replay transport")
		}
		operationReplay(op).domainReplay = capture
		defer func() { operationReplay(op).domainReplay = nil }()
	}
	e.mu.Lock()
	b, err := describe(ctx, e.analysisHost(op), s, auth, e.catalog.Domain.Ref(auth.Catalog.Epoch(), d.Address()), input, nil, nil)
	e.mu.Unlock()
	if err != nil {
		return nil, err
	}
	environment, err := view.MaterializedSnapshotEnvironment(&b, input)
	if err != nil {
		return nil, err
	}
	scope.routineDependencies = b.Dependencies
	evaluation := exec.Context{EvalStatement: exec.EvalStatement{Context: ctx, Host: hostOf(scope), Input: environment, Output: scope.output, Catalog: auth.Catalog}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}, EvalAdmission: exec.EvalAdmission{RoutineAuthority: &auth, RoutineRecipe: true, RoutineChecks: b.Dependencies}}
	evaluation.Input.DomainChecks = &evaluation
	return view.EvaluateMaterializedSnapshotRows(&evaluation, &b, d, s)
}
