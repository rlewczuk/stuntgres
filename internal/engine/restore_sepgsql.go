package engine

import (
	"context"
	"fmt"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/control"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
)

func decodeRestoreSepgsqlRequirement(r *restoreRun, comment string, _ parser.Position) (bool, error) {
	requirement, recognized, err := control.DecodeSepgsqlRequirement(comment)
	if err != nil || !recognized {
		return recognized, err
	}
	if r.sepgsqlRequirement != nil {
		return true, fmt.Errorf("stuntgres: duplicate sepgsql simulation requirement")
	}
	if err = control.ValidateSepgsqlRequirement(requirement, r.stage.Sepgsql); err != nil {
		return true, err
	}
	r.sepgsqlRequirement = requirement
	return true, nil
}
func admitRestoreSepgsqlLabel(r *restoreRun, _ *session.Operation, stmt parser.Statement) (control.Admission, error) {
	label, ok := stmt.(*parser.SecurityLabel)
	if !ok || r.sepgsqlRequirement == nil || !label.ProviderSpecified || label.Provider.Text != "selinux" {
		return control.NotHandled, nil
	}
	return control.Admitted, nil
}

func prepareRestoreSepgsqlLabel(r *restoreRun, _ *session.Operation, stmt parser.Statement) error {
	label, ok := stmt.(*parser.SecurityLabel)
	if !ok {
		return nil
	}
	if r.sepgsqlRequirement == nil || !label.ProviderSpecified || label.Provider.Text != "selinux" {
		return fmt.Errorf("stuntgres: security label replay requires compatible simulation metadata")
	}
	if label.Kind == "database" && len(label.Name.Parts) == 1 && label.Name.Parts[0].Text == r.sepgsqlRequirement.Database {
		label.Name.Parts = []parser.Identifier{{Text: r.stage.DatabaseName}}
	}
	return nil
}
func finishRestoreSepgsql(r *restoreRun, ctx context.Context, op *session.Operation) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	used := r.stage.Catalog.State.SecurityLabelCount() != 0
	for _, d := range r.stage.Catalog.State.Descriptors() {
		if p, ok := d.(*catalog.Procedure); ok && p.Sepgsql != nil {
			used = true
		}
	}
	if used && r.sepgsqlRequirement == nil {
		return fmt.Errorf("stuntgres: missing sepgsql simulation requirement")
	}
	if r.sepgsqlRequirement != nil {
		if err := control.ValidateSepgsqlRequirement(r.sepgsqlRequirement, r.stage.Sepgsql); err != nil {
			return err
		}
		if len(r.sepgsqlRequirement.RetiredColumns) != 0 {
			if err := restoreSepgsqlRetiredColumns(r, ctx, op); err != nil {
				return err
			}
		}
	}
	return catalog.ValidateSecurityLabels(r.stage.CatalogRead())
}

// Only the private native replay owner can import non-SQL-addressable labels.
// Attribute history has finished before this hook; final replacement still
// checks destination transfer authority.
func restoreSepgsqlRetiredColumns(r *restoreRun, ctx context.Context, op *session.Operation) error {
	e := r.stage
	e.Mu.Lock()
	auth, err := e.AuthorizationLocked(op)
	if err != nil {
		e.Mu.Unlock()
		return err
	}
	stage := e.BeginCatalogStage(auth.Catalog)
	stage.AuthorizeLocked(auth, op.Session)
	epoch := auth.Catalog.Epoch()
	for _, record := range r.sepgsqlRequirement.RetiredColumns {
		if err = ctx.Err(); err != nil {
			break
		}
		namespace := auth.Catalog.NameAddress(ids.NameKey{ClassID: ids.NamespaceClassID, Name: record.Schema})
		address := auth.Catalog.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: namespace, Name: record.Relation})
		relation, ok := auth.Catalog.Relation(address)
		if !ok || namespace == (ids.ObjectAddress{}) || record.Attribute <= 0 {
			err = fmt.Errorf("stuntgres: missing retired-column security label target")
			break
		}
		retired, dropped := relation.ColumnMeta.Dropped[record.Attribute]
		address.SubID = record.Attribute
		key := catalog.SecurityLabelKey{Object: address, Provider: "selinux"}
		if !dropped || !retired.Dropped || retired.Number != record.Attribute ||
			relation.IndexForAttribute(record.Attribute) >= 0 || catalog.SepgsqlClass(relation, record.Attribute) != "db_column" ||
			stage.SecurityLabel(key) != nil {
			err = fmt.Errorf("stuntgres: invalid retired-column security label target")
			break
		}
		stage.ObserveSecurityLabel(key)
		stage.RecordSecurityLabel(key, &catalog.SecurityLabelRecord{Label: record.Label, PolicyID: e.Sepgsql.ID(), PolicyVersion: e.Sepgsql.Version()})
	}
	e.Mu.Unlock()
	defer e.DiscardCatalogStage(stage)
	if err != nil {
		return err
	}
	prepared, err := e.ValidateCatalogStage(ctx, stage)
	if err != nil {
		return err
	}
	_, err = e.PublishCatalogOperation(op, nil, prepared, core.PublicationToken{Epoch: epoch})
	return err
}
