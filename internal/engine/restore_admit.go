package engine

import (
	"strings"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/control"
	"stuntgres/internal/engine/dump"
	"stuntgres/internal/engine/partition"
	"stuntgres/internal/engine/sequence"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/parser"
)

// Restore's statement allow-list: one admit entry per statement kind. A
// kind no entry admits is rejected with control's rejection text.

type restoreAdmitFunc = func(*restoreRun, *session.Operation, parser.Statement) (control.Admission, error)

// restoreAdmitKind admits every statement of kind T unchecked.
func restoreAdmitKind[T parser.Statement](_ *restoreRun, _ *session.Operation, stmt parser.Statement) (control.Admission, error) {
	if _, ok := stmt.(T); ok {
		return control.Admitted, nil
	}
	return control.NotHandled, nil
}

// restoreAdmitChecked admits a statement of kind T that check accepts.
func restoreAdmitChecked[T parser.Statement](check func(*restoreRun, *session.Operation, T) error) restoreAdmitFunc {
	return func(r *restoreRun, op *session.Operation, stmt parser.Statement) (control.Admission, error) {
		s, ok := stmt.(T)
		if !ok {
			return control.NotHandled, nil
		}
		if err := check(r, op, s); err != nil {
			return control.NotHandled, err
		}
		return control.Admitted, nil
	}
}

// restoreAdmitReplayed consumes a statement of kind T that replay applied
// itself; it does not execute.
func restoreAdmitReplayed[T parser.Statement](replay func(*restoreRun, *session.Operation, T) error) restoreAdmitFunc {
	return func(r *restoreRun, op *session.Operation, stmt parser.Statement) (control.Admission, error) {
		s, ok := stmt.(T)
		if !ok {
			return control.NotHandled, nil
		}
		if err := replay(r, op, s); err != nil {
			return control.NotHandled, err
		}
		return control.Consumed, nil
	}
}

// restoreAdmitSchemaObject admits the schema-level object DDL.
func restoreAdmitSchemaObject(_ *restoreRun, _ *session.Operation, stmt parser.Statement) (control.Admission, error) {
	switch stmt.(type) {
	case *parser.CreateSchema, *parser.AlterSchema, *parser.DropSchema, *parser.CreateComposite, *parser.CreateEnum, *parser.CreateDomain, *parser.CreateCollation, *parser.AlterCollation, *parser.CreateConversion, *parser.AlterConversion:
		return control.Admitted, nil
	}
	return control.NotHandled, nil
}

// restoreAdmitOperator admits operator DDL.
func restoreAdmitOperator(_ *restoreRun, _ *session.Operation, stmt parser.Statement) (control.Admission, error) {
	switch stmt.(type) {
	case *parser.CreateOperator, *parser.AlterOperator, *parser.DropOperator:
		return control.Admitted, nil
	}
	return control.NotHandled, nil
}

// restoreRejectForeignDDL rejects SQL/MED metadata.
func restoreRejectForeignDDL(_ *restoreRun, _ *session.Operation, stmt parser.Statement) (control.Admission, error) {
	if _, ok := stmt.(*parser.ForeignDDL); ok {
		return control.NotHandled, sqlError("0A000", "restore cannot replay SQL/MED foreign metadata")
	}
	return control.NotHandled, nil
}

// restoreStagesTemporary reports a TEMPORARY or pg_temp-qualified relation,
// which a private restore stage cannot retract.
func restoreStagesTemporary(persistence string, name parser.ObjectName) bool {
	return persistence == "temporary" || len(name.Parts) == 2 && (name.Parts[0].Text == "pg_temp" || strings.HasPrefix(name.Parts[0].Text, "pg_temp_"))
}

// admitRestoreAlterOwner pairs an ALTER OWNER with the domain or inheriting
// table its owner directive created.
func admitRestoreAlterOwner(r *restoreRun, op *session.Operation, s *parser.AlterOwner) error {
	if s.Kind == "domain" {
		return r.stage.restoreDomainOwner(op, s, r.cur.domainOwnerPair)
	}
	return r.stage.restoreInheritedOwner(op, s, r.cur.ownerPair)
}

