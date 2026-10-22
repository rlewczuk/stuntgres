package ddl

import (
	"context"
	"strings"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/parser"
)

const LoadUnsupported = "LOAD native libraries are unsupported in embedded mode"

// Load checks execution authority and lexical filename restrictions only. It
// never inspects the filesystem, loads process code, or stages catalog changes.
func Load(ctx context.Context, view access.View, s *parser.Load) error {
	actor, err := view.Roles.Descriptor(view.State.CurrentUser)
	if err != nil {
		return err
	}
	filename := s.Filename
	if !actor.Superuser {
		const prefix = "$libdir/plugins/"
		suffix, permitted := strings.CutPrefix(filename, prefix)
		if !permitted || strings.Contains(suffix, "/") {
			return sqlError("42501", "access to library \""+filename+"\" is not allowed")
		}
	}
	if strings.HasPrefix(filename, "$") {
		if macro, _, slash := strings.Cut(filename, "/"); slash && macro != "$libdir" {
			return sqlError("42602", "invalid macro name in path: "+filename)
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return sqlError("0A000", LoadUnsupported)
}
