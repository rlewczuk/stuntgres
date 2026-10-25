package engine

import (
	"strings"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/control"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/dump"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/largeobject"
	"stuntgres/internal/engine/partition"
	"stuntgres/internal/engine/partitioncmds"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/engine/tablecmds"
	"stuntgres/internal/parser"
)

// Restore's statement allow-list: one admit entry per statement kind. A
// kind no entry admits is rejected with control's rejection text.

type restoreAdmitFunc = func(*restoreRun, *session.Operation, parser.Statement) (control.Admission, error)

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

// restoreAdmitForeignDDL admits declarations, owner transfers and the selected
// deferred default repairs. Binding validates each repair's actual eligibility.
func restoreAdmitForeignDDL(r *restoreRun, op *session.Operation, stmt parser.Statement) (control.Admission, error) {
	s, ok := stmt.(*parser.ForeignDDL)
	if !ok {
		return control.NotHandled, nil
	}
	if s.Action == "import" {
		return control.NotHandled, core.SQLError("0A000", "restore cannot replay IMPORT FOREIGN SCHEMA")
	}
	if s.Action == "create" && !s.IfExists && !s.IfNotExists {
		switch s.Kind {
		case "wrapper", "server", "mapping":
			return control.Admitted, nil
		case "table":
			if s.Table != nil {
				if err := admitRestoreCreateTable(r, op, s.Table); err != nil {
					return control.NotHandled, err
				}
				return control.Admitted, nil
			}
		}
	}
	if s.Action == "alter" && s.Owner != nil && !s.IfExists && !s.HandlerSet && !s.ValidatorSet && !s.VersionSet && len(s.Options) == 0 && len(s.TableActions) == 0 && s.Inner == nil && s.NewName.Text == "" {
		switch s.Kind {
		case "wrapper", "server", "table":
			return control.Admitted, nil
		}
	}
	if alteration := restoreForeignDefault(s); alteration != nil {
		if err := dump.RestoreScalar(alteration.Expression, true); err != nil {
			return control.NotHandled, err
		}
		return control.Admitted, nil
	}
	return control.NotHandled, core.SQLError("0A000", "restore accepts only SQL/MED declarations, owner transfers and deferred foreign defaults")
}

// Keep admission and post-bind validation on the same single-action shape.
// The foreign metadata executor, not ordinary storage ALTER TABLE, runs it.
func restoreForeignDefault(s *parser.ForeignDDL) *parser.AlterTableDefault {
	if s.Action != "alter" || s.Kind != "table" || len(s.Name.Parts) != 2 || s.IfExists || s.IfNotExists ||
		s.Owner != nil || s.Inner != nil || s.Table != nil || s.NewName.Text != "" ||
		s.HandlerSet || s.ValidatorSet || s.VersionSet || len(s.Options) != 0 || len(s.ColumnOptions) != 0 || len(s.TableActions) != 1 {
		return nil
	}
	action := s.TableActions[0]
	alteration := action.Default
	if action.Ordinary != nil || action.Column.Text != "" || len(action.Options) != 0 || len(action.ColumnOptions) != 0 ||
		alteration == nil || alteration.Action != parser.SetDefault || alteration.Expression == nil || alteration.Only || len(alteration.Name.Parts) != 0 {
		return nil
	}
	return alteration
}

// admitRestoreAlterOwner admits captured installed-type ownership or pairs an
// ALTER OWNER with the exact fresh type or inheriting table its directive created.
func admitRestoreAlterOwner(r *restoreRun, op *session.Operation, s *parser.AlterOwner) error {
	if s.Kind == "domain" || s.Kind == "type" && r.cur.typeOwnerPair.Kind == "type" {
		return restoreTypeOwner(r.stage, op, s, r.cur.typeOwnerPair)
	}
	if s.Kind == "type" {
		return admitRestoreInstalledTypeOwner(r, op, s)
	}
	return partitioncmds.RestoreInheritedOwner(r.stage, op, s, r.cur.ownerPair)
}

func admitRestoreInstalledTypeOwner(r *restoreRun, op *session.Operation, s *parser.AlterOwner) error {
	reject := func() error { return core.SQLError("0A000", table.RestoreInheritedOwnerBoundary) }
	if s.IfExists || len(s.Name.Parts) != 2 || s.Target.Identity != "" || s.Target.Name == "" {
		return reject()
	}
	auth, err := core.OperationAuthorization(op)
	if err != nil {
		return err
	}
	for _, record := range r.extensionOwners {
		if record.Name != "isn" {
			continue
		}
		address := auth.Catalog.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: record.Name})
		ext, ok := auth.Catalog.Extension(address)
		if !ok || ext.Version != record.Version || auth.Catalog.MustNamespace(ext.TargetNamespace).Name() != record.Schema {
			continue
		}
		owner, err := auth.Roles.Descriptor(ext.Owner)
		if err != nil {
			return err
		}
		if owner.Name != record.Owner {
			continue
		}
		roles, err := extension.InstalledTypes(auth.Catalog, ext)
		if err != nil { return err }
		for _, role := range roles {
			typ := role.Type
			if typ.Name() != s.Name.Parts[1].Text || auth.Catalog.MustNamespace(typ.Namespace()).Name() != s.Name.Parts[0].Text { continue }
			for _, member := range record.Members {
				if member.Kind == "type" && member.Name == role.Name && member.Signature == "" && member.Owner == s.Target.Name { return nil }
			}
		}
	}
	return reject()
}

