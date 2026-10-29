package engine

import (
	"context"
	"fmt"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/host"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
)

func decodeRestoreHostRequirement(r *restoreRun, comment string, _ parser.Position) (bool, error) {
	requirement, recognized, err := host.DecodeRequirement(comment)
	if err != nil || !recognized {
		return recognized, err
	}
	if r.hostRequirements[requirement.Provider] {
		return true, fmt.Errorf("stuntgres: duplicate host runtime prerequisite")
	}
	if err = host.ValidateRequirement(requirement, r.stage.Catalog.HostRuntimes); err != nil {
		return true, err
	}
	if r.hostRequirements == nil {
		r.hostRequirements = make(map[string]bool)
	}
	r.hostRequirements[requirement.Provider] = true
	return true, nil
}
func finishRestoreHostRequirements(r *restoreRun, ctx context.Context, _ *session.Operation) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(r.hostRequirements) != 0 && (r.hostAbsences == nil || !r.hostAbsences.applied) {
		return fmt.Errorf("stuntgres: unconsumed host pair absence boundary")
	}
	for _, descriptor := range r.stage.Catalog.State.Descriptors() {
		var runtime *host.Runtime
		switch d := descriptor.(type) {
		case *catalog.Language:
			runtime = d.Host
		case *catalog.Transform:
			runtime = d.Runtime
		case *catalog.Procedure:
			runtime = d.HostHandler
			if d.Host != nil {
				runtime = d.Host.Program.Runtime()
			}
			if d.HostConversion != nil {
				runtime = d.HostConversion.Token.Runtime()
			}
		}
		if procedure, ok := descriptor.(*catalog.Procedure); ok && procedure.Host != nil && runtime != nil && !r.hostRequirements[runtime.Provider()] {
			return fmt.Errorf("stuntgres: missing host runtime prerequisite")
		}
	}
	return nil
}
