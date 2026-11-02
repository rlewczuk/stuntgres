package engine

import (
	"context"
	"errors"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/fdw"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
)

func decodeRestoreExternalRequirement(r *restoreRun, comment string, _ parser.Position) (bool, error) {
	required, recognized, err := fdw.DecodeExternalRequirement(comment)
	if err != nil || !recognized {
		return recognized, err
	}
	if r.externalRequirements[required.Handler] {
		return true, errors.New("stuntgres: duplicate external SQL prerequisite")
	}
	if err := r.stage.Foreign.ValidateExternalRequirement(required); err != nil {
		return true, err
	}
	if r.externalRequirements == nil {
		r.externalRequirements = make(map[catalog.ForeignFunctionName]bool)
	}
	r.externalRequirements[required.Handler] = true
	return true, nil
}

func finishRestoreExternalRequirements(r *restoreRun, ctx context.Context, _ *session.Operation) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	for _, d := range r.stage.Catalog.State.Descriptors() {
		wrapper, ok := d.(*catalog.ForeignObject)
		if !ok || wrapper.Address().ClassID != ids.ForeignWrapperClassID || wrapper.Handler == (ids.ObjectAddress{}) {
			continue
		}
		p := r.stage.Catalog.State.MustProcedure(wrapper.Handler)
		if p.Foreign != nil && p.Foreign.ExternalProvider != nil && !r.externalRequirements[p.Foreign.Name] {
			return errors.New("stuntgres: missing external SQL prerequisite")
		}
	}
	return nil
}
