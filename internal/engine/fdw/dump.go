package fdw

import (
	"context"
	"fmt"
	"strings"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/dump"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/regobj"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/engine/typedef"
)

// DumpOptions preserves provider option order and SQL literal bytes, including
// sensitive mapping values. Dump's host caller owns the resulting credentials.
func DumpOptions(options []catalog.ForeignOption) string {
	if len(options) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString(" OPTIONS (")
	for i, o := range options {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(catalog.QuoteIdentifier(o.Name))
		b.WriteByte(' ')
		b.WriteString(dump.CommentLiteral(o.Value))
	}
	b.WriteByte(')')
	return b.String()
}

// DumpProcedure validates a support identity. SQL validators are replayed by
// the routine graph; native callbacks remain destination registration prerequisites.
func DumpProcedure(view catalog.Read, address ids.ObjectAddress, signature catalog.ProcedureSignature) (string, error) {
	proc, ok := view.Procedure(address)
	if !ok || proc.Signature != signature || proc.Foreign == nil && (signature != catalog.ForeignValidatorSignature || proc.SQL == nil) {
		return "", fmt.Errorf("stuntgres: incompatible foreign dump procedure")
	}
	if err := (stored.ProcedureProvider{}).Validate(view, proc); err != nil {
		return "", err
	}
	ns, ok := view.Namespace(proc.Namespace())
	if !ok {
		return "", fmt.Errorf("stuntgres: missing foreign procedure namespace")
	}
	return catalog.QuoteIdentifier(ns.Name()) + "." + catalog.QuoteIdentifier(proc.Name()), nil
}

// dumpDeclarationProcedure references ordinary routine declarations separately
// from native prerequisites. The control graph emits SQL/PL definitions and
// orders them before their consumers; only native procedures need registration.
func dumpDeclarationProcedure(view catalog.Read, address ids.ObjectAddress, signature catalog.ProcedureSignature) (string, error) {
	proc, ok := view.Procedure(address)
	if !ok || proc.Foreign != nil {
		return DumpProcedure(view, address, signature)
	}
	if !proc.DynamicCallable() || proc.RoutineKind != catalog.FunctionRoutine || proc.Signature != signature {
		return "", fmt.Errorf("stuntgres: incompatible foreign dump procedure")
	}
	if err := (stored.ProcedureProvider{}).Validate(view, proc); err != nil {
		return "", err
	}
	ns, ok := view.Namespace(proc.Namespace())
	if !ok {
		return "", fmt.Errorf("stuntgres: missing foreign procedure namespace")
	}
	return catalog.QuoteIdentifier(ns.Name()) + "." + catalog.QuoteIdentifier(proc.Name()), nil
}

// DumpObject renders CREATE followed by ordinary owner transfer under bootstrap
// authority. Mappings name their captured principal and have no OWNER command.
func DumpObject(view catalog.Read, roles authorization.Read, d *catalog.ForeignObject) (dump.Statement, error) {
	if err := (catalog.ForeignCatalogProvider{Class: d.Address().ClassID}).Validate(view, d); err != nil {
		return dump.Statement{}, err
	}
	name := catalog.QuoteIdentifier(d.Name())
	var sql, ownerKind string
	switch d.Address().ClassID {
	case ids.ForeignWrapperClassID:
		sql = "CREATE FOREIGN DATA WRAPPER " + name
		ownerKind = "FOREIGN DATA WRAPPER"
		for _, capability := range []struct {
			address   ids.ObjectAddress
			signature catalog.ProcedureSignature
			label     string
		}{{d.Handler, catalog.ForeignHandlerSignature, "HANDLER"}, {d.Validator, catalog.ForeignValidatorSignature, "VALIDATOR"}} {
			if capability.address == (ids.ObjectAddress{}) {
				sql += " NO " + capability.label
				continue
			}
			function, err := dumpDeclarationProcedure(view, capability.address, capability.signature)
			if err != nil {
				return dump.Statement{}, err
			}
			sql += " " + capability.label + " " + function
		}
	case ids.ForeignServerClassID:
		wrapper, ok := view.Foreign(d.Wrapper)
		if !ok {
			return dump.Statement{}, fmt.Errorf("stuntgres: missing foreign-data wrapper")
		}
		sql = "CREATE SERVER " + name
		ownerKind = "SERVER"
		if d.ServerType != nil {
			sql += " TYPE " + dump.CommentLiteral(*d.ServerType)
		}
		if d.Version != nil {
			sql += " VERSION " + dump.CommentLiteral(*d.Version)
		}
		sql += " FOREIGN DATA WRAPPER " + catalog.QuoteIdentifier(wrapper.Name())
	case ids.ForeignMappingClassID:
		server, ok := view.Foreign(d.Server)
		if !ok {
			return dump.Statement{}, fmt.Errorf("stuntgres: missing foreign server")
		}
		principal := "PUBLIC"
		if d.MappedRole != (authorization.PrincipalRef{}) {
			role, err := roles.Descriptor(d.MappedRole)
			if err != nil {
				return dump.Statement{}, err
			}
			principal = catalog.QuoteIdentifier(role.Name)
			if d.Owner != d.MappedRole {
				return dump.Statement{}, fmt.Errorf("stuntgres: invalid mapping owner")
			}
		} else if d.Owner != (authorization.PrincipalRef{}) {
			return dump.Statement{}, fmt.Errorf("stuntgres: invalid PUBLIC mapping owner")
		}
		sql = "CREATE USER MAPPING FOR " + principal + " SERVER " + catalog.QuoteIdentifier(server.Name())
	default:
		return dump.Statement{}, fmt.Errorf("stuntgres: unsupported foreign dump class")
	}
	sql += DumpOptions(d.Options) + ";\n"
	if ownerKind != "" {
		role, err := roles.Descriptor(d.Owner)
		if err != nil {
			return dump.Statement{}, err
		}
		if role.Name != "postgres" {
			sql += "ALTER " + ownerKind + " " + name + " OWNER TO " + catalog.QuoteIdentifier(role.Name) + ";\n"
		}
	}
	return dump.Statement{Owner: "postgres", SQL: sql}, nil
}