// admitRestoreCreateTable captures and strips the table's foreign keys
// (bound at finish), then runs control's CREATE TABLE check.
func admitRestoreCreateTable(r *restoreRun, op *session.Operation, s *parser.CreateTable) error {
	if declarations := table.ForeignKeyDeclarations(s); len(declarations) != 0 {
		var err error
		r.cur.pending, err = tablecmds.CaptureRestoredForeignKeys(r.stage, op, declarations)
		if err != nil {
			return err
		}
		table.StripRestoreForeignKeys(s)
	}
	return control.CheckCreateTable(s)
}

// admitRestoreAlterTable admits a physical constructor, row-security actions,
// a valid CHECK (with inherited repair authority where needed), or the
// restorable constraint forms.
func admitRestoreAlterTable(r *restoreRun, op *session.Operation, s *parser.AlterTable) error {
	if r.cur.physicalConstructor != nil || access.RowSecurityActions(s.Actions) {
		return nil
	}
	if len(s.Actions) == 1 && s.Actions[0].Action == parser.SetRelationOptions && !s.IfExists && !s.Only && len(s.Name.Parts) == 2 {
		return nil // Execution uses the ordinary bounded validator and private stage.
	}
	if len(s.Actions) == 1 && s.Actions[0].Action == parser.AddCheck && s.Actions[0].Check != nil && !s.Actions[0].Check.NotValid {
		return partitioncmds.RestoreCheck(r.stage, op, s, r.generationRepairs)
	}
	return table.RestoreAlterConstraints(s)
}

// admitRestoreDetachPartition captures the teardown a later generated DROP
// TABLE completes.
func admitRestoreDetachPartition(r *restoreRun, op *session.Operation, s *parser.AlterTableDetachPartition) error {
	if s.IfExists || s.Only || len(s.Name.Parts) != 2 || len(s.Partition.Parts) != 2 {
		return core.SQLError("0A000", "restore accepts only unconditional qualified partition detachments")
	}
	record, err := partitioncmds.CapturePartitionReplayTeardown(r.stage, op, s)
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
		return core.SQLError("0A000", "restore accepts only generated empty partition replay teardown")
	}
	return partitioncmds.ValidatePartitionReplayTeardown(r.stage, op, s.Names[0], r.partitionTeardowns)
}

// admitRestoreAlterSequence admits OWNED BY, or an identity configuration
// the restored column matches.
func admitRestoreAlterSequence(r *restoreRun, op *session.Operation, s *parser.AlterSequence) error {
	if s.Action == "" && len(s.Options) == 1 && s.Options[0].Name == "owned" {
		return nil
	}
	return tablecmds.ValidateRestoreIdentityConfiguration(r.stage, op, s)
}

// admitRestoreSessionAuthorization admits owner directives and DEFAULT;
// DEFAULT also ends the open generation recipes.
func admitRestoreSessionAuthorization(r *restoreRun, _ *session.Operation, s *parser.SessionAuthorization) error {
	if s.Default {
		r.generationRepairs = nil
	}
	if !s.OwnerDirective && !s.Default {
		return core.SQLError("0A000", "restore accepts only named SET SESSION AUTHORIZATION owner directives or DEFAULT")
	}
	return nil
}

// replayRestoreCatalogUpdate applies a generated pg_constraint or
// pg_attribute repair: a retired constraint, a NO INHERIT NOT NULL or an
// attribute rank.
func replayRestoreCatalogUpdate(r *restoreRun, op *session.Operation, s *parser.Update) error {
	var err error
	if _, _, _, ok := largeobject.ACLRepair(s); ok {
		return restoreLargeObjectACL(r.stage, op, s)
	}
	if _, ok := control.ParseObjectACLRepair(s); ok {
		return restoreObjectACL(r.stage, op, s)
	}
	if table.RestoreCatalogName(s.Name, "pg_constraint") && len(s.Assignments) == 2 {
		err = partitioncmds.RestoreRetiredConstraint(r.stage, op, s, r.historicalAuthority)
	} else if table.RestoreCatalogName(s.Name, "pg_constraint") {
		err = tablecmds.RestoreNoInheritNotNull(r.stage, op, s, r.pairedNotNull)
		if err == nil {
			delete(r.notNullOrigins, r.pairedNotNull.Constraint)
		}
	} else {
		err = tablecmds.RestoreAttributeRank(r.stage, op, s)
	}
	r.pairedNotNull = table.RestoredNotNull{}
	return err
}

// replayRestorePhysicalNotNull applies one physical NOT NULL repair; the
// last one hands its relation to a foreign key provider and may defer a
// primary key.
func replayRestorePhysicalNotNull(r *restoreRun, op *session.Operation, s *parser.Delete) error {
	if err := tablecmds.RestorePhysicalNotNull(r.stage, op, s, r.pairedPhysical); err != nil {
		return err
	}
	if r.pairedPhysical.Position == len(r.pairedPhysical.Repairs) {
		var err error
		r.physicalForeignProvider, err = tablecmds.CapturePhysicalForeignKeyProvider(r.stage, op, r.pairedPhysical.Repairs[0].Relation, r.historicalRefs)
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
