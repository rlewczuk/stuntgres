package engine

import (
	"context"
	"errors"
	"slices"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
)

func (s *StatementScope) planForeignModification(ctx context.Context, source *exec.ForeignQuerySource, operation catalog.ForeignModifyOperation, copying bool) (*foreignModification, error) {
	s.foreignUnlocked()
	if err := ForeignOperationSupported(source.Wrapper, operation, source.Request.Table); err != nil {
		return nil, err
	}
	m := &foreignModification{Source: source, Request: catalog.ForeignModifyRequest{ForeignRequest: exec.CopyForeignRequest(source.Request), Operation: operation}, Copy: copying}
	if operation != catalog.ForeignInsert {
		provider, ok := source.Wrapper.(catalog.ForeignUpdateTargetProvider)
		if !ok {
			return nil, sqlError("0A000", "foreign modification requires row identity support")
		}
		targets, err := provider.AddForeignUpdateTargets(ctx, exec.CopyForeignRequest(source.Request), operation)
		if err != nil {
			return nil, exec.ForeignCallbackError(err)
		}
		if len(targets.Names) == 0 {
			return nil, errors.New("stuntgres: foreign row identity has no attributes")
		}
		targets.Names = slices.Clone(targets.Names)
		m.Request.Targets = targets
	}
	if !copying {
		if planner, ok := source.Wrapper.(catalog.ForeignModifyPlanner); ok {
			targets := m.Request.Targets
			targets.Names = slices.Clone(targets.Names)
			plan, err := planner.PlanForeignModify(ctx, exec.CopyForeignRequest(source.Request), operation, targets)
			if err != nil {
				return nil, exec.ForeignCallbackError(err)
			}
			m.Request.Plan = plan
		}
	}
	return m, nil
}