// admitRestoreCreateTable captures and strips the table's foreign keys
// (bound at finish) and checks its stored expressions.
func admitRestoreCreateTable(r *restoreRun, op *session.Operation, s *parser.CreateTable) error {
	if declarations := table.ForeignKeyDeclarations(s); len(declarations) != 0 {
		var err error
		r.cur.pending, err = r.stage.captureRestoredForeignKeys(op, declarations)
		if err != nil {
			return err
		}
		table.StripRestoreForeignKeys(s)
	}
	if restoreStagesTemporary(s.Persistence, s.Name) {
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
	return nil
}

func admitRestoreAlterDomain(_ *restoreRun, _ *session.Operation, s *parser.AlterDomain) error {
	return restoreDomainAlter(s)
}

func admitRestoreAlterView(_ *restoreRun, _ *session.Operation, s *parser.AlterView) error {
	if !s.Materialized || (s.Action != "depends" && s.Action != "no_depends") || s.IfExists {
		return sqlError("0A000", "restore accepts only materialized extension dependency ALTER")
	}
	return nil
}

func admitRestoreCreateView(_ *restoreRun, _ *session.Operation, s *parser.CreateView) error {
	if restoreStagesTemporary(s.Persistence, s.Name) {
		return sqlError("0A000", "restore cannot stage non-retractable effects")
	}
	return nil
}

func admitRestoreCreateQueryTable(_ *restoreRun, _ *session.Operation, s *parser.CreateQueryTable) error {
	if s.Form != parser.CreateMaterializedView || !s.NoData {
		return sqlError("0A000", "restore accepts only unpopulated materialized view definitions")
	}
	return nil
}

func admitRestoreAlterIndex(_ *restoreRun, _ *session.Operation, s *parser.AlterIndex) error {
	if s.Action != "rename" && s.Action != "attach" {
		return sqlError("0A000", "restore accepts only index rename and partition attachment")
	}
	return nil
}

// admitRestoreAlterTable admits a physical constructor, row-security
// actions, an inherited CHECK repair or the restorable constraint forms.
func admitRestoreAlterTable(r *restoreRun, op *session.Operation, s *parser.AlterTable) error {
	if r.cur.physicalConstructor != nil || access.RowSecurityActions(s.Actions) {
		return nil
	}
	if len(s.Actions) == 1 && s.Actions[0].Action == parser.AddCheck && s.Actions[0].Check != nil && !s.Actions[0].Check.NotValid {
		return r.stage.restoreInheritedCheck(op, s, r.generationRepairs)
	}
	return table.RestoreAlterConstraints(s)
}

func admitRestoreAlterTableOf(_ *restoreRun, _ *session.Operation, s *parser.AlterTableOf) error {
	if s.Type == nil {
		return sqlError("0A000", "restore accepts only typed-table attachments")
	}
	return nil
}

// admitRestoreDetachPartition captures the teardown a later generated DROP
// TABLE completes.
func admitRestoreDetachPartition(r *restoreRun, op *session.Operation, s *parser.AlterTableDetachPartition) error {
	if s.IfExists || s.Only || len(s.Name.Parts) != 2 || len(s.Partition.Parts) != 2 {
		return sqlError("0A000", "restore accepts only unconditional qualified partition detachments")
	}
	record, err := r.stage.capturePartitionReplayTeardown(op, s)
	if err != nil {
		return err
	}
	if record.Child.Domain() != nil {
		r.partitionTeardowns[record.Child.Address] = record
	}
	return nil
}

// admitRestoreDropTable admits only a generated partition replay teardown.
func admitRestoreDropTable(r *restoreRun, op *session.Operation, s *parser.DropTable) error {
	if s.IfExists || s.Mode != parser.DropRestrict || len(s.Names) != 1 || len(s.Names[0].Parts) != 2 || !strings.HasPrefix(s.Names[0].Parts[1].Text, partition.ReplayStem) {
		return sqlError("0A000", "restore accepts only generated empty partition replay teardown")
	}
	return r.stage.validatePartitionReplayTeardown(op, s.Names[0], r.partitionTeardowns)
}

func admitRestoreAttachPartition(_ *restoreRun, _ *session.Operation, s *parser.AlterTableAttachPartition) error {
	if s.IfExists {
		return sqlError("0A000", "restore accepts only unconditional partition attachments")
	}
	for _, tuple := range [3][]parser.PartitionDatum{s.Bound.Lower, s.Bound.Upper, s.Bound.Values} {
		for _, datum := range tuple {
			if datum.Kind == 0 {
				if err := dump.RestoreScalar(datum.Expression, true); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func admitRestoreCreateSequence(_ *restoreRun, _ *session.Operation, s *parser.CreateSequence) error {
	if s.Persistence == "temporary" {
		return sqlError("0A000", "restore cannot stage non-retractable effects")
	}
	return nil
}

// admitRestoreAlterSequence admits OWNED BY, or an identity configuration
// the restored column matches.
func admitRestoreAlterSequence(r *restoreRun, op *session.Operation, s *parser.AlterSequence) error {
	if s.Action == "" && len(s.Options) == 1 && s.Options[0].Name == "owned" {
		return nil
	}
	return r.stage.validateRestoreIdentityConfiguration(op, s)
}

func admitRestoreAlterTableDefault(_ *restoreRun, _ *session.Operation, s *parser.AlterTableDefault) error {
	if !(s.Action == parser.SetDefault && s.Expression != nil) && !(s.Action == parser.DropDefault && s.Only && len(s.Name.Parts) == 2) {
		return sqlError("0A000", "restore accepts only deferred SET DEFAULT or inherited ONLY DEFAULT alterations")
	}
	return nil
}

func admitRestoreSelect(_ *restoreRun, _ *session.Operation, s *parser.Select) error {
	if !sequence.RestoresState(s) {
		return sqlError("0A000", "restore accepts only generated sequence state")
	}
	return nil
}

func admitRestoreComment(_ *restoreRun, _ *session.Operation, s *parser.Comment) error {
	if s.Kind == parser.CommentRole {
		return sqlError("0A000", "restore accepts only database-local COMMENT targets")
	}
	return nil
}

// admitRestoreSessionAuthorization admits owner directives and DEFAULT;
// DEFAULT also ends the open generation recipes.
func admitRestoreSessionAuthorization(r *restoreRun, _ *session.Operation, s *parser.SessionAuthorization) error {
	if s.Default {
		r.generationRepairs = nil
	}
	if !s.OwnerDirective && !s.Default {
		return sqlError("0A000", "restore accepts only named SET SESSION AUTHORIZATION owner directives or DEFAULT")
	}
	return nil
}

// replayRestoreCatalogUpdate applies a generated pg_constraint or
// pg_attribute repair: a retired constraint, a NO INHERIT NOT NULL or an
// attribute rank.
func replayRestoreCatalogUpdate(r *restoreRun, op *session.Operation, s *parser.Update) error {
	var err error
	if table.RestoreCatalogName(s.Name, "pg_constraint") && len(s.Assignments) == 2 {
		err = r.stage.restoreRetiredConstraint(op, s, r.historicalAuthority)
	} else if table.RestoreCatalogName(s.Name, "pg_constraint") {
		err = r.stage.restoreNoInheritNotNull(op, s, r.pairedNotNull)
		if err == nil {
			delete(r.notNullOrigins, r.pairedNotNull.Constraint)
		}
	} else {
		err = r.stage.restoreAttributeRank(op, s)
	}
	r.pairedNotNull = table.RestoredNotNull{}
	return err
}

// replayRestorePhysicalNotNull applies one physical NOT NULL repair; the
// last one hands its relation to a foreign key provider and may defer a
// primary key.
func replayRestorePhysicalNotNull(r *restoreRun, op *session.Operation, s *parser.Delete) error {
	if err := r.stage.restorePhysicalNotNull(op, s, r.pairedPhysical); err != nil {
		return err
	}
	if r.pairedPhysical.Position == len(r.pairedPhysical.Repairs) {
		var err error
		r.physicalForeignProvider, err = r.stage.capturePhysicalForeignKeyProvider(op, r.pairedPhysical.Repairs[0].Relation, r.historicalRefs)
		if err != nil {
			return err
		}
		if r.pairedPhysical.Primary == nil {
			r.deferredPhysical = r.pairedPhysical
			r.deferredPhysical.OwnerDirective = false
		}
		r.pairedPhysical = nil
	}
	r.pairedNotNull = table.RestoredNotNull{}
	return nil
}

// admitRestoreInsert admits a plain VALUES INSERT whose subscripts and
// values are Restore scalars.
func admitRestoreInsert(_ *restoreRun, _ *session.Operation, s *parser.Insert) error {
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
	return nil
}
