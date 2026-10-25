package engine

import (
	"fmt"
	"slices"
	"strings"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ddl"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/tablecmds"
	"stuntgres/internal/engine/typedef"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func executeSchema(e *core.Engine, scope *core.StatementScope, op *session.Operation, stmt parser.Statement, count int, input sqltype.InputEnvironment) (core.Result, error) {
	if count != 0 {
		return core.Result{}, fmt.Errorf("stuntgres: got %d arguments, want 0", count)
	}
	if schema, ok := stmt.(*parser.CreateSchema); ok {
		for _, table := range schema.Tables {
			if table.PartitionOf != nil {
				return core.Result{}, expr.ErrorAt("0A000", "partition definitions in CREATE SCHEMA are unsupported", table.PartitionOf.Pos)
			}
			if table.PartitionBy != nil {
				return core.Result{}, expr.ErrorAt("0A000", "partition definitions in CREATE SCHEMA are unsupported", table.PartitionBy.Pos)
			}
		}
	}
	var stage *core.CatalogStage
	var err error
	noticeMark := len(op.Notices)
	if drop, ok := stmt.(*parser.DropSchema); ok {
		var manifest ddl.SchemaDropManifest
		manifest, err = acquireSchemaDrop(scope, drop)
		if err != nil {
			return core.Result{}, err
		}
		e.Mu.Lock()
		stage, err = stageSchemaDropLocked(e, op, drop, manifest)
		e.Mu.Unlock()
	} else if create, ok := stmt.(*parser.CreateCollation); ok {
		var plan typedef.CollationCreatePlan
		e.Mu.Lock()
		stage, plan, err = prepareCollationCreateLocked(e, op, create)
		e.Mu.Unlock()
		if err != nil || !plan.NeedsCatalogLock() {
			e.DiscardCatalogStage(stage)
			return core.Result{}, err
		}
		// Only the protected cross-encoding/insertion phase waits. Preparation
		// retains the original exact-key absence for concurrent index errors.
		if err = acquireCollationCatalog(scope, stage.Base); err != nil {
			op.Notices = op.Notices[:noticeMark]
			e.DiscardCatalogStage(stage)
			return core.Result{}, err
		}
		e.Mu.Lock()
		stage, err = completeCollationCreateLocked(e, op, stage, &plan)
		e.Mu.Unlock()
		if err != nil {
			op.Notices = op.Notices[:noticeMark]
			e.DiscardCatalogStage(stage)
			return core.Result{}, err
		}
	} else {
		e.Mu.Lock()
		stage, err = stageSchemaLocked(e, op, stmt, input, scope.Output)
		e.Mu.Unlock()
	}
	if err != nil || stage == nil {
		return core.Result{}, err
	}
	stage, err = admitDefinitionMutation(scope, stage, stmt, input, noticeMark)
	if err == nil {
		stage, err = admitDefinitionRetirement(scope, stage, stmt, input)
	}
	if err != nil {
		e.DiscardCatalogStage(stage)
		return core.Result{}, err
	}
	// Retain the constructor's actual fresh domain identity for bounded Restore
	// ownership pairing; never resolve an arbitrary later object by name.
	var created ids.ObjectRef
	if _, domain := stmt.(*parser.CreateDomain); domain {
		for address, descriptor := range stage.Descriptors() {
			if typ, ok := descriptor.(*catalog.Type); ok && typ.SQLType.IsDomain() && stage.Base.Get(address) == nil {
				created = e.Catalog.Domain.Ref(stage.Base.Epoch(), address)
				break
			}
		}
	}
	defer e.DiscardCatalogStage(stage)
	if _, droppingRoutine := stmt.(*parser.DropRoutine); droppingRoutine {
		if err := acquireRoutineRetirementRoots(scope, stage); err != nil {
			return core.Result{}, err
		}
	}
	switch stmt.(type) {
	case *parser.AlterDomain, *parser.AlterType:
		// The identity stager records its exact source before companion
		// displacement. Identity authority has no scalar heap resource.
		source := stage.DomainBases[0]
		ref := e.Catalog.Domain.Ref(stage.Base.Epoch(), source.Address())
		if err := scope.AcquireSequence(ref, lock.TypeExclusive, true); err != nil {
			return core.Result{}, err
		}
		// Composite RENAME owns its actual backing relation, just as the
		// relation rename owner does. SET SCHEMA only mutates tuple metadata.
		backing := ids.ObjectAddress{}
		if identity, ok := stmt.(*parser.AlterType); ok && !identity.SetSchema {
			typ := source.(*catalog.Type)
			if typ.SQLType.IsComposite() {
				backing = typ.OwningRelation
				if err := scope.AcquireSequence(e.Catalog.Domain.Ref(ref.Epoch, backing), lock.AccessExclusive, true); err != nil {
					return core.Result{}, err
				}
			}
		}
		e.Mu.Lock()
		current, admissionErr := e.AuthorizationLocked(op)
		if admissionErr == nil && (current.Catalog.Epoch() != ref.Epoch || current.Catalog.Get(ref.Address) != source) {
			admissionErr = ids.ErrCatalogReplaced
		}
		if admissionErr == nil && backing != (ids.ObjectAddress{}) && current.Catalog.Get(backing) != stage.Base.Get(backing) {
			admissionErr = ids.ErrCatalogReplaced
		}
		if admissionErr == nil {
			admissionErr = current.Check(access.Requirement{Object: ref, OwnerOnly: true})
		}
		e.Mu.Unlock()
		if admissionErr != nil {
			return core.Result{}, admissionErr
		}
	}
	droppingTypedSource := false
	switch stmt.(type) {
	case *parser.DropType, *parser.DropDomain:
		droppingTypedSource = true
	}
	if err := tablecmds.AcquireTypedSources(scope, stage, droppingTypedSource); err != nil {
		return core.Result{}, err
	}
	domainMode := parser.DropCascade
	cascade := false
	switch s := stmt.(type) {
	case *parser.DropAccessMethod:
		domainMode, cascade = s.Mode, s.Mode == parser.DropCascade
	case *parser.DropType:
		domainMode, cascade = s.Mode, s.Mode == parser.DropCascade
	case *parser.DropDomain:
		domainMode = s.Mode
		cascade = s.Mode == parser.DropCascade
	case *parser.DropCollation:
		domainMode = s.Mode
		cascade = s.Mode == parser.DropCascade
	case *parser.DropConversion:
		domainMode, cascade = s.Mode, s.Mode == parser.DropCascade
	case *parser.DropOperator:
		domainMode, cascade = s.Mode, s.Mode == parser.DropCascade
	case *parser.DropRoutine:
		domainMode, cascade = s.Mode, s.Mode == parser.DropCascade
	case *parser.DropCast:
		domainMode, cascade = s.Mode, s.Mode == parser.DropCascade
	case *parser.DropOperatorFamily:
		domainMode, cascade = s.Mode, s.Mode == parser.DropCascade
	case *parser.DropOperatorClass:
		domainMode, cascade = s.Mode, s.Mode == parser.DropCascade
	case *parser.AlterOperatorFamily:
		if s.Action == "drop" {
			domainMode = parser.DropRestrict
		}
	case *parser.DropSchema:
		cascade = s.Cascade
		if !s.Cascade {
			domainMode = parser.DropRestrict
		}
	}
	var removedRelations []ids.ObjectAddress
	for address, removal := range stage.Removals() {
		if _, ok := removal.Descriptor.(*catalog.Relation); ok {
			removedRelations = append(removedRelations, address)
		}
	}
	slices.SortFunc(removedRelations, func(a, b ids.ObjectAddress) int {
		if a.ObjectID < b.ObjectID {
			return -1
		}
		if a.ObjectID > b.ObjectID {
			return 1
		}
		return 0
	})
	for _, address := range removedRelations {
		ref := e.Catalog.Domain.Ref(stage.Base.Epoch(), address)
		if err := scope.AcquireSequence(ref, lock.AccessExclusive, true); err != nil {
			return core.Result{}, err
		}
	}
	if err := scope.StageExpressionDependents(stage, removedRelations, parser.DropCascade, nil); err != nil {
		return core.Result{}, err
	}
	if err = stageDomainDependents(scope, stage, domainMode); err != nil {
		return core.Result{}, err
	}
	if err := scope.StageOwnedSequences(stage, removedRelations, parser.DropCascade, nil); err != nil {
		return core.Result{}, err
	}
	stage.SchemaCascadeNames = append(stage.SchemaCascadeNames, stage.DropCascadeNames...)
	for a := range stage.Removals() {
		if r, ok := stage.Base.Relation(a); ok && r.RelationKind == catalog.SequenceRelation {
			if err := scope.AcquireSequence(e.Catalog.Domain.Ref(stage.Base.Epoch(), a), lock.AccessExclusive, true); err != nil {
				return core.Result{}, err
			}
		}
	}
	if _, ok := stmt.(*parser.CreateSchema); ok {
		if err := scope.AcquireDefinitionProviders(stage); err != nil {
			return core.Result{}, err
		}
	}
	if scope.ReplacementRead {
		e.ReplacementMu.RUnlock()
		scope.ReplacementRead = false
	}
	prepared, err := e.ValidateCatalogStage(op.Context(), stage)
	if err != nil {
		return core.Result{}, err
	}
	_, err = e.PublishCatalogOperation(op, scope, prepared, core.PublicationToken{Epoch: stage.Base.Epoch()})
	if creation, ok := stmt.(*parser.CreateSchema); ok && creation.IfNotExists && err != nil {
		skipped, failure := skipConcurrentSchema(scope, stage, creation, err)
		if skipped {
			return core.Result{}, nil
		}
		err = failure
	}
	if err == nil {
		for _, descriptor := range stage.Descriptors() {
			if ns, ok := descriptor.(*catalog.Namespace); ok && !ns.TemporaryToast && ns.Temporary == op.Session.SessionToken {
				op.Session.TemporaryNamespace = ns.Address()
			}
		}
		if cascade {
			details := stage.SchemaCascadeNames
			if len(details) > 0 {
				message := details[0]
				detail := ""
				if len(details) > 1 {
					message = fmt.Sprintf("drop cascades to %d other objects", len(details))
					detail = strings.Join(details, "\n")
				}
				op.EmitNotice(session.LevelNotice, sqlstate.Error{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: message, Detail: detail})
			}
		}
	}
	if err != nil {
		return core.Result{}, err
	}
	return core.Result{Created: created}, nil
}

// Preparation owns the normal candidate, including any newly materialized TEMP
// namespace. No collation OID or provider instance is allocated before admission.
func prepareCollationCreateLocked(e *core.Engine, op *session.Operation, stmt *parser.CreateCollation) (*core.CatalogStage, typedef.CollationCreatePlan, error) {
	auth, err := schemaAuthorizationLocked(e, op)
	if err != nil {
		return nil, typedef.CollationCreatePlan{}, err
	}
	stage := e.BeginCatalogStage(auth.Catalog)
	stage.AuthorizeLocked(auth, op.Session)
	plan, err := typedef.PrepareCreateCollationLocked(tablecmds.TypeHost{tablecmds.TableHost{core.DDLHost{E: e, Op: op, CatalogStage: stage}}}, auth, stmt)
	return stage, plan, err
}

func completeCollationCreateLocked(e *core.Engine, op *session.Operation, prepared *core.CatalogStage, plan *typedef.CollationCreatePlan) (*core.CatalogStage, error) {
	auth, err := schemaAuthorizationLocked(e, op)
	if err != nil {
		return prepared, err
	}
	if auth.Catalog.Domain() != prepared.Base.Domain() || auth.Catalog.Epoch() != prepared.Base.Epoch() {
		return prepared, ids.ErrCatalogReplaced
	}
	stage := prepared
	if auth.Catalog.State() != prepared.Base.State() {
		stage = e.BeginCatalogStage(auth.Catalog)
		for _, descriptor := range prepared.Descriptors() {
			if err = stage.Create(descriptor); err != nil {
				e.DiscardCatalogStage(stage)
				return prepared, err
			}
		}
		e.DiscardCatalogStage(prepared)
	}
	stage.AuthorizeLocked(auth, op.Session)
	host := tablecmds.TypeHost{tablecmds.TableHost{core.DDLHost{E: e, Op: op, CatalogStage: stage}}}
	err = typedef.StagePreparedCollationLocked(host, auth, plan)
	return stage, err
}

// schemaAuthorizationLocked supplies the same current authority and early
// context/actor gates to both ordinary schema staging and admitted schema DROP.
func schemaAuthorizationLocked(e *core.Engine, op *session.Operation) (core.AuthorizationView, error) {
	auth, err := e.AuthorizationLocked(op)
	if err != nil {
		return core.AuthorizationView{}, err
	}
	if err := op.Context().Err(); err != nil {
		return core.AuthorizationView{}, err
	}
	if _, err := auth.Roles.Descriptor(auth.State.CurrentUser); err != nil {
		return core.AuthorizationView{}, err
	}
	return auth, nil
}

// stageSchemaDropLocked recaptures authority, validates the whole admitted
// manifest and constructs its exact target stage without an unlocked handoff.
func stageSchemaDropLocked(e *core.Engine, op *session.Operation, stmt *parser.DropSchema, manifest ddl.SchemaDropManifest) (*core.CatalogStage, error) {
	auth, err := schemaAuthorizationLocked(e, op)
	if err != nil {
		return nil, err
	}
	stage := e.BeginCatalogStage(auth.Catalog)
	stage.AuthorizeLocked(auth, op.Session)
	if staged, err := ddl.StageDropSchema(SchemaHost{core.DDLHost{E: e, Op: op, CatalogStage: stage}}, auth, stmt, manifest); err != nil || !staged {
		return nil, err
	}
	return stage, nil
}

func stageSchemaLocked(e *core.Engine, op *session.Operation, stmt parser.Statement, input sqltype.InputEnvironment, output sqltype.OutputEnvironment) (*core.CatalogStage, error) {
	auth, err := schemaAuthorizationLocked(e, op)
	if err != nil {
		return nil, err
	}
	stage := e.BeginCatalogStage(auth.Catalog)
	stage.AuthorizeLocked(auth, op.Session)
	host := core.DDLHost{E: e, Op: op, CatalogStage: stage}
	typeHost, routineHost, schemaHost := tablecmds.TypeHost{tablecmds.TableHost{host}}, RoutineHost{host}, SchemaHost{host}
	switch s := stmt.(type) {
	case *parser.CreateAccessMethod:
		if err = index.StageCreateAccessMethod(routineHost, auth, s); err != nil {
			return nil, err
		}
	case *parser.DropAccessMethod:
		if err = index.StageDropAccessMethod(routineHost, auth, s); err != nil {
			return nil, err
		}
	case *parser.CreateCast:
		if err = routine.StageCreateCast(routineHost, auth, s); err != nil {
			return nil, err
		}
	case *parser.DropCast:
		if err = routine.StageDropCast(routineHost, auth, s); err != nil {
			return nil, err
		}
	case *parser.CreateComposite:
		if err = typedef.StageCreateCompositeLocked(typeHost, auth, s); err != nil {
			return nil, err
		}
	case *parser.CreateCollation:
		if err = typedef.StageCreateCollationLocked(typeHost, auth, s); err != nil {
			return nil, err
		}
	case *parser.AlterCollation:
		if err = typedef.StageAlterCollationLocked(typeHost, auth, s); err != nil {
			return nil, err
		}
	case *parser.DropCollation:
		if err = typedef.StageDropCollationLocked(typeHost, auth, s); err != nil {
			return nil, err
		}
	case *parser.CreateConversion:
		if err = typedef.StageCreateConversionLocked(typeHost, auth, s); err != nil {
			return nil, err
		}
	case *parser.AlterConversion:
		if err = typedef.StageAlterConversionLocked(typeHost, auth, s); err != nil {
			return nil, err
		}
	case *parser.DropConversion:
		if err = typedef.StageDropConversionLocked(typeHost, auth, s); err != nil {
			return nil, err
		}
	case *parser.CreateAggregate:
		if err = routine.StageCreateAggregate(routineHost, auth, s, input); err != nil {
			return nil, err
		}
	case *parser.CreateFunction:
		if err = routine.StageCreateFunction(routineHost, auth, s, input); err != nil {
			return nil, err
		}
	case *parser.CreateLanguage:
		return nil, routine.RejectCreateLanguage(host, auth)
	case *parser.AlterRoutine:
		if err = routine.StageAlterRoutine(routineHost, auth, s); err != nil {
			return nil, err
		}
	case *parser.DropRoutine:
		if err = routine.StageDropRoutine(routineHost, auth, s); err != nil {
			return nil, err
		}
	case *parser.CreateOperatorFamily:
		if err = index.StageCreateOperatorFamily(routineHost, auth, s); err != nil {
			return nil, err
		}
	case *parser.AlterOperatorFamily:
		if err = index.StageAlterOperatorFamily(routineHost, auth, s); err != nil {
			return nil, err
		}
	case *parser.DropOperatorFamily:
		if err = index.StageDropOperatorFamily(routineHost, auth, s); err != nil {
			return nil, err
		}
	case *parser.CreateOperatorClass:
		if err = index.StageCreateOperatorClass(routineHost, auth, s); err != nil {
			return nil, err
		}
	case *parser.AlterOperatorClass:
		if err = index.StageAlterOperatorClass(routineHost, auth, s); err != nil {
			return nil, err
		}
	case *parser.DropOperatorClass:
		if err = index.StageDropOperatorClass(routineHost, auth, s); err != nil {
			return nil, err
		}
	case *parser.CreateOperator:
		if err = routine.StageCreateOperator(routineHost, auth, s); err != nil {
			return nil, err
		}
	case *parser.AlterOperator:
		if err = routine.StageAlterOperator(routineHost, auth, s); err != nil {
			return nil, err
		}
	case *parser.DropOperator:
		if err = routine.StageDropOperator(routineHost, auth, s); err != nil {
			return nil, err
		}
	case *parser.CreateDomain:
		if err = typedef.StageCreateDomainLocked(typeHost, auth, s, input, output); err != nil {
			return nil, err
		}
	case *parser.AlterDomain:
		if err = typedef.StageAlterDomainIdentityLocked(typeHost, auth, s); err != nil {
			return nil, err
		}
	case *parser.DropDomain:
		if err = typedef.StageDropDomainLocked(typeHost, auth, s); err != nil {
			return nil, err
		}
	case *parser.AlterType:
		if err = typedef.StageAlterTypeIdentityLocked(typeHost, auth, s); err != nil {
			return nil, err
		}
	case *parser.DropType:
		if err = typedef.StageDropTypeLocked(typeHost, auth, s); err != nil {
			return nil, err
		}
	case *parser.CreateSchema:
		if staged, err := ddl.StageCreateSchema(schemaHost, auth, s, input); err != nil || !staged {
			return nil, err
		}
	case *parser.AlterSchema:
		if staged, err := ddl.StageAlterSchema(host, auth, s); err != nil || !staged {
			return nil, err
		}
	case *parser.DropSchema:
		manifest, err := ddl.CaptureSchemaDrop(host, auth, s)
		if err != nil {
			return nil, err
		}
		if staged, err := ddl.StageDropSchema(schemaHost, auth, s, manifest); err != nil || !staged {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("stuntgres: invalid schema statement")
	}
	return stage, nil
}
