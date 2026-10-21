package engine

import (
	"fmt"
	"strings"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

func (e *Engine) executeCreateIndex(scope *StatementScope, s *parser.CreateIndex, count int) (Result, error) {
	if count != 0 {
		return Result{}, fmt.Errorf("stuntgres: got %d arguments, want 0", count)
	}
	if operationReplayOrZero(scope.op).tableDestination && s.Concurrent.Character != 0 {
		return Result{}, fmt.Errorf("stuntgres: restore cannot execute concurrent index phases")
	}
	for {
		e.mu.Lock()
		fail := func(err error) (Result, error) { e.mu.Unlock(); return Result{}, err }
		if err := scope.refreshRelationGrantsLocked(e); err != nil {
			return fail(err)
		}
		auth, err := e.statementAuthorizationLocked(scope.op, s)
		if err != nil {
			return fail(err)
		}
		if err = scope.captureIdentityLocked(auth); err != nil {
			return fail(err)
		}
		parent, ok := auth.Catalog.Relation(scope.lockedRef.Address)
		if !ok {
			return fail(ids.ErrCatalogReplaced)
		}
		if operationReplayOrZero(scope.op).tableDestination {
			bootstrap, err := auth.Roles.Descriptor(auth.State.Authenticated)
			if err != nil || !bootstrap.Superuser || e.importTargets == nil {
				return fail(fmt.Errorf("stuntgres: invalid trusted restore index authority"))
			}
		}
		if len(s.Keys)+len(s.Include) > 32 {
			return fail(sqlError("54011", "cannot use more than 32 columns in an index"))
		}
		if parent.RelationKind != catalog.TableRelation && parent.RelationKind != catalog.MaterializedViewRelation && parent.RelationKind != catalog.PartitionedRelation {
			if parent.RelationKind == catalog.IndexRelation {
				return fail(&sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42809", Message: fmt.Sprintf(`cannot open relation "%s"`, parent.Name()), Detail: "This operation is not supported for indexes."})
			}
			return fail(&sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42809", Message: fmt.Sprintf(`cannot create index on relation "%s"`, parent.Name()), Detail: fmt.Sprintf("This operation is not supported for %ss.", relationDiagnosticKind(parent))})
		}
		ns := e.catalog.Domain.Ref(auth.Catalog.Epoch(), parent.Namespace())
		if err = auth.CheckCreate(ns); err != nil {
			denied, ok := err.(*sqlstate.Error)
			if !operationReplayOrZero(scope.op).tableDestination || !ok || denied.Code != "42501" {
				return fail(err)
			}
		}
		if err = index.ValidateIndexMethod(s); err != nil {
			return fail(err)
		}
		if s.Concurrent.Character != 0 && s.Predicate != nil {
			return fail(sqlError("0A000", "partial concurrent indexes are unsupported"))
		}
		if len(s.Options) != 0 {
			method := catalog.BtreeMethod
			if s.Method == "hash" {
				method = catalog.HashMethod
			}
			if s.Method == "gist" {
				method = catalog.GistMethod
			}
			if err := index.ValidateIndexReloptions(s.Options, method, false); err != nil {
				return fail(err)
			}
		}
		predicate, err := e.bindIndexPredicate(scope.op, scope.input, auth, parent, s.Predicate)
		if err != nil {
			return fail(err)
		}
		definition, columns, attributes, lookup, err := e.bindIndexAttributes(scope.op, scope.input, auth, parent, s)
		if err != nil {
			return fail(err)
		}
		definition.Predicate = predicate
		name := s.Name.Text
		if name == "" {
			var addition strings.Builder
			for i, column := range columns {
				if i > 0 {
					addition.WriteByte('_')
				}
				addition.WriteString(column.Name)
				if addition.Len() >= 64 {
					break
				}
			}
			name = generatedRelationName(parent.Name(), addition.String(), "idx", func(n string) bool {
				_, ok := auth.Catalog.LookupName(ids.NameKey{ClassID: ids.RelationClassID, Namespace: parent.Namespace(), Name: n})
				return ok
			})
		}
		key := ids.NameKey{ClassID: ids.RelationClassID, Namespace: parent.Namespace(), Name: name}
		if _, exists := auth.Catalog.LookupName(key); exists {
			e.mu.Unlock()
			if s.IfNotExists {
				scope.op.ExistingRelationNotice(name)
				scope.completed = true
				return Result{}, nil
			}
			return Result{}, sqlError("42P07", fmt.Sprintf(`relation "%s" already exists`, name))
		}
		// The released provider-options phase follows catalog name admission.
		for i, raw := range s.Keys {
			if len(raw.Options) != 0 {
				return fail(sqlError("22023", fmt.Sprintf("operator class %s has no options", definition.Keys[i].Class.Name)))
			}
		}
		definition.Unique, definition.NullsNotDistinct = s.Unique, s.NullsNotDistinct
		if s.Unique && s.Concurrent.Character == 0 && !operationReplayOrZero(scope.op).tableDestination {
			if err := index.ValidateUniqueProviders(parent, definition, s.Keys); err != nil {
				return fail(err)
			}
		}
		if err := index.PartitionUniqueCoverage(parent, definition); err != nil {
			return fail(err)
		}
		if len(s.Options) != 0 {
			return fail(index.Capability("index storage parameters are unsupported", s.WithPos))
		}
		oid, err := e.catalog.allocateOIDLocked()
		if err != nil {
			return fail(err)
		}
		revision, err := e.catalog.revisionLocked()
		if err != nil {
			return fail(err)
		}
		for i := range columns {
			columns[i].OriginTable = uint32(oid)
			columns[i].OriginAttribute = int16(i + 1)
		}
		d := &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: oid}, ObjectNamespace: parent.Namespace(), ObjectName: name, ObjectRevision: revision, Owner: parent.Owner, Columns: columns, Attributes: attributes, Index: lookup, RelationKind: catalog.IndexRelation, SQLIndex: definition}
		d.ColumnMeta.HighWater = int32(len(attributes))
		if s.Concurrent.Character != 0 {
			definition.Ready, definition.Valid = false, false
			if err := e.visibilityWriteLocked(scope.txRef); err != nil {
				return fail(err)
			}
		}
		base := auth.Catalog
		stage := e.BeginCatalogStage(base)
		stage.authorizeLocked(auth, scope.op.Session)
		stage.indexes().Output = scope.output
		err = stage.Create(d)
		if err == nil && parent.Partition != nil {
			if s.Only {
				definition.Valid = len(parent.Partition.Children) == 0
			} else {
				err = e.stagePartitionIndexChildren(scope.op, stage, parent, d, true)
			}
		}
		e.mu.Unlock()
		if err != nil {
			e.DiscardCatalogStage(stage)
			return Result{}, err
		}
		prepared, err := e.ValidateCatalogStage(scope.op.Context(), stage)
		if err != nil {
			e.DiscardCatalogStage(stage)
			return Result{}, err
		}
		_, err = e.publishCatalogOperation(scope.op, scope, prepared, PublicationToken{Epoch: base.Epoch()})
		e.DiscardCatalogStage(stage)
		if _, conflict := err.(catalog.Conflict); conflict {
			if contextErr := scope.op.Context().Err(); contextErr != nil {
				return Result{}, contextErr
			}
			if s.Name.Text == "" {
				continue
			}
			if s.IfNotExists {
				e.mu.Lock()
				currentAuth, authErr := e.statementAuthorizationLocked(scope.op, s)
				current := e.privateCatalogLocked(scope.owner.Catalog)
				_, exists := current.LookupName(key)
				same := current.Epoch() == base.Epoch() && current.Get(parent.Address()) != nil
				if authErr == nil && same && exists {
					authErr = currentAuth.Check(access.Requirement{Object: scope.lockedRef, OwnerOnly: true})
				}
				if authErr == nil && same && exists {
					authErr = currentAuth.CheckCreate(ns)
				}
				e.mu.Unlock()
				if authErr != nil {
					return Result{}, authErr
				}
				if same && exists {
					scope.op.ExistingRelationNotice(name)
					scope.completed = true
					return Result{}, nil
				}
			}
			return Result{}, sqlError("42P07", fmt.Sprintf(`relation "%s" already exists`, name))
		}
		if err == nil && s.Concurrent.Character != 0 {
			e.mu.Lock()
			e.visibility.ResetPhaseLocked(scope.txRef)
			e.mu.Unlock()
			return e.executeConcurrentIndex(scope, s, d)
		}
		if err == nil {
			scope.completed = true
		}
		return Result{}, err
	}
}
