package engine

import (
	"context"
	"strings"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/dump"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/engine/sequence"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/typedef"
	"stuntgres/internal/parser"
)

// The inline entries hold the Restore steps not yet split into named hook
// table entries, in their original order.

func (r *restoreRun) inlineBefore(op *session.Operation, stmt parser.Statement) (bool, error) {
	if routine.ReplayDirective(r.source, stmt) {
		operationReplay(op).sqlBodies = true
		return true, nil
	}
	return false, nil
}

func (r *restoreRun) inlineBefore2(op *session.Operation, stmt parser.Statement) (bool, error) {
	r.cur.priorAuthorization, r.cur.ownerForCreate, r.cur.ownerPair = r.lastAuthorization, r.createOwner, r.pairedOwner
	r.cur.domainOwnerPair = r.pairedDomainOwner
	r.pairedDomainOwner = typedef.RestoredDomainOwner{}
	r.lastAuthorization, r.createOwner = authorization.PrincipalRef{}, authorization.PrincipalRef{}
	r.pairedOwner = restoredInheritedOwner{}
	return false, nil
}

func (r *restoreRun) inlineAdmit(op *session.Operation, stmt parser.Statement) (restoreAdmission, error) {
	var err error
	switch s := stmt.(type) {
	case *parser.AlterOwner:
		if s.Kind == "domain" {
			if err := r.stage.restoreDomainOwner(op, s, r.cur.domainOwnerPair); err != nil {
				return restoreNotHandled, err
			}
		} else if err := r.stage.restoreInheritedOwner(op, s, r.cur.ownerPair); err != nil {
			return restoreNotHandled, err
		}
	case *parser.CreateTable:
		if declarations := foreignKeyDeclarations(s); len(declarations) != 0 {
			r.cur.pending, err = r.stage.captureRestoredForeignKeys(op, declarations)
			if err != nil {
				return restoreNotHandled, err
			}
			stripRestoreForeignKeys(s)
		}
		if s.Persistence == "temporary" || len(s.Name.Parts) == 2 && (s.Name.Parts[0].Text == "pg_temp" || strings.HasPrefix(s.Name.Parts[0].Text, "pg_temp_")) {
			return restoreNotHandled, sqlError("0A000", "restore cannot stage non-retractable effects")
		}
		for _, element := range s.Elements {
			if element.Column != nil {
				for _, constraint := range element.Column.Constraints {
					if err := dump.RestoreScalar(constraint.Default, true); err != nil {
						return restoreNotHandled, err
					}
					if constraint.Generation != nil {
						if err := dump.RestoreScalar(constraint.Generation.Expression, true); err != nil {
							return restoreNotHandled, err
						}
					}
					if constraint.Check != nil {
						if err := dump.RestoreScalar(constraint.Check.Expression, true); err != nil {
							return restoreNotHandled, err
						}
					}
				}
			}
			if element.Check != nil {
				if err := dump.RestoreScalar(element.Check.Expression, true); err != nil {
					return restoreNotHandled, err
				}
			}
			if element.Exclusion != nil {
				for _, key := range element.Exclusion.Index.Keys {
					if err := dump.RestoreScalar(key.Expression, true); err != nil {
						return restoreNotHandled, err
					}
				}
				if err := dump.RestoreScalar(element.Exclusion.Index.Predicate, true); err != nil {
					return restoreNotHandled, err
				}
			}
		}
	case *parser.AlterDomain:
		if err := restoreDomainAlter(s); err != nil {
			return restoreNotHandled, err
		}
	case *parser.CreateSchema, *parser.AlterSchema, *parser.DropSchema, *parser.CreateComposite, *parser.CreateEnum, *parser.CreateDomain, *parser.CreateCollation, *parser.AlterCollation, *parser.CreateConversion, *parser.AlterConversion:
	case *parser.CreateOperator, *parser.AlterOperator, *parser.DropOperator:
	case *parser.CreateFunction:
	case *parser.CreateExtension:
	case *parser.AlterView:
		if !s.Materialized || (s.Action != "depends" && s.Action != "no_depends") || s.IfExists {
			return restoreNotHandled, sqlError("0A000", "restore accepts only materialized extension dependency ALTER")
		}
	case *parser.CreateView:
		if s.Persistence == "temporary" || len(s.Name.Parts) == 2 && (s.Name.Parts[0].Text == "pg_temp" || strings.HasPrefix(s.Name.Parts[0].Text, "pg_temp_")) {
			return restoreNotHandled, sqlError("0A000", "restore cannot stage non-retractable effects")
		}
	case *parser.CreateQueryTable:
		if s.Form != parser.CreateMaterializedView || !s.NoData {
			return restoreNotHandled, sqlError("0A000", "restore accepts only unpopulated materialized view definitions")
		}
	case *parser.CreateIndex:
	case *parser.AlterIndex:
		if s.Action != "rename" && s.Action != "attach" {
			return restoreNotHandled, sqlError("0A000", "restore accepts only index rename and partition attachment")
		}
	case *parser.CreatePolicy:
	case *parser.CreateRule:
	case *parser.AlterTable:
		if r.cur.physicalConstructor != nil {
			break
		}
		if access.RowSecurityActions(s.Actions) {
			break
		}
		if len(s.Actions) == 1 && s.Actions[0].Action == parser.AddCheck && s.Actions[0].Check != nil && !s.Actions[0].Check.NotValid {
			if err := r.stage.restoreInheritedCheck(op, s, r.generationRepairs); err != nil {
				return restoreNotHandled, err
			}
			break
		}
		if err := restoreAlterConstraints(s); err != nil {
			return restoreNotHandled, err
		}
	case *parser.AlterTableOf:
		if s.Type == nil {
			return restoreNotHandled, sqlError("0A000", "restore accepts only typed-table attachments")
		}
	case *parser.AlterTableDetachPartition:
		if s.IfExists || s.Only || len(s.Name.Parts) != 2 || len(s.Partition.Parts) != 2 {
			return restoreNotHandled, sqlError("0A000", "restore accepts only unconditional qualified partition detachments")
		}
		record, captureErr := r.stage.capturePartitionReplayTeardown(op, s)
		if captureErr != nil {
			return restoreNotHandled, captureErr
		}
		if record.child.Domain() != nil {
			r.partitionTeardowns[record.child.Address] = record
		}
	case *parser.DropTable:
		if s.IfExists || s.Mode != parser.DropRestrict || len(s.Names) != 1 || len(s.Names[0].Parts) != 2 || !strings.HasPrefix(s.Names[0].Parts[1].Text, partitionReplayStem) {
			return restoreNotHandled, sqlError("0A000", "restore accepts only generated empty partition replay teardown")
		}
		if err := r.stage.validatePartitionReplayTeardown(op, s.Names[0], r.partitionTeardowns); err != nil {
			return restoreNotHandled, err
		}
	case *parser.AlterTableAttachPartition:
		if s.IfExists {
			return restoreNotHandled, sqlError("0A000", "restore accepts only unconditional partition attachments")
		}
		for _, tuple := range [2][]parser.PartitionDatum{s.Bound.Lower, s.Bound.Upper} {
			for _, datum := range tuple {
				if datum.Kind == 0 {
					if err := dump.RestoreScalar(datum.Expression, true); err != nil {
						return restoreNotHandled, err
					}
				}
			}
		}
	case *parser.CreateSequence:
		if s.Persistence == "temporary" {
			return restoreNotHandled, sqlError("0A000", "restore cannot stage non-retractable effects")
		}
	case *parser.AlterSequence:
		if !(s.Action == "" && len(s.Options) == 1 && s.Options[0].Name == "owned") {
			if err := r.stage.validateRestoreIdentityConfiguration(op, s); err != nil {
				return restoreNotHandled, err
			}
		}
	case *parser.AlterTableDefault:
		if !(s.Action == parser.SetDefault && s.Expression != nil) && !(s.Action == parser.DropDefault && s.Only && len(s.Name.Parts) == 2) {
			return restoreNotHandled, sqlError("0A000", "restore accepts only deferred SET DEFAULT or inherited ONLY DEFAULT alterations")
		}
	case *parser.Select:
		if !sequence.RestoresState(s) {
			return restoreNotHandled, sqlError("0A000", "restore accepts only generated sequence state")
		}
	case *parser.Comment:
		if s.Kind == parser.CommentRole {
			return restoreNotHandled, sqlError("0A000", "restore accepts only database-local COMMENT targets")
		}
	case *parser.SessionAuthorization:
		if s.Default {
			r.generationRepairs = nil
		}
		if !s.OwnerDirective && !s.Default {
			return restoreNotHandled, sqlError("0A000", "restore accepts only named SET SESSION AUTHORIZATION owner directives or DEFAULT")
		}
	case *parser.Update:
		var repairErr error
		if restoreCatalogName(s.Name, "pg_constraint") && len(s.Assignments) == 2 {
			repairErr = r.stage.restoreRetiredConstraint(op, s, r.historicalAuthority)
		} else if restoreCatalogName(s.Name, "pg_constraint") {
			repairErr = r.stage.restoreNoInheritNotNull(op, s, r.pairedNotNull)
			if repairErr == nil {
				delete(r.notNullOrigins, r.pairedNotNull.constraint)
			}
		} else {
			repairErr = r.stage.restoreAttributeRank(op, s)
		}
		r.pairedNotNull = restoredNotNull{}
		if repairErr != nil {
			return restoreNotHandled, repairErr
		}
		return restoreConsumed, nil
	case *parser.Delete:
		if err := r.stage.restorePhysicalNotNull(op, s, r.pairedPhysical); err != nil {
			return restoreNotHandled, err
		}
		if r.pairedPhysical.position == len(r.pairedPhysical.repairs) {
			r.physicalForeignProvider, err = r.stage.capturePhysicalForeignKeyProvider(op, r.pairedPhysical.repairs[0].relation, r.historicalRefs)
			if err != nil {
				return restoreNotHandled, err
			}
			if r.pairedPhysical.primary == nil {
				r.deferredPhysical = r.pairedPhysical
				r.deferredPhysical.ownerDirective = false
			}
			r.pairedPhysical = nil
		}
		r.pairedNotNull = restoredNotNull{}
		return restoreConsumed, nil
	case *parser.Insert:
		if len(s.With) != 0 {
			return restoreNotHandled, sqlError("0A000", "restore INSERT WITH clauses are unsupported")
		}
		if s.Returning != nil {
			return restoreNotHandled, sqlError("0A000", "restore INSERT RETURNING is unsupported")
		}
		if s.Conflict != nil {
			return restoreNotHandled, sqlError("0A000", "restore INSERT conflict clauses are unsupported")
		}
		if s.Source != nil {
			return restoreNotHandled, sqlError("0A000", "restore INSERT query sources are unsupported")
		}
		for _, target := range s.Targets {
			for _, step := range target.Steps {
				if step.Subscript == nil {
					continue
				}
				bound := step.Subscript
				if bound.Lower != nil {
					if err := dump.RestoreScalar(bound.Lower, false); err != nil {
						return restoreNotHandled, err
					}
				}
				if bound.Upper != nil {
					if err := dump.RestoreScalar(bound.Upper, false); err != nil {
						return restoreNotHandled, err
					}
				}
			}
		}
		for _, row := range s.Rows {
			for _, v := range row {
				if err := dump.RestoreScalar(v, false); err != nil {
					return restoreNotHandled, err
				}
			}
		}
	case *parser.ForeignDDL:
		return restoreNotHandled, sqlError("0A000", "restore cannot replay SQL/MED foreign metadata")
	default:
		return restoreNotHandled, sqlError("0A000", "restore accepts only CREATE/ALTER/DROP SCHEMA, CREATE TABLE/SEQUENCE/VIEW/INDEX/POLICY/RULE, materialized extension dependency ALTER, row-security/existing-index/eligible NOT VALID ALTER TABLE, INSERT, deferred reference DEFAULT, database-local COMMENT, generated sequence ownership/state and SET SESSION AUTHORIZATION")
	}
	return restoreAdmitted, err
}

