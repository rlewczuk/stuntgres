package engine

import (
	"context"
	"fmt"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/control"
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
func finishRestoreSepgsql(r *restoreRun, ctx context.Context, _ *session.Operation) error {
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
	}
	return catalog.ValidateSecurityLabels(r.stage.CatalogRead())
}
