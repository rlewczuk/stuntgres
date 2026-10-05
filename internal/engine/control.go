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
func bootstrapNamespaceDumpable(d *namespaceDescriptor, bootstrap PrincipalRef) bool {
	if d.owner != bootstrap || !d.acl.Explicit {
		return false
	}
	publicRights := UsagePrivilege
	if d.address.ObjectID == PublicNamespaceOID {
		publicRights |= CreatePrivilege
	}
	var publicUse, publicOptions, ownerUse, ownerOptions Privileges
	for _, entry := range d.acl.Entries {
		if entry.Grantor != bootstrap {
			return false
		}
		if entry.Grantee.Public {
			if entry.Grantee.Role != (PrincipalRef{}) {
				return false
			}
			publicUse |= entry.Privileges
			publicOptions |= entry.GrantOptions
		} else {
			if entry.Grantee.Role != bootstrap {
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
		versions            []rowVersion
	}
	tables := make([]dumpRelation, 0)
	for a, d := range e.state.descriptors {
		r, ok := d.(*relationDescriptor)
		if !ok {
			if _, builtinType := d.(*typeDescriptor); builtinType {
				continue
			}
			if d.Kind() == NamespaceObject {
				ns, valid := d.(*namespaceDescriptor)
				if valid && (a.ObjectID == PublicNamespaceOID || a.ObjectID == PGCatalogNamespaceOID) &&
					!bootstrapNamespaceDumpable(ns, e.roles.names["postgres"]) {
					e.mu.Unlock()
					return fmt.Errorf("stuntgres: dump cannot serialize changed builtin namespace owner or ACL")
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
		tables = append(tables, dumpRelation{principal.Name, ns.name, r.name, r.columns, storage.versions})
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
			line.WriteString(quoteIdentifier(c.Name) + " " + c.Usage.Type.SQLName())
			if !c.Nullable {
				line.WriteString(" NOT NULL")
			}
		}
		line.WriteString(");\n")
		if err := emit(line.String()); err != nil {
			return err
		}
		for _, version := range t.versions {
			row := version.values
			line.Reset()
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
	statements, err := parser.Parse(string(data))
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
		case *parser.CreateTable:
		case *parser.SessionAuthorization:
			if !s.OwnerDirective {
				return sqlError("0A000", "restore accepts only named SET SESSION AUTHORIZATION owner directives")
			}
		case *parser.Insert:
			for _, row := range s.Rows {
				for _, v := range row {
					if v.Kind == parser.ParameterValue {
						return sqlError("42P02", "parameters are not allowed in restore")
					}
				}
			}
		default:
			return sqlError("0A000", "restore accepts only CREATE, INSERT and SET SESSION AUTHORIZATION")
		}
		_, err = stage.Execute(op, stmt, 0, nil)
		op.DetachNotices()
		if err != nil {
			return err
		}
	}
	_, err = e.ReplaceCatalog(ctx, stage.CatalogRead())
	return err
}
