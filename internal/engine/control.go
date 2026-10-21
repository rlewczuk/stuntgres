package engine

import (
	"context"
	"fmt"
	"io"
	"slices"
	"strings"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/dump"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/regobj"
	"stuntgres/internal/engine/session"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/engine/sequence"
	"stuntgres/internal/engine/typedef"
	"stuntgres/internal/parser"
)

func (e *Engine) Reset(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	fresh := NewWithSettings(nil, e.databaseName, false)
	if err := fresh.InstallForeignDefinitions(e.foreign.Definitions()); err != nil {
		return err
	}
	_, err := e.ReplaceCatalog(ctx, fresh, fresh.CatalogRead())
	return err
}

// Namespace ACL entry order and split entries do not affect reproducibility.
// Principal/grantor references do: losing them would change DROP dependencies.
func bootstrapNamespaceDumpable(d *catalog.Namespace) bool {
	if d.Address().ObjectID == ids.PGToastNamespaceOID {
		return !d.ACL.Explicit && len(d.ACL.Entries) == 0
	}
	if !d.ACL.Explicit {
		return false
	}
	publicRights := authorization.UsagePrivilege
	var publicUse, publicOptions, ownerUse, ownerOptions authorization.Privileges
	for _, entry := range d.ACL.Entries {
		if entry.Grantor != d.Owner {
			return false
		}
		if entry.Grantee.Public {
			if entry.Grantee.Role != (authorization.PrincipalRef{}) {
				return false
			}
			publicUse |= entry.Privileges
			publicOptions |= entry.GrantOptions
		} else {
			if entry.Grantee.Role != d.Owner {
				return false
			}
			ownerUse |= entry.Privileges
			ownerOptions |= entry.GrantOptions
		}
	}
	return publicUse == publicRights && publicOptions == 0 &&
		ownerUse == authorization.UsagePrivilege|authorization.CreatePrivilege && ownerOptions == 0
}

