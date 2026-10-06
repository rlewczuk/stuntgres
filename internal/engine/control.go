package engine

import (
	"context"
	"fmt"
	"io"
	"sort"
	"strings"

	"stuntgres/internal/parser"
)

func (e *Engine) Reset(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	fresh := New()
	_, err := e.ReplaceCatalog(ctx, fresh.CatalogRead())
	return err
}
func quoteIdentifier(s string) string { return `"` + strings.ReplaceAll(s, `"`, `""`) + `"` }

// Namespace ACL entry order and split entries do not affect reproducibility.
// Principal/grantor references do: losing them would change DROP dependencies.
func bootstrapNamespaceDumpable(d *namespaceDescriptor) bool {
	if !d.acl.Explicit {
		return false
	}
	publicRights := UsagePrivilege
	if d.address.ObjectID == PublicNamespaceOID {
		publicRights |= CreatePrivilege
	}
	var publicUse, publicOptions, ownerUse, ownerOptions Privileges
	for _, entry := range d.acl.Entries {
		if entry.Grantor != d.owner {
			return false
		}
		if entry.Grantee.Public {
			if entry.Grantee.Role != (PrincipalRef{}) {
				return false
			}
			publicUse |= entry.Privileges
			publicOptions |= entry.GrantOptions
		} else {
			if entry.Grantee.Role != d.owner {
				return false
			}
			ownerUse |= entry.Privileges
			ownerOptions |= entry.GrantOptions
		}
	}
	return publicUse == publicRights && publicOptions == 0 &&
		ownerUse == UsagePrivilege|CreatePrivilege && ownerOptions == UsagePrivilege|CreatePrivilege
}
func (e *Engine) Dump(ctx context.Context, dst io.Writer) error {
	if dst == nil {
		return fmt.Errorf("stuntgres: nil dump writer")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	e.mu.Lock()
	if err := ctx.Err(); err != nil {
		e.mu.Unlock()
		return err
	}
	type dumpRelation struct {
		owner, schema, name string
		columns             []ColumnDescriptor
		attributes          []int32
		versions            []rowVersion
		ends                *deletionRoot
		frontier            CommitSeq
		comment             string
		columnComments      map[int32]string
		notNullConstraints  []notNullConstraint
	}
	tables := make([]dumpRelation, 0)
	type dumpSchema struct{ name, owner string }
	var schemas []dumpSchema
	type dumpComment struct{ name, text string }
	var schemaComments []dumpComment
	// Reconstruct bootstrap identity changes before creating ordinary namespaces.
	// These statements run only in Restore's isolated catalog under bootstrap
	// authority; ACLs are reproduced by normal owner-transfer semantics.
	var preamble []string
	type builtinSchema struct {
		original, name, owner string
		moved                 bool
	}
	var builtins []builtinSchema
	for _, b := range []struct {
		oid  OID
		name string
	}{{PublicNamespaceOID, "public"}, {PGCatalogNamespaceOID, "pg_catalog"}} {
		d, exists := e.state.descriptors[ObjectAddress{ClassID: NamespaceClassID, ObjectID: b.oid}]
		if !exists {
			preamble = append(preamble, "DROP SCHEMA "+quoteIdentifier(b.name)+";\n")
			continue
		}
		ns := d.(*namespaceDescriptor)
		role, err := (RoleRead{e, e.roles}).Descriptor(ns.owner)
		if err != nil {
			e.mu.Unlock()
			return err
		}
		builtins = append(builtins, builtinSchema{original: b.name, name: ns.name, owner: role.Name})
	}
	remaining := 0
	for i := range builtins {
		builtins[i].moved = builtins[i].name == builtins[i].original
		if !builtins[i].moved {
			remaining++
		}
	}
	for remaining > 0 {
		progress := false
		for i := range builtins {
			b := &builtins[i]
			if b.moved {
				continue
			}
			occupied := false
			for j := range builtins {
				if !builtins[j].moved && builtins[j].original == b.name {
					occupied = true
				}
			}
			if occupied {
				continue
			}
			preamble = append(preamble, "ALTER SCHEMA "+quoteIdentifier(b.original)+" RENAME TO "+quoteIdentifier(b.name)+";\n")
			b.moved = true
			remaining--
			progress = true
		}
		if progress {
			continue
		}
		// A cycle needs a free ordinary spelling, never a reserved pg_ name.
		for i := range builtins {
			b := &builtins[i]
			if b.moved {
				continue
			}
			var temporary string
			for suffix := 0; ; suffix++ {
				candidate := fmt.Sprintf("stuntgres_restore_namespace_%d_%d", i, suffix)
				if _, exists := e.state.names[NameKey{ClassID: NamespaceClassID, Name: candidate}]; !exists {
					temporary = candidate
					break
				}
			}
			preamble = append(preamble, "ALTER SCHEMA "+quoteIdentifier(b.original)+" RENAME TO "+quoteIdentifier(temporary)+";\n")
			b.original = temporary
			break
		}
	}
	for _, b := range builtins {
		if b.owner != "postgres" {
			preamble = append(preamble, "ALTER SCHEMA "+quoteIdentifier(b.name)+" OWNER TO "+quoteIdentifier(b.owner)+";\n")
		}
	}
	for a, d := range e.state.descriptors {
		r, ok := d.(*relationDescriptor)
		if !ok {
			if _, builtinType := d.(*typeDescriptor); builtinType {
				continue
			}
			if d.Kind() == NamespaceObject {
				ns, valid := d.(*namespaceDescriptor)
				if valid && ns.comment != "" {
					schemaComments = append(schemaComments, dumpComment{ns.name, ns.comment})
				}
				if valid && (a.ObjectID == PublicNamespaceOID || a.ObjectID == PGCatalogNamespaceOID) &&
					!bootstrapNamespaceDumpable(ns) {
					e.mu.Unlock()
					return fmt.Errorf("stuntgres: dump cannot serialize changed builtin namespace ACL")
				}
				if valid && a.ObjectID != PublicNamespaceOID && a.ObjectID != PGCatalogNamespaceOID {
					if ns.acl.Explicit {
						e.mu.Unlock()
						return fmt.Errorf("stuntgres: dump cannot serialize explicit namespace ACL")
					}
					role, err := (RoleRead{e, e.roles}).Descriptor(ns.owner)
					if err != nil {
						e.mu.Unlock()
						return err
					}
					schemas = append(schemas, dumpSchema{ns.name, role.Name})
				}
				continue
			}
			e.mu.Unlock()
			return fmt.Errorf("stuntgres: unsupported catalog descriptor kind")
		}
		ns, ok := e.state.descriptors[r.namespace].(*namespaceDescriptor)
		if !ok {
			e.mu.Unlock()
			return fmt.Errorf("stuntgres: relation namespace is missing")
		}
		if r.acl.Explicit || len(r.columnACL) != 0 {
			e.mu.Unlock()
			return fmt.Errorf("stuntgres: dump cannot serialize explicit relation or column ACL")
		}
		principal, err := (RoleRead{e, e.roles}).Descriptor(r.owner)
		if err != nil {
			e.mu.Unlock()
			return err
		}
		storage := e.storage[a]
		if storage == nil {
			e.mu.Unlock()
			return fmt.Errorf("stuntgres: relation storage is missing")
		}
		tables = append(tables, dumpRelation{owner: principal.Name, schema: ns.name, name: r.name, columns: r.columns, attributes: r.attributes, versions: storage.versions, ends: storage.ends, frontier: e.commitSeq, comment: r.comment, columnComments: r.columnComments, notNullConstraints: r.notNullConstraints})
	}
	e.mu.Unlock()
	sort.Slice(tables, func(i, j int) bool {
		if tables[i].owner != tables[j].owner {
			return tables[i].owner < tables[j].owner
		}
		if tables[i].schema != tables[j].schema {
			return tables[i].schema < tables[j].schema
		}
		return tables[i].name < tables[j].name
	})
	emit := func(s string) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		n, err := io.WriteString(dst, s)
		if err != nil {
			return err
		}
		if n != len(s) {
			return io.ErrShortWrite
		}
		return ctx.Err()
	}
	for _, line := range preamble {
		if err := emit(line); err != nil {
			return err
		}
	}
	sort.Slice(schemas, func(i, j int) bool { return schemas[i].name < schemas[j].name })
	for _, s := range schemas {
		if err := emit("CREATE SCHEMA " + quoteIdentifier(s.name) + " AUTHORIZATION " + quoteIdentifier(s.owner) + ";\n"); err != nil {
			return err
		}
	}
	sort.Slice(schemaComments, func(i, j int) bool { return schemaComments[i].name < schemaComments[j].name })
	for _, c := range schemaComments {
		if err := emit("COMMENT ON SCHEMA " + quoteIdentifier(c.name) + " IS " + commentLiteral(c.text) + ";\n"); err != nil {
			return err
		}
	}
	currentOwner := "postgres"
	for _, t := range tables {
		if t.owner != currentOwner {
			if err := emit("SET SESSION AUTHORIZATION '" + strings.ReplaceAll(t.owner, "'", "''") + "';\n"); err != nil {
				return err
			}
			currentOwner = t.owner
		}
		qualified := quoteIdentifier(t.schema) + "." + quoteIdentifier(t.name)
		var line strings.Builder
		line.WriteString("CREATE TABLE " + qualified + " (")
		for i, c := range t.columns {
			if i > 0 {
				line.WriteString(", ")
			}
			declaration, err := c.Usage.Declaration()
			if err != nil {
				return err
			}
			line.WriteString(quoteIdentifier(c.Name) + " " + declaration)
			if !c.Nullable {
				for _, constraint := range t.notNullConstraints {
					if constraint.Attribute == t.attributes[i] {
						line.WriteString(" CONSTRAINT " + quoteIdentifier(constraint.Name) + " NOT NULL")
						break
					}
				}
			}
		}
		line.WriteString(");\n")
		if err := emit(line.String()); err != nil {
			return err
		}
		if t.comment != "" {
			if err := emit("COMMENT ON TABLE " + qualified + " IS " + commentLiteral(t.comment) + ";\n"); err != nil {
				return err
			}
		}
		for i, c := range t.columns {
			if text := t.columnComments[t.attributes[i]]; text != "" {
				if err := emit("COMMENT ON COLUMN " + qualified + "." + quoteIdentifier(c.Name) + " IS " + commentLiteral(text) + ";\n"); err != nil {
					return err
				}
			}
		}
		for _, version := range t.versions {
			if err := ctx.Err(); err != nil {
				return err
			}
			if !liveVersion(version, t.frontier, t.ends, nil) {
				continue
			}
			row := version.values
			line.Reset()
			if len(t.columns) == 0 {
				if err := emit("INSERT INTO " + qualified + " DEFAULT VALUES;\n"); err != nil {
					return err
				}
				continue
			}
			line.WriteString("INSERT INTO " + qualified + " VALUES (")
			for i, v := range row {
				if i > 0 {
					line.WriteString(", ")
				}
				literal, err := v.Type().SQLLiteral(ctx, v)
				if err != nil {
					return err
				}
				line.WriteString(literal)
			}
			line.WriteString(");\n")
			if err := emit(line.String()); err != nil {
				return err
			}
		}
	}
	return ctx.Err()
}