func (r *restoreRun) inlinePrepare2(op *session.Operation, stmt parser.Statement) error {
	operationReplay(op).tableDestination = false
	if _, ok := stmt.(*parser.CreateTable); ok {
		operationReplay(op).tableDestination = true
	}
	if _, ok := stmt.(*parser.CreateDomain); ok {
		operationReplay(op).tableDestination = true
	}
	if _, ok := stmt.(*parser.CreateEnum); ok {
		operationReplay(op).tableDestination = true
	}
	if _, ok := stmt.(*parser.CreateComposite); ok {
		operationReplay(op).tableDestination = true
	}
	if _, ok := stmt.(*parser.CreatePolicy); ok {
		operationReplay(op).tableDestination = true
	}
	if _, ok := stmt.(*parser.CreateRule); ok {
		operationReplay(op).tableDestination = true
	}
	if _, ok := stmt.(*parser.CreateSequence); ok {
		operationReplay(op).tableDestination = true
	}
	if _, ok := stmt.(*parser.CreateView); ok {
		operationReplay(op).tableDestination = true
	}
	if _, ok := stmt.(*parser.CreateIndex); ok {
		operationReplay(op).tableDestination = true
	}
	if creation, ok := stmt.(*parser.CreateQueryTable); ok && creation.Form == parser.CreateMaterializedView {
		operationReplay(op).tableDestination = true
	}
	if _, ok := stmt.(*parser.CreateCollation); ok {
		operationReplay(op).tableDestination = true
	}
	if _, ok := stmt.(*parser.CreateConversion); ok {
		operationReplay(op).tableDestination = true
	}
	if _, ok := stmt.(*parser.AlterConversion); ok {
		operationReplay(op).tableDestination = true
	}
	if _, ok := stmt.(*parser.CreateOperator); ok {
		operationReplay(op).tableDestination = true
	}
	if _, ok := stmt.(*parser.AlterOperator); ok {
		operationReplay(op).tableDestination = true
	}
	if _, ok := stmt.(*parser.CreateExtension); ok {
		operationReplay(op).tableDestination = true
	}
	operationReplay(op).insertAuthority = false
	if _, ok := stmt.(*parser.Insert); ok {
		operationReplay(op).insertAuthority = true
	}
	operationReplay(op).commentAuthority = false
	if _, ok := stmt.(*parser.Comment); ok {
		operationReplay(op).commentAuthority = true
	}
	_, replayDefault := stmt.(*parser.AlterTableDefault)
	operationReplay(op).defaultReplay = replayDefault
	_, operationReplay(op).partitionAttach = stmt.(*parser.AlterTableAttachPartition)
	if alteration, ok := stmt.(*parser.AlterTable); ok {
		if err := r.stage.beginPartitionCheckReplay(op, alteration); err != nil {
			return err
		}
		if err := r.stage.beginPartitionUsingIndexReplay(op, alteration); err != nil {
			return err
		}
		if err := r.stage.beginPartitionUniqueReplay(op, alteration); err != nil {
			return err
		}
		if len(alteration.Actions) == 1 && alteration.Actions[0].Action == parser.AddUnique && operationReplayOrZero(op).partitionUnique == nil && r.cur.physicalConstructor == nil {
			return sqlError("0A000", "restore accepts only captured private virtual unique definitions")
		}
	}
	return nil
}

