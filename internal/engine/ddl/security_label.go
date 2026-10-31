package ddl

import (
 "context"
 "stuntgres/internal/engine/access"
 "stuntgres/internal/parser"
)

// SecurityLabel selects a provider before resolving the target or its owner.
// PostgreSQL's C provider ABI and SELinux integration are not available here.
func SecurityLabel(ctx context.Context, view access.View, s *parser.SecurityLabel) error {
 if err := ctx.Err(); err != nil { return err }
 if _, err := view.Roles.Descriptor(view.State.CurrentUser); err != nil { return err }
 if s.Provider == nil { return sqlError("22023", "no security label providers have been loaded") }
 return sqlError("22023", "security label provider \""+s.Provider.Text+"\" is not loaded")
}
