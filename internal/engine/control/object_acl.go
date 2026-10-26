package control

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/dump"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/engine/regobj"
	"stuntgres/internal/engine/typedef"
	"stuntgres/internal/sqltype"
)

// All strings are rendered from the immutable capture before writer I/O.
func prepareObjectACLs(ctx context.Context, s *dumpScript) error {
	c := s.Capture
	bootstrap, _ := c.OriginalRoles.Lookup("postgres")
	var statements []string
	s.typeACLStatements = make(map[ids.OID][]string)
	for _, d := range c.OriginalCatalog.Descriptors() {
		if err := ctx.Err(); err != nil {
			return err
		}
		if temporaryDumpObject(c.OriginalCatalog.State(), d) {
			continue
		}
		owner, acl, _ := access.DescriptorACL(d)
		var table, column, locator string
		switch d := d.(type) {
		case *catalog.Namespace:
			if namespaceACLReconstructed(c, d, bootstrap.Ref) {
				continue
			}
			table, column = "pg_namespace", "nspacl"
			locator = "nspname=" + dump.CommentLiteral(d.Name()) + "::pg_catalog.name"
		case *catalog.Tablespace:
			if !acl.Explicit && owner == bootstrap.Ref {
				continue
			}
			table, column = "pg_tablespace", "spcacl"
			locator = "spcname=" + dump.CommentLiteral(d.Name()) + "::pg_catalog.name"
		case *catalog.Type:
			// Constructor defaults retain raw absence without a repair.
			if typedef.DefaultDomainACL(d) || !acl.Explicit {
				continue
			}
			table, column = "pg_type", "typacl"
			locator = "oid=" + dump.CommentLiteral(regobj.TypeSpelling(c.OriginalCatalog, nil, d.Address().ObjectID)) + "::pg_catalog.regtype"
		case *catalog.Relation:
			if d.RelationKind == catalog.CatalogRelation {
				continue
			}
			if err := access.ValidateACL(c.OriginalRoles, d); err != nil {
				return err
			}
			ns, ok := c.OriginalCatalog.Namespace(d.Namespace())
			if !ok {
				return fmt.Errorf("stuntgres: missing ACL relation namespace")
			}
			identity := dump.CommentLiteral(catalog.QuoteIdentifier(ns.Name())+"."+catalog.QuoteIdentifier(d.Name())) + "::pg_catalog.regclass"
			if acl.Explicit {
				literal, err := objectACLLiteral(ctx, c, acl)
				if err != nil {
					return err
				}
				statements = append(statements, "UPDATE pg_catalog.pg_class SET relacl="+literal+"::pg_catalog.aclitem[] WHERE oid="+identity+";\n")
			}
			for i, sub := range d.Attributes {
				columnACL := d.Security.ColumnACL[sub]
				if !columnACL.Explicit {
					continue
				}
				literal, err := objectACLLiteral(ctx, c, columnACL)
				if err != nil {
					return err
				}
				statements = append(statements, "UPDATE pg_catalog.pg_attribute SET attacl="+literal+"::pg_catalog.aclitem[] WHERE attrelid="+identity+" AND attname="+dump.CommentLiteral(d.Columns[i].Name)+"::pg_catalog.name;\n")
			}
			continue
		case *catalog.Procedure:
			if catalog.ExtensionMembership(d) != (ids.ObjectAddress{}) {
				continue
			}
			if !acl.Explicit && d.SQL == nil && d.Aggregate == nil {
				continue
			}
			ns, ok := c.OriginalCatalog.Namespace(d.Namespace())
			if !ok {
				return fmt.Errorf("stuntgres: missing ACL routine namespace")
			}
			var args strings.Builder
			if d.Signature != "" {
				for raw := range strings.SplitSeq(string(d.Signature), ",") {
					id, err := strconv.ParseUint(raw, 10, 32)
					if err != nil {
						return err
					}
					if args.Len() > 0 {
						args.WriteString(", ")
					}
					args.WriteString(regobj.TypeSpelling(c.OriginalCatalog, nil, ids.OID(id)))
				}
			}
			table, column = "pg_proc", "proacl"
			identity := catalog.QuoteIdentifier(ns.Name()) + "." + catalog.QuoteIdentifier(d.Name()) + "(" + args.String() + ")"
			locator = "oid=" + dump.CommentLiteral(identity) + "::pg_catalog.regprocedure"
		case *catalog.Language:
			if !acl.Explicit && owner == bootstrap.Ref {
				continue
			}
			table, column = "pg_language", "lanacl"
			locator = "lanname=" + dump.CommentLiteral(d.Name()) + "::pg_catalog.name"
		case *catalog.ForeignObject:
			switch d.Address().ClassID {
			case ids.ForeignWrapperClassID:
				table, column = "pg_foreign_data_wrapper", "fdwacl"
				locator = "fdwname=" + dump.CommentLiteral(d.Name()) + "::pg_catalog.name"
			case ids.ForeignServerClassID:
				table, column = "pg_foreign_server", "srvacl"
				locator = "srvname=" + dump.CommentLiteral(d.Name()) + "::pg_catalog.name"
			default:
				continue
			}
		default:
			continue
		}
		if err := access.ValidateACL(c.OriginalRoles, d); err != nil {
			return err
		}
		literal, err := objectACLLiteral(ctx, c, acl)
		if err != nil {
			return err
		}
		ownerColumn := ""
		switch table {
		case "pg_language":
			ownerColumn = "lanowner"
		case "pg_namespace":
			// User namespace ownership is already in CREATE SCHEMA.
			if d.Address().ObjectID == ids.PublicNamespaceOID || d.Address().ObjectID == ids.PGCatalogNamespaceOID || d.Address().ObjectID == ids.PGToastNamespaceOID || d.Address().ObjectID == 13229 {
				ownerColumn = "nspowner"
			}
		case "pg_tablespace":
			ownerColumn = "spcowner"
		}
		if ownerColumn != "" {
			role, err := c.OriginalRoles.Descriptor(owner)
			if err != nil {
				return err
			}
			statement := "UPDATE pg_catalog." + table + " SET " + ownerColumn + "=" + dump.CommentLiteral(catalog.QuoteIdentifier(role.Name)) + "::pg_catalog.regrole WHERE " + locator + ";\n"
			statements = append(statements, statement)
			if table == "pg_namespace" || table == "pg_tablespace" {
				s.initialACLStatements = append(s.initialACLStatements, statement)
			}
		}
		statement := "UPDATE pg_catalog." + table + " SET " + column + "=" + literal + "::pg_catalog.aclitem[] WHERE " + locator + ";\n"
		statements = append(statements, statement)
		switch table {
		case "pg_namespace", "pg_tablespace":
			s.initialACLStatements = append(s.initialACLStatements, statement)
		case "pg_type":
			anchor, err := (Resolver{}).Predecessor(c.OriginalCatalog, d.Address(), ids.ObjectAddress{})
			if err != nil {
				return err
			}
			if _, extension := c.OriginalCatalog.Extension(ids.ObjectAddress{ClassID: ids.ExtensionClassID, ObjectID: anchor}); extension && s.extensionTypeAfter != 0 {
				anchor = s.extensionTypeAfter
			}
			if anchor == 0 {
				s.initialACLStatements = append(s.initialACLStatements, statement)
			} else {
				s.typeACLStatements[anchor] = append(s.typeACLStatements[anchor], statement)
			}
		}
	}
	// Owner replacement must precede its exact ACL replacement.
	compare := func(a, b string) int {
		ownerA, ownerB := objectACLOwnerStatement(a), objectACLOwnerStatement(b)
		if ownerA != ownerB {
			if ownerA {
				return -1
			}
			return 1
		}
		return strings.Compare(a, b)
	}
	slices.SortFunc(statements, compare)
	slices.SortFunc(s.initialACLStatements, compare)
	for _, statements := range s.typeACLStatements {
		slices.SortFunc(statements, compare)
	}
	s.objectACLStatements = statements
	return nil
}

