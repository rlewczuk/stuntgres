package schemacmds

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/fdw"
	"stuntgres/internal/engine/ids"
)

// Both direct CREATE and imported CREATE use the same private construction
// phases. ALTER never enters this shell path. The caller owns stage cleanup.
func finishForeignTableCreation(e *core.Engine, scope *core.StatementScope, stage *core.CatalogStage, jobs []fdw.Validation) error {
	var address ids.ObjectAddress
	var creation *catalog.Relation
	for a, d := range stage.Descriptors() {
		if relation, ok := d.(*catalog.Relation); ok && relation.RelationKind == catalog.ForeignRelation && stage.Base.Get(a) == nil {
			if address != (ids.ObjectAddress{}) {
				return fmt.Errorf("stuntgres: multiple foreign creations in one stage")
			}
			address = a
			creation = relation
		}
	}
	if err := scope.AcquireDefinitionProviders(stage); err != nil {
		return err
	}
	// The table job resolves the wrapper's validator before any column schedule.
	needsValidation := len(jobs) != 0 && jobs[0].HasValidator()
	if address == (ids.ObjectAddress{}) || !needsValidation {
		// Without an invocation there is no phase observer. Preserve the
		// ordinary one-stage path, including IF NOT EXISTS admission.
		for _, scheduled := range jobs {
			if err := scope.Op.Context().Err(); err != nil {
				return err
			}
			if scheduled.Attribute == 0 {
				continue
			}
			_, options, err := fdw.PrepareColumnValidation(stage.Base, scheduled)
			if err != nil {
				return err
			}
			creation.Foreign.Columns[scheduled.Attribute] = options
		}
		prepared, err := e.ValidateCatalogStage(scope.Op.Context(), stage)
		if err != nil {
			return err
		}
		_, err = e.PublishCatalogOperation(scope.Op, scope, prepared, core.PublicationToken{Epoch: stage.Base.Epoch()})
		return err
	}
	var columns []int32
	for _, job := range jobs {
		if job.Attribute != 0 {
			columns = append(columns, job.Attribute)
		}
	}
	construction, err := scope.StageForeignConstruction(stage, address, columns)
	if err != nil {
		return err
	}
	for i := range jobs {
		if err := scope.Op.Context().Err(); err != nil {
			return err
		}
		var options []catalog.ForeignOption
		if jobs[i].Attribute != 0 {
			e.Mu.Lock()
			read := e.PrivateCatalogLocked(scope.Catalog)
			prepared, values, err := fdw.PrepareColumnValidation(read, jobs[i])
			e.Mu.Unlock()
			if err != nil {
				return err
			}
			jobs[i], options = prepared, values
		}
		if err := fdw.RunValidations(scope.Op.Context(), jobs[i:i+1], scope.InvokeForeignValidator); err != nil {
			return err
		}
		if err := construction.Advance(jobs[i].Attribute, options); err != nil {
			return err
		}
	}
	return construction.Complete()
}