// DumpTable renders logical foreign descriptors, never storage or remote rows.
// Column options are addressed by stable attnum rather than live slice slot.
func DumpTable(ctx context.Context, view catalog.Read, roles authorization.Read, r *catalog.Relation, deferred map[int32]bool) (dump.Statement, error) {
	if r.Foreign == nil {
		return dump.Statement{}, fmt.Errorf("stuntgres: missing foreign table definition")
	}
	owner, err := roles.Descriptor(r.Owner)
	if err != nil {
		return dump.Statement{}, err
	}
	ns, ok := view.Namespace(r.Namespace())
	if !ok {
		return dump.Statement{}, fmt.Errorf("stuntgres: missing foreign table namespace")
	}
	server, ok := view.Foreign(r.Foreign.Server)
	if !ok || server.Address().ClassID != ids.ForeignServerClassID {
		return dump.Statement{}, fmt.Errorf("stuntgres: missing foreign server")
	}
	if len(r.Columns) != len(r.Attributes) {
		return dump.Statement{}, fmt.Errorf("stuntgres: invalid foreign attributes")
	}
	qualified := catalog.QuoteIdentifier(ns.Name()) + "." + catalog.QuoteIdentifier(r.Name())
	var b strings.Builder
	b.WriteString("CREATE FOREIGN TABLE " + qualified + " (")
	for i, column := range r.Columns {
		if i > 0 {
			b.WriteString(", ")
		}
		attribute := r.Attributes[i]
		spelling, err := regobj.ColumnDeclaration(view, column)
		if err != nil {
			return dump.Statement{}, err
		}
		b.WriteString(catalog.QuoteIdentifier(column.Name) + " " + spelling)
		if column.Usage.Collation.ID != column.Usage.Type.Usage().Collation.ID {
			collation := typedef.CollationSQLName(view, column.Usage.Collation.ID)
			if collation == "" {
				return dump.Statement{}, fmt.Errorf("stuntgres: unsupported stored collation")
			}
			b.WriteString(" COLLATE " + collation)
		}
		b.WriteString(DumpOptions(r.Foreign.Columns[attribute]))
		if definition, ok := r.ColumnMeta.Defaults[attribute]; ok && !deferred[attribute] {
			text, err := stored.DefaultSQL(ctx, view, definition.Expression)
			if err != nil {
				return dump.Statement{}, err
			}
			b.WriteString(" DEFAULT " + text)
		}
		if definition, ok := r.ColumnMeta.Generations[attribute]; ok {
			text, err := (stored.ScalarRenderer{Owner: r}).SQL(ctx, view, definition.Expression)
			if err != nil {
				return dump.Statement{}, err
			}
			b.WriteString(" GENERATED ALWAYS AS (" + text + ") " + definition.Kind.SQL())
		}
		if !column.Nullable {
			for _, constraint := range r.Constraints.NotNull {
				if constraint.Attribute == attribute && !constraint.NotValid {
					b.WriteString(" CONSTRAINT " + catalog.QuoteIdentifier(constraint.Name) + " NOT NULL")
					break
				}
			}
		}
	}
	for i, check := range table.OrderedChecks(r) {
		if check.NotValid {
			return dump.Statement{}, fmt.Errorf("stuntgres: dump cannot serialize unvalidated foreign CHECK")
		}
		text, err := (stored.ScalarRenderer{Owner: r, CheckReplayPrecision: true}).SQL(ctx, view, check.Expression)
		if err != nil {
			return dump.Statement{}, err
		}
		if len(r.Columns) != 0 || i != 0 {
			b.WriteString(", ")
		}
		b.WriteString("CONSTRAINT " + catalog.QuoteIdentifier(check.Name) + " CHECK (" + text + ")")
		if check.NoInherit {
			b.WriteString(" NO INHERIT")
		}
	}
	b.WriteString(") SERVER " + catalog.QuoteIdentifier(server.Name()) + DumpOptions(r.Foreign.Options) + ";\n")
	definition := b.String()
	b.Reset()
	if r.Comment != "" {
		b.WriteString("COMMENT ON FOREIGN TABLE " + qualified + " IS " + dump.CommentLiteral(r.Comment) + ";\n")
	}
	for i, column := range r.Columns {
		if text := r.ColumnMeta.Comments[r.Attributes[i]]; text != "" {
			b.WriteString("COMMENT ON COLUMN " + qualified + "." + catalog.QuoteIdentifier(column.Name) + " IS " + dump.CommentLiteral(text) + ";\n")
		}
	}
	if owner.Name != "postgres" {
		b.WriteString("ALTER FOREIGN TABLE " + qualified + " OWNER TO " + catalog.QuoteIdentifier(owner.Name) + ";\n")
	}
	return dump.Statement{Owner: "postgres", SQL: definition, FollowingSQL: b.String()}, nil
}
