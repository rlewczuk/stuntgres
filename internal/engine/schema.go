package engine

import (
	"fmt"
	"strings"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ddl"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/typedef"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func (e *Engine) executeSchema(scope *StatementScope, op *session.Operation, stmt parser.Statement, count int, input sqltype.InputEnvironment) (Result, error) {
	if count != 0 {
		return Result{}, fmt.Errorf("stuntgres: got %d arguments, want 0", count)
	}
	if schema, ok := stmt.(*parser.CreateSchema); ok {
		for _, table := range schema.Tables {
			if table.PartitionOf != nil {
				return Result{}, expr.ErrorAt("0A000", "partition definitions in CREATE SCHEMA are unsupported", table.PartitionOf.Pos)
			}
			if table.PartitionBy != nil {
				return Result{}, expr.ErrorAt("0A000", "partition definitions in CREATE SCHEMA are unsupported", table.PartitionBy.Pos)
			}
		}
	}
	e.mu.Lock()
	stage, err := e.stageSchemaLocked(op, stmt, input, scope.output)
	e.mu.Unlock()
	if err != nil || stage == nil {
		return Result{}, err
	}
	// Retain the constructor's actual fresh domain identity for bounded Restore
	// ownership pairing; never resolve an arbitrary later object by name.
	var created ids.ObjectRef
	if _, domain := stmt.(*parser.CreateDomain); domain {
		for address, descriptor := range stage.Descriptors() {
			if typ, ok := descriptor.(*catalog.Type); ok && typ.SQLType.IsDomain() && stage.Base.Get(address) == nil {
				created = e.catalog.Domain.Ref(stage.Base.Epoch(), address)
				break
			}
		}
	}
	defer e.DiscardCatalogStage(stage)
	_, droppingTypedSource := stmt.(*parser.DropType)
	if err := scope.acquireTypedSources(stage, droppingTypedSource); err != nil {
		return Result{}, err
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
	if err := scope.stageExpressionDependents(stage, removedRelations, parser.DropCascade, nil); err != nil {
		return Result{}, err
	}
	if err = scope.stageDomainDependents(stage, domainMode); err != nil {
		return Result{}, err
	}
	if err := scope.stageOwnedSequences(stage, removedRelations, parser.DropCascade, nil); err != nil {
		return Result{}, err
	}
	stage.schemaCascadeNames = append(stage.schemaCascadeNames, stage.dropCascadeNames...)
	for a := range stage.Removals() {
		if r, ok := stage.Base.Relation(a); ok && r.RelationKind == catalog.SequenceRelation {
			if err := scope.acquireSequence(e.catalog.Domain.Ref(stage.Base.Epoch(), a), lock.AccessExclusive, true); err != nil {
				return Result{}, err
			}
		}
	}
	if _, ok := stmt.(*parser.CreateSchema); ok {
		if err := scope.acquireDefinitionProviders(stage); err != nil {
			return Result{}, err
		}
	}
	if scope.replacementRead {
		e.replacementMu.RUnlock()
		scope.replacementRead = false
	}
	prepared, err := e.ValidateCatalogStage(op.Context(), stage)
	if err != nil {
		return Result{}, err
	}
	_, err = e.publishCatalogOperation(op, scope, prepared, PublicationToken{Epoch: stage.Base.Epoch()})
	if err == nil {
		for _, descriptor := range stage.Descriptors() {
			if ns, ok := descriptor.(*catalog.Namespace); ok && !ns.TemporaryToast && ns.Temporary == op.Session.SessionToken {
				op.Session.TemporaryNamespace = ns.Address()
			}
		}
		if cascade {
			details := stage.schemaCascadeNames
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
		return Result{}, err
	}
	return Result{created: created}, nil
}
func (e *Engine) stageSchemaLocked(op *session.Operation, stmt parser.Statement, input sqltype.InputEnvironment, output sqltype.OutputEnvironment) (*CatalogStage, error) {
	auth, err := e.authorizationLocked(op)
	if err != nil {
		return nil, err
	}
	if err = op.Context().Err(); err != nil {
		return nil, err
	}
	// The actor's role descriptor gates every staged statement, not only the
	// schema ones.
	if _, err = auth.Roles.Descriptor(auth.State.CurrentUser); err != nil {
		return nil, err
	}
	stage := e.BeginCatalogStage(auth.Catalog)
	stage.authorizeLocked(auth, op.Session)
	host := ddlHost{e: e, op: op, stage: stage}
	switch s := stmt.(type) {
	case *parser.CreateAccessMethod:
		if err = index.StageCreateAccessMethod(host, auth, s); err != nil {
			return nil, err
		}
	case *parser.DropAccessMethod:
		if err = index.StageDropAccessMethod(host, auth, s); err != nil {
			return nil, err
		}
	case *parser.CreateCast:
		if err = routine.StageCreateCast(host, auth, s); err != nil {
			return nil, err
		}
	case *parser.DropCast:
		if err = routine.StageDropCast(host, auth, s); err != nil {
			return nil, err
		}
	case *parser.CreateComposite:
		if err = typedef.StageCreateCompositeLocked(host, auth, s); err != nil {
			return nil, err
		}
	case *parser.CreateCollation:
		if err = typedef.StageCreateCollationLocked(host, auth, s); err != nil {
			return nil, err
		}
	case *parser.AlterCollation:
		if err = typedef.StageAlterCollationLocked(host, auth, s); err != nil {
			return nil, err
		}
	case *parser.DropCollation:
		if err = typedef.StageDropCollationLocked(host, auth, s); err != nil {
			return nil, err
		}
	case *parser.CreateConversion:
		if err = typedef.StageCreateConversionLocked(host, auth, s); err != nil {
			return nil, err
		}
	case *parser.AlterConversion:
		if err = typedef.StageAlterConversionLocked(host, auth, s); err != nil {
			return nil, err
		}
	case *parser.DropConversion:
		if err = typedef.StageDropConversionLocked(host, auth, s); err != nil {
			return nil, err
		}
	case *parser.CreateAggregate:
		if err = routine.StageCreateAggregate(host, auth, s, input); err != nil {
			return nil, err
		}
	case *parser.CreateFunction:
		if err = routine.StageCreateFunction(host, auth, s, input); err != nil {
			return nil, err
		}
	case *parser.CreateLanguage:
		return nil, routine.RejectCreateLanguage(host, auth)
	case *parser.AlterRoutine:
		if err = routine.StageAlterRoutine(host, auth, s); err != nil {
			return nil, err
		}
	case *parser.DropRoutine:
		if err = routine.StageDropRoutine(host, auth, s); err != nil {
			return nil, err
		}
	case *parser.CreateOperatorFamily:
		if err = index.StageCreateOperatorFamily(host, auth, s); err != nil {
			return nil, err
		}
	case *parser.AlterOperatorFamily:
		if err = index.StageAlterOperatorFamily(host, auth, s); err != nil {
			return nil, err
		}
	case *parser.DropOperatorFamily:
		if err = index.StageDropOperatorFamily(host, auth, s); err != nil {
			return nil, err
		}
	case *parser.CreateOperatorClass:
		if err = index.StageCreateOperatorClass(host, auth, s); err != nil {
			return nil, err
		}
	case *parser.AlterOperatorClass:
		if err = index.StageAlterOperatorClass(host, auth, s); err != nil {
			return nil, err
		}
	case *parser.DropOperatorClass:
		if err = index.StageDropOperatorClass(host, auth, s); err != nil {
			return nil, err
		}
	case *parser.CreateOperator:
		if err = routine.StageCreateOperator(host, auth, s); err != nil {
			return nil, err
		}
	case *parser.AlterOperator:
		if err = routine.StageAlterOperator(host, auth, s); err != nil {
			return nil, err
		}
	case *parser.DropOperator:
		if err = routine.StageDropOperator(host, auth, s); err != nil {
			return nil, err
		}
	case *parser.CreateDomain:
		if err = typedef.StageCreateDomainLocked(host, auth, s, input, output); err != nil {
			return nil, err
		}
	case *parser.AlterDomain:
		if err = typedef.StageAlterDomainIdentityLocked(host, auth, s); err != nil {
			return nil, err
		}
	case *parser.DropDomain:
		if err = typedef.StageDropDomainLocked(host, auth, s); err != nil {
			return nil, err
		}
	case *parser.AlterType:
		if err = typedef.StageAlterTypeIdentityLocked(host, auth, s); err != nil {
			return nil, err
		}
	case *parser.DropType:
		if err = typedef.StageDropTypeLocked(host, auth, s); err != nil {
			return nil, err
		}
	case *parser.CreateSchema:
		if staged, err := ddl.StageCreateSchema(host, auth, s, input); err != nil || !staged {
			return nil, err
		}
	case *parser.AlterSchema:
		if staged, err := ddl.StageAlterSchema(host, auth, s); err != nil || !staged {
			return nil, err
		}
	case *parser.DropSchema:
		if staged, err := ddl.StageDropSchema(host, auth, s); err != nil || !staged {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("stuntgres: invalid schema statement")
	}
	return stage, nil
}
