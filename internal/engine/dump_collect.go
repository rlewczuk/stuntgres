package engine

import (
	"context"
	"fmt"
	"strings"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/control"
	"stuntgres/internal/engine/dump"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/engine/typedef"
)

// dumpCollector is one entry of Dump's Collect table. The first entry whose
// matches accepts a descriptor collects it; later entries never see it.
type dumpCollector struct {
	name    string // for tests and error context only
	matches func(catalog.Descriptor) bool
	collect func(context.Context, *dumpCollection, ids.ObjectAddress, catalog.Descriptor) error
}

// dumpCollectors lists the descriptor kinds Dump accepts, in check order.
// A descriptor no entry matches is an unsupported kind.
var dumpCollectors = [...]dumpCollector{
	{"relation", isDumpRelation, collectRelationDump},
	{"database", isDumpDatabase, collectDatabaseDump},
	{"parameter", isDumpParameter, collectParameterDump},
	{"tablespace", isDumpTablespace, collectTablespaceDump},
	{"large object", isDumpLargeObject, collectLargeObjectDump},
	{"operator", isDumpOperator, collectOperatorDump},
	{"procedure", isDumpProcedure, collectProcedureDump},
	{"text search", isDumpTextSearch, collectTextSearchDump},
	{"extension", isDumpExtension, collectExtensionDump},
	{"type", isDumpType, collectTypeDump},
	{"collation", isDumpCollation, collectCollationDump},
	{"conversion", isDumpConversion, collectConversionDump},
	{"namespace", isDumpNamespace, collectNamespaceDump},
}

// collect hands one descriptor to the first matching Collect entry.
// Objects in temporary namespaces are never dumped.
func (c *dumpCollection) collect(ctx context.Context, a ids.ObjectAddress, d catalog.Descriptor) error {
	if ns, ok := c.source.state.Namespace(d.Namespace()); ok && ns.Temporary != (ids.SessionToken{}) {
		return nil
	}
	for _, collector := range dumpCollectors {
		if collector.matches(d) {
			return collector.collect(ctx, c, a, d)
		}
	}
	return fmt.Errorf("stuntgres: unsupported catalog descriptor kind")
}

func isDumpRelation(d catalog.Descriptor) bool {
	_, ok := d.(*catalog.Relation)
	return ok
}

func isDumpDatabase(d catalog.Descriptor) bool {
	_, ok := d.(*catalog.Database)
	return ok
}

func isDumpParameter(d catalog.Descriptor) bool {
	_, ok := d.(*catalog.Parameter)
	return ok
}

func isDumpTablespace(d catalog.Descriptor) bool {
	_, ok := d.(*catalog.Tablespace)
	return ok
}

func isDumpLargeObject(d catalog.Descriptor) bool {
	_, ok := d.(*catalog.LargeObject)
	return ok
}

func collectLargeObjectDump(_ context.Context, c *dumpCollection, _ ids.ObjectAddress, d catalog.Descriptor) error {
	object := d.(*catalog.LargeObject)
	if err := (catalog.LargeObjectProvider{}).Validate(c.CatalogView, object); err != nil {
		return err
	}
	if err := access.ValidateACL(c.OriginalRoles, object); err != nil {
		return err
	}
	c.LargeObjects = append(c.LargeObjects, object)
	return nil
}

func isDumpOperator(d catalog.Descriptor) bool {
	_, ok := d.(*catalog.Operator)
	return ok
}

func isDumpProcedure(d catalog.Descriptor) bool {
	_, ok := d.(*catalog.Procedure)
	return ok
}

func isDumpTextSearch(d catalog.Descriptor) bool {
	switch d.(type) {
	case *catalog.TSTemplate, *catalog.TSDictionary:
		return true
	}
	return false
}

func isDumpExtension(d catalog.Descriptor) bool {
	_, ok := d.(*catalog.Extension)
	return ok
}

func isDumpType(d catalog.Descriptor) bool {
	_, ok := d.(*catalog.Type)
	return ok
}

func isDumpCollation(d catalog.Descriptor) bool {
	_, ok := d.(*catalog.Collation)
	return ok
}

func isDumpConversion(d catalog.Descriptor) bool {
	_, ok := d.(*catalog.Conversion)
	return ok
}

func isDumpNamespace(d catalog.Descriptor) bool { return d.Kind() == ids.NamespaceObject }

func collectDatabaseDump(context.Context, *dumpCollection, ids.ObjectAddress, catalog.Descriptor) error {
	return nil // Destination attachment authority is never serialized.
}

func collectParameterDump(context.Context, *dumpCollection, ids.ObjectAddress, catalog.Descriptor) error {
	return fmt.Errorf("stuntgres: dump cannot serialize parameter ACL")
}

