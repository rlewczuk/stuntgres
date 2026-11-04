package ddl

import (
	"context"
	"strings"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/parser"
)

const LoadUnsupported = "LOAD native libraries are unsupported in embedded mode"

// LoadModule identifies the concrete module selected by successful admission.
// Zero is not a supported module.
type LoadModule uint8

const (
	LoadOverexplain LoadModule = iota + 1
	LoadSepgsql
	LoadAutoExplain
)

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

// Load preserves the sepgsql utility hook and ordinary library authorization
// before selecting a supported module. It never performs filesystem loading.
func Load(ctx context.Context, view access.View, s *parser.Load) (LoadModule, error) {
	if view.Sepgsql.Policy != nil && !view.Sepgsql.Policy.Permissive(view.Sepgsql.Subject) {
		return 0, sqlError("42501", "SELinux: LOAD is not permitted")
	}
	if err := checkLoad(ctx, view, s); err != nil {
		return 0, err
	}
	switch s.Filename {
	case "pg_overexplain", "pg_overexplain.so", "$libdir/pg_overexplain", "$libdir/pg_overexplain.so":
		return LoadOverexplain, nil
	case "auto_explain", "auto_explain.so", "$libdir/auto_explain", "$libdir/auto_explain.so":
		return LoadAutoExplain, nil
	case "$libdir/sepgsql":
		if view.Sepgsql.Policy != nil {
			return LoadSepgsql, nil
		}
	}
	return 0, sqlError("0A000", LoadUnsupported)
}
