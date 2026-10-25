package control

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/dump"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/engine/regobj"
	"stuntgres/internal/sqltype"
)

// All strings are rendered from the immutable capture before writer I/O.
func prepareObjectACLs(ctx context.Context, c *Capture) ([]string, error) {
	bootstrap, _ := c.OriginalRoles.Lookup("postgres")
	var statements []string
	for _, d := range c.OriginalCatalog.Descriptors() {
		if err := ctx.Err(); err != nil { return nil, err }
		if temporaryDumpObject(c.OriginalCatalog.State(),d) { continue }
		owner, acl, _ := access.DescriptorACL(d)
		var table, column, locator string
		switch d := d.(type) {
		case *catalog.Procedure:
			if catalog.ExtensionMembership(d) != (ids.ObjectAddress{}) { continue }
			if !acl.Explicit && d.SQL == nil && d.Aggregate == nil { continue }
			ns, ok := c.OriginalCatalog.Namespace(d.Namespace())
			if !ok { return nil, fmt.Errorf("stuntgres: missing ACL routine namespace") }
			var args strings.Builder
			if d.Signature != "" {
				for raw := range strings.SplitSeq(string(d.Signature), ",") {
					id, err := strconv.ParseUint(raw, 10, 32)
					if err != nil { return nil, err }
					if args.Len() > 0 { args.WriteString(", ") }
					args.WriteString(regobj.TypeSpelling(c.OriginalCatalog, nil, ids.OID(id)))
				}
			}
			table, column = "pg_proc", "proacl"
			identity := catalog.QuoteIdentifier(ns.Name())+"."+catalog.QuoteIdentifier(d.Name())+"("+args.String()+")"
			locator = "oid="+dump.CommentLiteral(identity)+"::pg_catalog.regprocedure"
		case *catalog.Language:
			if !acl.Explicit && owner == bootstrap.Ref { continue }
			table, column = "pg_language", "lanacl"
			locator = "lanname="+dump.CommentLiteral(d.Name())+"::pg_catalog.name"
		case *catalog.ForeignObject:
			switch d.Address().ClassID {
			case ids.ForeignWrapperClassID:
				table, column = "pg_foreign_data_wrapper", "fdwacl"
				locator = "fdwname="+dump.CommentLiteral(d.Name())+"::pg_catalog.name"
			case ids.ForeignServerClassID:
				table, column = "pg_foreign_server", "srvacl"
				locator = "srvname="+dump.CommentLiteral(d.Name())+"::pg_catalog.name"
			default: continue
			}
		default: continue
		}
		if err := access.ValidateACL(c.OriginalRoles, d); err != nil { return nil, err }
		literal := "NULL"
		if acl.Explicit && len(acl.Entries)==0 {
			// Text '{}' loses PostgreSQL's one-dimensional empty ACL shape.
			literal = emptyObjectACLExpression
		} else if acl.Explicit {
			array, ok := c.OriginalCatalog.LookupType(sqltype.ACLItemArray)
			if !ok { return nil, fmt.Errorf("stuntgres: missing ACL array type") }
			value, err := pgcatalog.ClassACL(c.OriginalCatalog.Owner(), c.OriginalRoles, array, acl)
			if err != nil { return nil, err }
			text, err := array.OutputWithEnvironment(ctx, value, sqltype.OutputEnvironment{ACLItem: c.ACLOutput})
			if err != nil { return nil, err }
			literal = dump.CommentLiteral(text)
		}
		if table == "pg_language" && owner != bootstrap.Ref {
			role, err := c.OriginalRoles.Descriptor(owner)
			if err != nil { return nil, err }
			statements = append(statements, "UPDATE pg_catalog.pg_language SET lanowner="+dump.CommentLiteral(catalog.QuoteIdentifier(role.Name))+"::pg_catalog.regrole WHERE "+locator+";\n")
		}
		statements = append(statements, "UPDATE pg_catalog."+table+" SET "+column+"="+literal+"::pg_catalog.aclitem[] WHERE "+locator+";\n")
	}
	// Owner replacement must precede its exact ACL replacement.
	slices.SortFunc(statements, func(a,b string) int {
		if strings.Contains(a," SET lanowner=") != strings.Contains(b," SET lanowner=") {
			if strings.Contains(a," SET lanowner=") { return -1 }; return 1
		}
		return strings.Compare(a,b)
	})
	return statements, nil
}

func emitDumpObjectACLs(ctx context.Context, s *dumpScript, w *dumpWriter) error {
	if len(s.objectACLStatements) == 0 { return nil }
	if err := w.forceOwner("postgres"); err != nil { return err }
	for _, statement := range s.objectACLStatements {
		if err := ctx.Err(); err != nil { return err }
		if err := w.emit(statement); err != nil { return err }
	}
	return nil
}