func collectTablespaceDump(_ context.Context, c *dumpCollection, _ ids.ObjectAddress, d catalog.Descriptor) error {
	t := d.(*catalog.Tablespace)
	if err := (catalog.TablespaceProvider{}).Validate(c.CatalogView, t); err != nil {
		return err
	}
	if t.Owner != c.source.bootstrap || t.ACL.Explicit {
		return fmt.Errorf("stuntgres: dump cannot serialize altered tablespace authority")
	}
	return nil
}

func collectOperatorDump(_ context.Context, c *dumpCollection, _ ids.ObjectAddress, d catalog.Descriptor) error {
	t := d.(*catalog.Operator)
	dumped, err := routine.OperatorDumped(c.CatalogView, t)
	if dumped {
		c.Operators = append(c.Operators, t)
	}
	return err
}

func collectProcedureDump(_ context.Context, c *dumpCollection, _ ids.ObjectAddress, d catalog.Descriptor) error {
	t := d.(*catalog.Procedure)
	if t.SQL != nil {
		if err := (stored.ProcedureProvider{}).Validate(c.CatalogView, t); err != nil {
			return err
		}
		c.SQLRoutines = append(c.SQLRoutines, t)
		return nil
	}
	// Native foreign and conversion capabilities belong to the bootstrap
	// principal (10).
	if t.Foreign != nil {
		if err := (stored.ProcedureProvider{}).Validate(c.CatalogView, t); err != nil {
			return err
		}
		if t.ACL.Explicit || t.Owner != c.source.bootstrap {
			return fmt.Errorf("stuntgres: dump cannot serialize altered native foreign capability authority")
		}
		return nil
	}
	if t.Builtin != nil {
		if err := (stored.ProcedureProvider{}).Validate(c.CatalogView, t); err != nil {
			return err
		}
		if t.Owner != c.source.bootstrap || t.ACL.Explicit {
			return fmt.Errorf("stuntgres: dump cannot serialize altered builtin routine authority")
		}
		return nil
	}
	if t.Conversion.Name() != "" {
		if err := (stored.ProcedureProvider{}).Validate(c.CatalogView, t); err != nil {
			return err
		}
		if t.Owner != c.source.bootstrap || t.ACL.Explicit {
			return fmt.Errorf("stuntgres: dump cannot serialize altered builtin conversion routine authority")
		}
		return nil
	}
	if t.ACL.Explicit {
		return fmt.Errorf("stuntgres: dump cannot serialize explicit routine ACL")
	}
	if catalog.ExtensionMembership(t) == (ids.ObjectAddress{}) {
		return fmt.Errorf("stuntgres: dump cannot serialize loose extension member")
	}
	return nil
}

func collectTextSearchDump(_ context.Context, _ *dumpCollection, _ ids.ObjectAddress, d catalog.Descriptor) error {
	if catalog.ExtensionMembership(d) == (ids.ObjectAddress{}) {
		return fmt.Errorf("stuntgres: dump cannot serialize loose extension member")
	}
	return nil
}

func collectExtensionDump(_ context.Context, c *dumpCollection, _ ids.ObjectAddress, d catalog.Descriptor) error {
	t := d.(*catalog.Extension)
	if err := (extension.CatalogProvider{Class: ids.ExtensionClassID}).Validate(c.CatalogView, t); err != nil {
		return err
	}
	ns := c.CatalogView.MustNamespace(t.TargetNamespace)
	if ns.Temporary != (ids.SessionToken{}) {
		return fmt.Errorf("stuntgres: temporary extension installation is not portable")
	}
	principal, err := c.DomainRoles.Descriptor(t.Owner)
	if err != nil {
		return err
	}
	metadata, err := extension.OwnerMetadata(c.CatalogView, c.DomainRoles, t)
	if err != nil {
		return err
	}
	text := "CREATE EXTENSION " + catalog.QuoteIdentifier(t.Name()) + " WITH SCHEMA " + catalog.QuoteIdentifier(ns.Name()) + " VERSION '" + strings.ReplaceAll(t.Version, "'", "''") + "';\n" + metadata
	c.Graph[dump.Event{OID: t.Address().ObjectID}] = nil
	c.ViewIndices[t.Address().ObjectID] = len(c.Views)
	c.Views = append(c.Views, control.OwnedStatement{principal.Name, text})
	return nil
}