// Dump emits replayable logical definitions and rows, including renamed,
// manual, partial and nested partitioned key/index trees. Virtual indexes have
// no physical roots; their actual ancestry and incomplete validity are retained.
//
// Dump reads engine state only in captureDumpLocked, under Engine.mu, through
// the Collect tables (dumpCollectors). It then orders the captured graph and
// writes dumpSections, whose event section uses the Emit table
// (dumpPhaseEmitters).
func (e *Engine) Dump(ctx context.Context, dst io.Writer) error {
	if dst == nil {
		return fmt.Errorf("stuntgres: nil dump writer")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	e.mu.Lock()
	capture, err := e.captureDumpLocked(ctx)
	e.mu.Unlock()
	if err != nil {
		return err
	}
	ctx = regobj.ACLItemOutputContext(ctx, capture.aclOutput)
	script, err := buildDumpScript(ctx, capture)
	if err != nil {
		return err
	}
	w := &dumpWriter{ctx: ctx, dst: dst}
	for _, section := range dumpSections {
		if err := section.emit(ctx, script, w); err != nil {
			return err
		}
	}
	return ctx.Err()
}

type contextReader struct {
	ctx context.Context
	src io.Reader
}

func (r contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	n, err := r.src.Read(p)
	if canceled := r.ctx.Err(); canceled != nil {
		return n, canceled
	}
	return n, err
}

// Restore replaces the persistent logical catalog atomically from Dump SQL.
// Partitioned key/index references are reminted against retained live TEMP
// objects; failed or canceled replacement leaves the old tree usable.
// Native replacement is not a PostgreSQL control API.
func (e *Engine) Restore(ctx context.Context, src io.Reader) error {
	if src == nil {
		return fmt.Errorf("stuntgres: nil restore reader")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	stage := e.restoreStage()
	data, err := io.ReadAll(contextReader{ctx, src})
	if err != nil {
		return err
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	r := &restoreRun{stage: stage, source: string(data)}
	r.statements, err = parser.ParseWithOptions(r.source, parser.Options{Context: ctx, StandardConformingStrings: true, LineComment: func(comment string, position parser.Position) error {
		domainRecord, domainRecognized, domainErr := typedef.DecodeDomainReplay(comment)
		if domainErr != nil {
			return domainErr
		}
		if domainRecognized {
			r.domainReplays = append(r.domainReplays, typedef.PositionedDomainReplay{Record: domainRecord, End: position.Offset + len(comment)})
			return nil
		}
		ownerRecord, ownerRecognized, ownerErr := extension.DecodeOwners(comment)
		if ownerErr != nil {
			return ownerErr
		}
		if ownerRecognized {
			r.extensionOwners = append(r.extensionOwners, ownerRecord)
			return nil
		}
		record, recognized, err := decodeMaterializedSnapshot(comment)
		if err == nil && recognized {
			r.snapshots = append(r.snapshots, record)
		}
		return err
	}})
	if err != nil {
		return err
	}
	r.domainReplayPairs, err = typedef.PairDomainReplay(r.source, r.statements, r.domainReplays)
	if err != nil {
		return err
	}
	// The isolated role snapshot retains exact target identities for final remapping.
	sess, err := stage.StartSession(ctx, session.SessionStart{ID: 1, Authenticated: stage.roles.domain.Principal(10)})
	if err != nil {
		return err
	}
	defer func() { sess.RequestEnd(session.CleanClose); _ = sess.WaitEnd(context.Background()) }()
	op, err := sess.Acquire(ctx)
	if err != nil {
		return err
	}
	defer op.Done()
	defer func() {
		operationReplay(op).sqlBodies = false
		operationReplay(op).defaultReplay = false
		operationReplay(op).partitionAttach = false
		operationReplay(op).partitionCheck = nil
		operationReplay(op).partitionUsingIndex = nil
		operationReplay(op).partitionUnique = nil
	}()
	r.startCaptures(e.retiredArchiveHighWater())
	for statementIndex, stmt := range r.statements {
		r.cur = restoreStatement{index: statementIndex}
		if routine.ReplayDirective(r.source, stmt) {
			operationReplay(op).sqlBodies = true
			continue
		}
		if r.pairedPhysical != nil && !r.pairedPhysical.admits(stmt) {
			return sqlError("0A000", restorePhysicalNotNullBoundary)
		}
		if alteration, ok := stmt.(*parser.AlterTable); ok {
			r.cur.physicalConstructor, err = stage.preparePhysicalNotNullConstructor(op, alteration, physicalNotNullFollowing(r.statements, r.cur.index), r.historicalRefs)
			if err != nil {
				return err
			}
			if r.cur.physicalConstructor == nil {
				r.cur.physicalConstructor, err = stage.prepareDeferredPhysicalPrimary(op, alteration, r.deferredPhysical, r.historicalRefs)
				if err != nil {
					return err
				}
			}
		}
		if directive, ok := stmt.(*parser.SessionAuthorization); ok && directive.OwnerDirective && !directive.Default && r.deferredPhysical != nil && !r.deferredPhysical.ownerDirective {
			r.deferredPhysical.ownerDirective = true
		} else {
			r.deferredPhysical = nil
		}
		if _, ok := stmt.(*parser.CreateIndex); ok {
			r.cur.physicalIndexBase = stage.CatalogRead()
		}
		r.cur.priorAuthorization, r.cur.ownerForCreate, r.cur.ownerPair = r.lastAuthorization, r.createOwner, r.pairedOwner
		r.cur.domainOwnerPair = r.pairedDomainOwner
		r.pairedDomainOwner = typedef.RestoredDomainOwner{}
		r.lastAuthorization, r.createOwner = authorization.PrincipalRef{}, authorization.PrincipalRef{}
		r.pairedOwner = restoredInheritedOwner{}
		if alteration, ok := stmt.(*parser.AlterTable); ok && r.cur.physicalConstructor == nil {
			declaration, admitted, captureErr := stage.captureInheritedForeignKey(op, alteration, r.historicalRefs)
			if captureErr != nil {
				return captureErr
			}
			if admitted {
				r.pendingForeignKeys = append(r.pendingForeignKeys, declaration)
				r.physicalForeignProvider = nil
				r.pairedNotNull = restoredNotNull{}
				continue
			}
		}
		if r.physicalForeignProvider != nil {
			if directive, ok := stmt.(*parser.SessionAuthorization); ok && directive.OwnerDirective && !directive.Default && !r.physicalForeignProvider.ownerDirective {
				r.physicalForeignProvider.ownerDirective = true
			} else if alteration, ok := stmt.(*parser.AlterTable); ok {
				declaration, admitted, captureErr := stage.capturePhysicalSelfForeignKey(op, alteration, r.physicalForeignProvider, r.historicalRefs)
				if captureErr != nil {
					return captureErr
				}
				if admitted {
					r.pendingForeignKeys = append(r.pendingForeignKeys, declaration)
					r.pairedNotNull = restoredNotNull{}
					continue
				}
				r.cur.physicalConstructor, err = stage.preparePhysicalCompetingUnique(op, alteration, r.physicalForeignProvider, r.historicalRefs)
				if err != nil {
					return err
				}
				if r.cur.physicalConstructor == nil {
					r.physicalForeignProvider = nil
				}
			} else {
				r.physicalForeignProvider = nil
			}
		}
		switch s := stmt.(type) {
		case *parser.AlterOwner:
			if s.Kind == "domain" {
				if err := stage.restoreDomainOwner(op, s, r.cur.domainOwnerPair); err != nil {
					return err
				}
			} else if err := stage.restoreInheritedOwner(op, s, r.cur.ownerPair); err != nil {
				return err
			}
		case *parser.CreateTable:
			if declarations := foreignKeyDeclarations(s); len(declarations) != 0 {
				r.cur.pending, err = stage.captureRestoredForeignKeys(op, declarations)
				if err != nil {
					return err
				}
				stripRestoreForeignKeys(s)
			}
			if s.Persistence == "temporary" || len(s.Name.Parts) == 2 && (s.Name.Parts[0].Text == "pg_temp" || strings.HasPrefix(s.Name.Parts[0].Text, "pg_temp_")) {
				return sqlError("0A000", "restore cannot stage non-retractable effects")
			}
			for _, element := range s.Elements {
				if element.Column != nil {
					for _, constraint := range element.Column.Constraints {
						if err := dump.RestoreScalar(constraint.Default, true); err != nil {
							return err
						}
						if constraint.Generation != nil {
							if err := dump.RestoreScalar(constraint.Generation.Expression, true); err != nil {
								return err
							}
						}
						if constraint.Check != nil {
							if err := dump.RestoreScalar(constraint.Check.Expression, true); err != nil {
								return err
							}
						}
					}
				}
				if element.Check != nil {
					if err := dump.RestoreScalar(element.Check.Expression, true); err != nil {
						return err
					}
				}
				if element.Exclusion != nil {
					for _, key := range element.Exclusion.Index.Keys {
						if err := dump.RestoreScalar(key.Expression, true); err != nil {
							return err
						}
					}
					if err := dump.RestoreScalar(element.Exclusion.Index.Predicate, true); err != nil {
						return err
					}
				}
			}
		case *parser.AlterDomain:
			if err := restoreDomainAlter(s); err != nil {
				return err
			}
		case *parser.CreateSchema, *parser.AlterSchema, *parser.DropSchema, *parser.CreateComposite, *parser.CreateEnum, *parser.CreateDomain, *parser.CreateCollation, *parser.AlterCollation, *parser.CreateConversion, *parser.AlterConversion:
		case *parser.CreateOperator, *parser.AlterOperator, *parser.DropOperator:
		case *parser.CreateFunction:
		case *parser.CreateExtension:
		case *parser.AlterView:
			if !s.Materialized || (s.Action != "depends" && s.Action != "no_depends") || s.IfExists {
				return sqlError("0A000", "restore accepts only materialized extension dependency ALTER")
			}
		case *parser.CreateView:
			if s.Persistence == "temporary" || len(s.Name.Parts) == 2 && (s.Name.Parts[0].Text == "pg_temp" || strings.HasPrefix(s.Name.Parts[0].Text, "pg_temp_")) {
				return sqlError("0A000", "restore cannot stage non-retractable effects")
			}
		case *parser.CreateQueryTable:
			if s.Form != parser.CreateMaterializedView || !s.NoData {
				return sqlError("0A000", "restore accepts only unpopulated materialized view definitions")
			}
		case *parser.CreateIndex:
		case *parser.AlterIndex:
			if s.Action != "rename" && s.Action != "attach" {
				return sqlError("0A000", "restore accepts only index rename and partition attachment")
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
				if err := stage.restoreInheritedCheck(op, s, r.generationRepairs); err != nil {
					return err
				}
				break
			}
			if err := restoreAlterConstraints(s); err != nil {
				return err
			}
		case *parser.AlterTableOf:
			if s.Type == nil {
				return sqlError("0A000", "restore accepts only typed-table attachments")
			}
		case *parser.AlterTableDetachPartition:
			if s.IfExists || s.Only || len(s.Name.Parts) != 2 || len(s.Partition.Parts) != 2 {
				return sqlError("0A000", "restore accepts only unconditional qualified partition detachments")
			}
			record, captureErr := stage.capturePartitionReplayTeardown(op, s)
			if captureErr != nil {
				return captureErr
			}
			if record.child.Domain() != nil {
				r.partitionTeardowns[record.child.Address] = record
			}
		case *parser.DropTable:
			if s.IfExists || s.Mode != parser.DropRestrict || len(s.Names) != 1 || len(s.Names[0].Parts) != 2 || !strings.HasPrefix(s.Names[0].Parts[1].Text, partitionReplayStem) {
				return sqlError("0A000", "restore accepts only generated empty partition replay teardown")
			}
			if err := stage.validatePartitionReplayTeardown(op, s.Names[0], r.partitionTeardowns); err != nil {
				return err
			}
		case *parser.AlterTableAttachPartition:
			if s.IfExists {
				return sqlError("0A000", "restore accepts only unconditional partition attachments")
			}
			for _, tuple := range [2][]parser.PartitionDatum{s.Bound.Lower, s.Bound.Upper} {
				for _, datum := range tuple {
					if datum.Kind == 0 {
						if err := dump.RestoreScalar(datum.Expression, true); err != nil {
							return err
						}
					}
				}
			}
		case *parser.CreateSequence:
			if s.Persistence == "temporary" {
				return sqlError("0A000", "restore cannot stage non-retractable effects")
			}
		case *parser.AlterSequence:
			if !(s.Action == "" && len(s.Options) == 1 && s.Options[0].Name == "owned") {
				if err := stage.validateRestoreIdentityConfiguration(op, s); err != nil {
					return err
				}
			}
		case *parser.AlterTableDefault:
			if !(s.Action == parser.SetDefault && s.Expression != nil) && !(s.Action == parser.DropDefault && s.Only && len(s.Name.Parts) == 2) {
				return sqlError("0A000", "restore accepts only deferred SET DEFAULT or inherited ONLY DEFAULT alterations")
			}
		case *parser.Select:
			if !sequence.RestoresState(s) {
				return sqlError("0A000", "restore accepts only generated sequence state")
			}
		case *parser.Comment:
			if s.Kind == parser.CommentRole {
				return sqlError("0A000", "restore accepts only database-local COMMENT targets")
			}
		case *parser.SessionAuthorization:
			if s.Default {
				r.generationRepairs = nil
			}
			if !s.OwnerDirective && !s.Default {
				return sqlError("0A000", "restore accepts only named SET SESSION AUTHORIZATION owner directives or DEFAULT")
			}
		case *parser.Update:
			var repairErr error
			if restoreCatalogName(s.Name, "pg_constraint") && len(s.Assignments) == 2 {
				repairErr = stage.restoreRetiredConstraint(op, s, r.historicalAuthority)
			} else if restoreCatalogName(s.Name, "pg_constraint") {
				repairErr = stage.restoreNoInheritNotNull(op, s, r.pairedNotNull)
				if repairErr == nil {
					delete(r.notNullOrigins, r.pairedNotNull.constraint)
				}
			} else {
				repairErr = stage.restoreAttributeRank(op, s)
			}
			r.pairedNotNull = restoredNotNull{}
			if repairErr != nil {
				return repairErr
			}
			continue
		case *parser.Delete:
			if err := stage.restorePhysicalNotNull(op, s, r.pairedPhysical); err != nil {
				return err
			}
			if r.pairedPhysical.position == len(r.pairedPhysical.repairs) {
				r.physicalForeignProvider, err = stage.capturePhysicalForeignKeyProvider(op, r.pairedPhysical.repairs[0].relation, r.historicalRefs)
				if err != nil {
					return err
				}
				if r.pairedPhysical.primary == nil {
					r.deferredPhysical = r.pairedPhysical
					r.deferredPhysical.ownerDirective = false
				}
				r.pairedPhysical = nil
			}
			r.pairedNotNull = restoredNotNull{}
			continue
		case *parser.Insert:
			if len(s.With) != 0 {
				return sqlError("0A000", "restore INSERT WITH clauses are unsupported")
			}
			if s.Returning != nil {
				return sqlError("0A000", "restore INSERT RETURNING is unsupported")
			}
			if s.Conflict != nil {
				return sqlError("0A000", "restore INSERT conflict clauses are unsupported")
			}
			if s.Source != nil {
				return sqlError("0A000", "restore INSERT query sources are unsupported")
			}
			for _, target := range s.Targets {
				for _, step := range target.Steps {
					if step.Subscript == nil {
						continue
					}
					bound := step.Subscript
					if bound.Lower != nil {
						if err := dump.RestoreScalar(bound.Lower, false); err != nil {
							return err
						}
					}
					if bound.Upper != nil {
						if err := dump.RestoreScalar(bound.Upper, false); err != nil {
							return err
						}
					}
				}
			}
			for _, row := range s.Rows {
				for _, v := range row {
					if err := dump.RestoreScalar(v, false); err != nil {
						return err
					}
				}
			}
		case *parser.ForeignDDL:
			return sqlError("0A000", "restore cannot replay SQL/MED foreign metadata")
		default:
			return sqlError("0A000", "restore accepts only CREATE/ALTER/DROP SCHEMA, CREATE TABLE/SEQUENCE/VIEW/INDEX/POLICY/RULE, materialized extension dependency ALTER, row-security/existing-index/eligible NOT VALID ALTER TABLE, INSERT, deferred reference DEFAULT, database-local COMMENT, generated sequence ownership/state and SET SESSION AUTHORIZATION")
		}
		if insertion, ok := stmt.(*parser.Insert); ok && len(r.generationRepairs) != 0 {
			r.generationRepairs, err = stage.closeRestoredRecipesForData(op, insertion, r.generationRepairs)
			if err != nil {
				return err
			}
		}
		if _, ownerDirective := stmt.(*parser.SessionAuthorization); !ownerDirective {
			r.pairedNotNull = restoredNotNull{}
		}
		if alteration, ok := stmt.(*parser.AlterTable); r.cur.physicalConstructor == nil && ok && len(alteration.Actions) == 1 && alteration.Actions[0].Action == parser.SetNotNull {
			r.pairedNotNull, err = stage.pairRestoredNotNull(op, alteration, r.notNullOrigins)
			if err != nil {
				return err
			}
		}
		if alteration, ok := stmt.(*parser.AlterTable); ok && len(alteration.Actions) == 1 && alteration.Actions[0].Action == parser.SetExpression {
			if err := stage.pairRestoredGeneration(op, alteration, r.placeholderOrigins); err != nil {
				return err
			}
		}
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
			if err := stage.beginPartitionCheckReplay(op, alteration); err != nil {
				return err
			}
			if err := stage.beginPartitionUsingIndexReplay(op, alteration); err != nil {
				return err
			}
			if err := stage.beginPartitionUniqueReplay(op, alteration); err != nil {
				return err
			}
			if len(alteration.Actions) == 1 && alteration.Actions[0].Action == parser.AddUnique && operationReplayOrZero(op).partitionUnique == nil && r.cur.physicalConstructor == nil {
				return sqlError("0A000", "restore accepts only captured private virtual unique definitions")
			}
		}
		var result Result
		if insertion, ok := stmt.(*parser.Insert); ok {
			if record, paired := r.domainReplayPairs[insertion]; paired {
				result, err = stage.executeDomainReplayInsert(op, insertion, record)
			} else {
				result, err = stage.Execute(op, stmt, 0, nil)
			}
		} else {
			result, err = stage.Execute(op, stmt, 0, nil)
		}
		operationReplay(op).defaultReplay = false
		operationReplay(op).partitionAttach = false
		operationReplay(op).partitionCheck = nil
		operationReplay(op).partitionUsingIndex = nil
		operationReplay(op).partitionUnique = nil
		if result.Rows != nil {
			closeErr := result.Rows.Close()
			if err == nil {
				err = closeErr
			}
		}
		operationReplay(op).commentAuthority = false
		op.DetachNotices()
		if err != nil {
			return err
		}
		if r.cur.physicalConstructor != nil {
			r.pairedPhysical, err = stage.capturePhysicalNotNullConstructor(op, r.cur.physicalConstructor, r.physicalIndexes)
			if err != nil {
				return err
			}
		}
		if alteration, ok := stmt.(*parser.AlterTable); ok && len(alteration.Actions) == 1 && r.pairedPhysical == nil && (alteration.Actions[0].Action == parser.AddUnique || alteration.Actions[0].Action == parser.AddUsingIndex) {
			r.physicalForeignProvider, err = stage.capturePhysicalForeignKeyProvider(op, alteration.Name, r.historicalRefs)
			if err != nil {
				return err
			}
			if r.physicalForeignProvider != nil && r.cur.physicalConstructor != nil && r.cur.physicalConstructor.competing {
				r.physicalForeignProvider.permuted = true
			}
		}
		if creation, ok := stmt.(*parser.CreateIndex); ok {
			if index := stage.capturePhysicalImportIndex(r.cur.physicalIndexBase, creation, r.historicalRefs); index != nil {
				r.physicalIndexes[index.Address()] = index
			}
		}
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
			r.pairedDomainOwner, err = stage.captureRestoredDomainOwner(op, result.created, r.cur.ownerForCreate)
			if err != nil {
				return err
			}
		}
		if creation, ok := stmt.(*parser.CreateTable); ok && result.created != (ids.ObjectRef{}) {
			r.historicalRefs[result.created.Address] = result.created
			captureHistoricalRelation(stage.CatalogRead(), result.created, r.historicalAuthority.facts)
			stage.captureRestoredNotNulls(result.created, creation, r.notNullOrigins)
			stage.captureRestoredPlaceholders(result.created, creation, r.placeholderOrigins)
			if r.cur.ownerForCreate != (authorization.PrincipalRef{}) && len(creation.Parents) != 0 {
				r.pairedOwner, err = stage.captureInheritedOwner(op, result.created, r.cur.ownerForCreate)
				if err != nil {
					return err
				}
			}
		}
		if alteration, altered := stmt.(*parser.AlterTable); altered && slices.ContainsFunc(alteration.Actions, func(a parser.AlterTableAction) bool {
			return a.Action == parser.AddCheck || a.Action == parser.AddNotNull || a.Action == parser.SetNotNull
		}) {
			stage.captureHistoricalFacts(r.historicalRefs, r.historicalAuthority.facts)
		}
		if result.created != (ids.ObjectRef{}) && len(r.cur.pending.definitions) != 0 {
			r.cur.pending.child = result.created
			if err := stage.resolveRestoredForeignKeyTargets(&r.cur.pending); err != nil {
				return err
			}
			r.pendingForeignKeys = append(r.pendingForeignKeys, r.cur.pending)
		}
		if alteration, ok := stmt.(*parser.AlterTableDefault); ok {
			if err := stage.validateRestoredDefault(op, alteration); err != nil {
				return err
			}
		}
		if alteration, ok := stmt.(*parser.AlterTable); ok && len(alteration.Actions) == 1 && alteration.Actions[0].Action == parser.SetExpression {
			record, captureErr := stage.captureRestoredGeneration(op, alteration)
			if captureErr != nil {
				return captureErr
			}
			r.generationRepairs = append(r.generationRepairs, record)
			delete(r.placeholderOrigins, restoredGenerationKey{record.ref.Address, record.attribute})
		}
	}
	if r.pairedPhysical != nil {
		return sqlError("0A000", restorePhysicalNotNullBoundary)
	}
	if err := stage.bindRestoredForeignKeys(ctx, op, r.pendingForeignKeys); err != nil {
		return err
	}
	if err := stage.importExtensionOwners(ctx, op, r.extensionOwners); err != nil {
		return err
	}
	if err := stage.importMaterializedSnapshots(ctx, op, r.snapshots); err != nil {
		return err
	}
	if err := stage.validateRestoredSQLRoutines(ctx, op); err != nil {
		return err
	}
	_, err = e.replaceCatalog(ctx, stage, stage.CatalogRead(), op)
	return err
}
