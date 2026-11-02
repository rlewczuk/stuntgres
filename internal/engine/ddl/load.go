package ddl

import (
	"context"
	"strings"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/parser"
)

const LoadUnsupported = "LOAD native libraries are unsupported in embedded mode"

// checkLoad checks current execution authority and lexical restrictions before
// either bounded built-in activation or the arbitrary-library refusal.
func checkLoad(ctx context.Context, view access.View, s *parser.Load) error {
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
	return nil
}

// Load admits only the built-in module after ordinary authorization. All other
// native libraries retain the arbitrary-loader boundary without filesystem access.
func Load(ctx context.Context, view access.View, s *parser.Load) error {
	if err := checkLoad(ctx, view, s); err != nil {
		return err
	}
	switch s.Filename {
	case "pg_overexplain", "pg_overexplain.so", "$libdir/pg_overexplain", "$libdir/pg_overexplain.so":
		return nil
	}
	return sqlError("0A000", LoadUnsupported)
}