func collectTypeDump(_ context.Context, c *dumpCollection, _ ids.ObjectAddress, d catalog.Descriptor) error {
	t := d.(*catalog.Type)
	// A recipe can regenerate synthetic type defaults, never a real
	// explicit ACL (even one equal to the effective baseline).
	if t.ACL.Explicit && !typedef.DefaultDomainACL(t) {
		message := "stuntgres: dump cannot serialize changed type ACL"
		if t.SQLType.IsDomain() || t.SQLType.IsEnum() {
			message = "stuntgres: dump cannot serialize changed domain ACL"
		} else if t.SQLType.IsComposite() {
			message = "stuntgres: dump cannot serialize changed composite ACL"
		}
		return fmt.Errorf("%s", message)
	}
	if t.SQLType.IsQueryInt() || t.SQLType.IsCube() || t.Extension != (ids.ObjectAddress{}) {
		return (stored.TypeProvider{}).Validate(c.CatalogView, t)
	}
	if t.SQLType.IsUserDefined() && !t.SQLType.IsComposite() {
		c.Domains = append(c.Domains, t)
	} else if t.SQLType.IsArray() {
		if err := (stored.TypeProvider{}).Validate(c.CatalogView, t); err != nil {
			return err
		}
	} else if t.Namespace().ObjectID != ids.PGCatalogNamespaceOID && t.Namespace().ObjectID != 13229 && t.OwningRelation == (ids.ObjectAddress{}) {
		return fmt.Errorf("stuntgres: dump cannot serialize unsupported user type")
	}
	return nil
}

func collectCollationDump(_ context.Context, c *dumpCollection, _ ids.ObjectAddress, d catalog.Descriptor) error {
	t := d.(*catalog.Collation)
	if err := (catalog.CollationProvider{}).Validate(c.CatalogView, t); err != nil {
		return err
	}
	if !t.Seeded() {
		c.Collations = append(c.Collations, t)
	}
	return nil
}

func collectConversionDump(_ context.Context, c *dumpCollection, _ ids.ObjectAddress, d catalog.Descriptor) error {
	t := d.(*catalog.Conversion)
	if err := (catalog.ConversionProvider{}).Validate(c.CatalogView, t); err != nil {
		return err
	}
	if !t.Seeded() {
		c.Conversions = append(c.Conversions, t)
	}
	return nil
}

func collectNamespaceDump(_ context.Context, c *dumpCollection, a ids.ObjectAddress, d catalog.Descriptor) error {
	ns, valid := d.(*catalog.Namespace)
	if valid && ns.Temporary != (ids.SessionToken{}) {
		return nil
	}
	if valid && ns.Comment != catalog.NamespaceCommentBaseline(a.ObjectID) {
		c.SchemaComments = append(c.SchemaComments, control.SchemaComment{ns.Name(), ns.Comment})
	}
	if valid && (a.ObjectID == ids.PublicNamespaceOID || a.ObjectID == ids.PGCatalogNamespaceOID || a.ObjectID == ids.PGToastNamespaceOID || a.ObjectID == 13229) &&
		!bootstrapNamespaceDumpable(ns) {
		return fmt.Errorf("stuntgres: dump cannot serialize changed builtin namespace ACL")
	}
	if valid && a.ObjectID != ids.PublicNamespaceOID && a.ObjectID != ids.PGCatalogNamespaceOID && a.ObjectID != ids.PGToastNamespaceOID && a.ObjectID != 13229 {
		if ns.ACL.Explicit {
			return fmt.Errorf("stuntgres: dump cannot serialize explicit namespace ACL")
		}
		role, err := c.DomainRoles.Descriptor(ns.Owner)
		if err != nil {
			return err
		}
		_, configured := c.source.foreign.NamespaceName(a)
		c.Schemas = append(c.Schemas, control.Schema{Name: ns.Name(), Owner: role.Name, Configured: configured})
	}
	return nil
}

// Namespace ACL entry order and split entries do not affect reproducibility.
// Principal/grantor references do: losing them would change DROP dependencies.
func bootstrapNamespaceDumpable(d *catalog.Namespace) bool {
	if d.Address().ObjectID == ids.PGToastNamespaceOID {
		return !d.ACL.Explicit && len(d.ACL.Entries) == 0
	}
	if !d.ACL.Explicit {
		return false
	}
	publicRights := authorization.UsagePrivilege
	var publicUse, publicOptions, ownerUse, ownerOptions authorization.Privileges
	for _, entry := range d.ACL.Entries {
		if entry.Grantor != d.Owner {
			return false
		}
		if entry.Grantee.Public {
			if entry.Grantee.Role != (authorization.PrincipalRef{}) {
				return false
			}
			publicUse |= entry.Privileges
			publicOptions |= entry.GrantOptions
		} else {
			if entry.Grantee.Role != d.Owner {
				return false
			}
			ownerUse |= entry.Privileges
			ownerOptions |= entry.GrantOptions
		}
	}
	return publicUse == publicRights && publicOptions == 0 &&
		ownerUse == authorization.UsagePrivilege|authorization.CreatePrivilege && ownerOptions == 0
}