// Omit only exactly reconstructed raw authority. In particular, split entries
// and explicit user-schema defaults are not reduced to effective equality.
func namespaceACLReconstructed(c *Capture, d *catalog.Namespace, bootstrap authorization.PrincipalRef) bool {
	owner := bootstrap
	switch d.Address().ObjectID {
	case ids.PublicNamespaceOID:
		role, ok := c.OriginalRoles.Lookup("pg_database_owner")
		if !ok {
			return false
		}
		owner = role.Ref
	case ids.PGCatalogNamespaceOID, 13229:
	case ids.PGToastNamespaceOID:
		return d.Owner == owner && !d.ACL.Explicit && len(d.ACL.Entries) == 0
	default:
		return !d.ACL.Explicit && len(d.ACL.Entries) == 0
	}
	if d.Owner != owner || !d.ACL.Explicit || len(d.ACL.Entries) != 2 {
		return false
	}
	ownerEntry := authorization.ACLEntry{Grantor: owner, Grantee: authorization.ACLGrantee{Role: owner}, Privileges: authorization.UsagePrivilege | authorization.CreatePrivilege}
	publicEntry := authorization.ACLEntry{Grantor: owner, Grantee: authorization.ACLGrantee{Public: true}, Privileges: authorization.UsagePrivilege}
	return d.ACL.Entries[0] == ownerEntry && d.ACL.Entries[1] == publicEntry ||
		d.ACL.Entries[1] == ownerEntry && d.ACL.Entries[0] == publicEntry
}

