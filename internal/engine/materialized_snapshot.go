package engine

import (
	"context"
	"fmt"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/typecmds"
	"stuntgres/internal/engine/typedef"
	"stuntgres/internal/engine/view"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// All rows and descriptors belong exclusively to Restore's isolated engine.
// No public INSERT execution, authority relaxation or definition evaluation is
// involved. The destination engine still receives one fenced replacement only.
func importMaterializedSnapshots(e *core.Engine, ctx context.Context, op *session.Operation, records []view.MaterializedSnapshot) error {
	if len(records) == 0 {
		return nil
	}
	input, _ := op.Session.Settings.Presentation()
	input.ExactSnapshot = true
	e.Mu.Lock()
	auth, err := e.AuthorizationLocked(op)
	if err == nil {
		var bootstrap authorization.RoleDescriptor
		bootstrap, err = auth.Roles.Descriptor(auth.State.Authenticated)
		if err == nil && (!bootstrap.Superuser || e.ImportTargets == nil) {
			err = fmt.Errorf("stuntgres: invalid materialized view import authority")
		}
		auth.State.CurrentUser = bootstrap.Ref
	}
	e.Mu.Unlock()
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
		e.Mu.Lock()
		namespace := e.Catalog.State.NameAddress(ids.NameKey{ClassID: ids.NamespaceClassID, Name: record.Schema})
		address := e.Catalog.State.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: namespace, Name: record.Name})
		d, ok := e.Catalog.State.Relation(address)
		valid := ok && d.RelationKind == catalog.MaterializedViewRelation && !d.Populated && !seen[address]
		e.Mu.Unlock()
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
			values, err := importMaterializedSnapshotRow(e, ctx, op, auth, d, s, input, replay)
			if err != nil {
				return err
			}
			rows = append(rows, values...)
		}
		imports = append(imports, imported{d, rows, record.Populated})
	}
	e.Mu.Lock()
	defer e.Mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	next := catalog.CloneState(e.Catalog.State)
	for _, item := range imports {
		versions, err := e.Storage.AllocateRowVersionsLocked(item.rows, nil)
		if err != nil {
			return err
		}
		for i := range versions {
			versions[i].Born = e.Visibility.CommitSeq()
			versions[i].Frozen, versions[i].ImportFrozen = true, true
		}
		d := *item.descriptor
		d.ObjectRevision, err = e.Catalog.RevisionLocked()
		if err != nil {
			return err
		}
		d.Populated = item.populated
		next.SetDescriptor(d.Address(), &d)
		e.Storage.InstallRelationLocked(d.Address(), &storage.Relation{Versions: versions})
	}
	e.Catalog.PublishLocked(next)
	return nil
}

// Captured cells use the same exact INSERT binder transport as ordinary private
// table replay. A real statement scope also retains normal CHECK providers for
// all other non-NULL conversions; no constraint suppression is introduced here.
func importMaterializedSnapshotRow(e *core.Engine, ctx context.Context, op *session.Operation, auth core.AuthorizationView, d *catalog.Relation, s *parser.Insert, input sqltype.InputEnvironment, replay *typedef.DomainReplayRecord) (rows [][]sqltype.Value, err error) {
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
	if err = scope.Gate(s); err != nil {
		return nil, err
	}
	scope.Input = input
	scope.RoutineAuth = auth
	if replay != nil {
		capture, captureErr := typecmds.CaptureDomainReplay(e, scope, s, *replay, auth, d)
		if captureErr != nil {
			return nil, captureErr
		}
		if core.OperationReplayOrZero(op).DomainReplay != nil {
			return nil, fmt.Errorf("stuntgres: nested native replay transport")
		}
		core.OperationReplay(op).DomainReplay = capture
		defer func() { core.OperationReplay(op).DomainReplay = nil }()
	}
	e.Mu.Lock()
	b, err := core.Describe(ctx, e.AnalysisHost(op), s, auth, e.Catalog.Domain.Ref(auth.Catalog.Epoch(), d.Address()), input, nil, nil)
	e.Mu.Unlock()
	if err != nil {
		return nil, err
	}
	environment, err := view.MaterializedSnapshotEnvironment(&b, input)
	if err != nil {
		return nil, err
	}
	scope.RoutineDependencies = b.Dependencies
	evaluation := exec.Context{EvalStatement: exec.EvalStatement{Context: ctx, Host: core.HostOf(scope), Input: environment, Output: scope.Output, Catalog: auth.Catalog}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}, EvalAdmission: exec.EvalAdmission{RoutineAuthority: &auth, RoutineRecipe: true, RoutineChecks: b.Dependencies}}
	evaluation.Input.DomainChecks = &evaluation
	return view.EvaluateMaterializedSnapshotRows(&evaluation, &b, d, s)
}