func (r *restoreRun) inlineAfter(op *session.Operation, stmt parser.Statement, result Result) error {
	var err error
	if directive, ok := stmt.(*parser.SessionAuthorization); ok && directive.OwnerDirective && !directive.Default {
		auth, authErr := operationAuthorization(op)
		if authErr != nil {
			return authErr
		}
		actor, roleErr := auth.Roles.Descriptor(auth.State.CurrentUser)
		if roleErr != nil {
			return roleErr
		}
		if actor.Superuser && r.cur.priorAuthorization != (authorization.PrincipalRef{}) && r.cur.priorAuthorization != actor.Ref {
			r.createOwner = r.cur.priorAuthorization
		}
		r.lastAuthorization = actor.Ref
	}
	if _, domain := stmt.(*parser.CreateDomain); domain && r.cur.ownerForCreate != (authorization.PrincipalRef{}) && result.created != (ids.ObjectRef{}) {
		r.pairedDomainOwner, err = r.stage.captureRestoredDomainOwner(op, result.created, r.cur.ownerForCreate)
		if err != nil {
			return err
		}
	}
	return nil
}

func (r *restoreRun) inlineFinish(ctx context.Context, op *session.Operation) error {
	if err := r.stage.importExtensionOwners(ctx, op, r.extensionOwners); err != nil {
		return err
	}
	if err := r.stage.importMaterializedSnapshots(ctx, op, r.snapshots); err != nil {
		return err
	}
	if err := r.stage.validateRestoredSQLRoutines(ctx, op); err != nil {
		return err
	}
	return nil
}