func objectACLOwnerStatement(s string) bool {
	return strings.HasPrefix(s, "UPDATE pg_catalog.pg_language SET lanowner=") ||
		strings.HasPrefix(s, "UPDATE pg_catalog.pg_namespace SET nspowner=") ||
		strings.HasPrefix(s, "UPDATE pg_catalog.pg_tablespace SET spcowner=")
}

// Apply only captured authority, once its identities exist and before consumers
// need it. The final phase still reconciles authority after all owner changes.
func emitDumpInitialACLs(ctx context.Context, s *dumpScript, w *dumpWriter) error {
	return emitObjectACLStatements(ctx, w, s.initialACLStatements)
}

func emitDumpObjectACLs(ctx context.Context, s *dumpScript, w *dumpWriter) error {
	return emitObjectACLStatements(ctx, w, s.objectACLStatements)
}

func emitObjectACLStatements(ctx context.Context, w *dumpWriter, statements []string) error {
	if len(statements) == 0 {
		return nil
	}
	if err := w.forceOwner("postgres"); err != nil {
		return err
	}
	for _, statement := range statements {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := w.emit(statement); err != nil {
			return err
		}
	}
	return nil
}

func objectACLLiteral(ctx context.Context, c *Capture, acl authorization.ACLValue) (string, error) {
	if !acl.Explicit {
		return "NULL", nil
	}
	if len(acl.Entries) == 0 {
		return emptyObjectACLExpression, nil
	}
	array, ok := c.OriginalCatalog.LookupType(sqltype.ACLItemArray)
	if !ok {
		return "", fmt.Errorf("stuntgres: missing ACL array type")
	}
	value, err := pgcatalog.ClassACL(c.OriginalCatalog.Owner(), c.OriginalRoles, array, acl)
	if err != nil {
		return "", err
	}
	text, err := array.OutputWithEnvironment(ctx, value, sqltype.OutputEnvironment{ACLItem: c.ACLOutput})
	if err != nil {
		return "", err
	}
	return dump.CommentLiteral(text), nil
}