type contextReader struct {
	ctx context.Context
	src io.Reader
}

func (r contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	n, err := r.src.Read(p)
	if canceled := r.ctx.Err(); canceled != nil {
		return n, canceled
	}
	return n, err
}
func (e *Engine) Restore(ctx context.Context, src io.Reader) error {
	if src == nil {
		return fmt.Errorf("stuntgres: nil restore reader")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	stage := e.restoreStage()
	data, err := io.ReadAll(contextReader{ctx, src})
	if err != nil {
		return err
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	statements, err := parser.ParseWithOptions(string(data), parser.Options{Context: ctx, StandardConformingStrings: true, RestoreDefaultValues: true})
	if err != nil {
		return err
	}
	// The isolated role snapshot retains exact target identities for final remapping.
	session, err := stage.StartSession(ctx, SessionStart{ID: 1})
	if err != nil {
		return err
	}
	defer func() { session.RequestEnd(CleanClose); _ = session.WaitEnd(context.Background()) }()
	op, err := session.Acquire(ctx)
	if err != nil {
		return err
	}
	defer op.Done()
	for _, stmt := range statements {
		switch s := stmt.(type) {
		case *parser.CreateTable, *parser.CreateSchema, *parser.AlterSchema, *parser.DropSchema:
		case *parser.Comment:
			if s.Kind == parser.CommentRole {
				return sqlError("0A000", "restore accepts only database-local COMMENT targets")
			}
		case *parser.SessionAuthorization:
			if !s.OwnerDirective {
				return sqlError("0A000", "restore accepts only named SET SESSION AUTHORIZATION owner directives")
			}
		case *parser.Insert:
			if s.Source != nil {
				return sqlError("0A000", "restore INSERT query sources are unsupported")
			}
			for _, row := range s.Rows {
				for _, v := range row {
					if err := restoreScalar(v); err != nil {
						return err
					}
				}
			}
		default:
			return sqlError("0A000", "restore accepts only CREATE/ALTER/DROP SCHEMA, CREATE TABLE, INSERT, database-local COMMENT and SET SESSION AUTHORIZATION")
		}
		op.restoreTableDestination = false
		if _, ok := stmt.(*parser.CreateTable); ok {
			op.restoreTableDestination = true
		}
		op.restoreInsertAuthority = false
		if _, ok := stmt.(*parser.Insert); ok {
			op.restoreInsertAuthority = true
		}
		op.restoreCommentAuthority = false
		if _, ok := stmt.(*parser.Comment); ok {
			op.restoreCommentAuthority = true
		}
		var result Result
		result, err = stage.Execute(op, stmt, 0, nil)
		if result.Rows != nil {
			closeErr := result.Rows.Close()
			if err == nil {
				err = closeErr
			}
		}
		op.restoreCommentAuthority = false
		op.DetachNotices()
		if err != nil {
			return err
		}
	}
	_, err = e.ReplaceCatalog(ctx, stage.CatalogRead())
	return err
}

// Explicit escape strings are independent of the destination string setting.
func commentLiteral(text string) string {
	return "E'" + strings.ReplaceAll(strings.ReplaceAll(text, "\\", "\\\\"), "'", "''") + "'"
}

func restoreScalar(raw parser.Expr) error {
	switch r := raw.(type) {
	case parser.CastExpr:
		return restoreScalar(r.Child)
	case parser.ParameterExpr:
		return expressionError("42P02", "parameters are not allowed in restore", r.Pos)
	case parser.LiteralExpr, parser.TypedLiteralExpr:
		return nil
	case parser.OperatorExpr:
		if len(r.Name.Parts) != 1 || (!arithmeticName(r.Name.Parts[0].Text) && !symbolicArithmeticName(r.Name.Parts[0].Text)) {
			return fmt.Errorf("stuntgres: invalid restore scalar")
		}
		for _, child := range r.Args {
			if err := restoreScalar(child); err != nil {
				return err
			}
		}
		return nil
	default:
		return fmt.Errorf("stuntgres: invalid restore scalar")
	}
}
