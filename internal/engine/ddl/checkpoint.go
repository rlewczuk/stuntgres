package ddl

import (
	"context"
	"fmt"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/parser"
)

const CheckpointUnsupported = "CHECKPOINT physical WAL and fsync operations are unsupported in embedded mode"

// Checkpoint checks current inherited authority without requesting physical work
// or staging any catalog, data, or statistics changes.
func Checkpoint(ctx context.Context, view access.View, _ *parser.Checkpoint) error {
	target, ok := view.Roles.Lookup("pg_checkpoint")
	if !ok {
		return fmt.Errorf("stuntgres: missing pg_checkpoint role")
	}
	allowed, err := view.Roles.InheritsRole(view.State.CurrentUser, target.Ref)
	if err != nil {
		return err
	}
	if !allowed {
		return detailError("42501", "permission denied to execute CHECKPOINT command", `Only roles with privileges of the "pg_checkpoint" role may execute this command.`)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return sqlError("0A000", CheckpointUnsupported)
}
