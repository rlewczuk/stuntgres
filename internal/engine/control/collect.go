package control

import (
	"context"
	"fmt"
	"strings"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/dump"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/fdw"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/partition"
	"stuntgres/internal/engine/regobj"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/engine/typedef"
)

// Source is what the Collect tables read: the capture's catalog and role
// reads, the generation recipe plan, and locked engine state. The root fills
// it under Engine.mu and calls Collect inside that window; it never outlives
// the window.
type Source struct {
	// CatalogView, OriginalCatalog, the two role reads, ConstraintReplay and
	// ACLOutput become the Capture's fields of the same names.
	CatalogView, OriginalCatalog catalog.Read
	OriginalRoles, DomainRoles   authorization.Read
	ConstraintReplay             *partition.ConstraintReplay
	ACLOutput                    regobj.ACLItemOutput
	// RecipePlan is the captured generation replay plan, by table.
	RecipePlan map[ids.ObjectAddress]*table.DumpRelation

	State     *catalog.State
	Relations map[ids.ObjectAddress]*storage.Relation
	Sequences map[ids.ObjectAddress]*storage.SequenceGenerator
	Frontier  ids.CommitSeq
	Bootstrap authorization.PrincipalRef
	Foreign   fdw.Registry
}

// dumpCollection is one Collect pass: the capture the collectors fill and
// the source they read. It never outlives Engine.mu.
type dumpCollection struct {
	*Capture
	source Source
}

// Collect builds the builtin-schema preamble, then hands every descriptor of
// source.State to the Collect tables and returns the filled capture. ctx
// carries the ACL output context the renderers read.
func Collect(ctx context.Context, source Source) (*Capture, error) {
	preamble, err := dumpBuiltinSchemaPreamble(source.State, source.DomainRoles)
	if err != nil {
		return nil, err
	}
	c := &dumpCollection{
		Capture: &Capture{
			CatalogView:      source.CatalogView,
			OriginalCatalog:  source.OriginalCatalog,
			OriginalRoles:    source.OriginalRoles,
			DomainRoles:      source.DomainRoles,
			ConstraintReplay: source.ConstraintReplay,
			ACLOutput:        source.ACLOutput,
			Preamble:         preamble,
			Tables:           make([]table.DumpRelation, 0),
			ViewIndices:      make(map[ids.OID]int),
			TableIndices:     make(map[ids.OID]int),
			IndexIndices:     make(map[ids.OID]int),
			Composites:       make(map[ids.OID]*catalog.Relation),
			Graph:            make(dump.Graph),
		},
		source: source,
	}
	for a, d := range source.State.Descriptors() {
		if err := c.collect(ctx, a, d); err != nil {
			return nil, err
		}
	}
	return c.Capture, nil
}

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
	{"foreign object", isDumpForeign, collectForeignDump},
	{"relation", isDumpRelation, collectRelationDump},
	{"database", isDumpDatabase, collectDatabaseDump},
	{"parameter", isDumpParameter, collectParameterDump},
	{"tablespace", isDumpTablespace, collectTablespaceDump},
	{"language", func(d catalog.Descriptor) bool { _, ok := d.(*catalog.Language); return ok }, collectLanguageDump},
	{"large object", isDumpLargeObject, collectLargeObjectDump},
	{"access method", isDumpAccessMethod, collectAccessMethodDump},
	{"operator family", isDumpOperatorFamily, collectOperatorFamilyDump},
	{"operator", isDumpOperator, collectOperatorDump},
	{"cast", isDumpCast, collectCastDump},
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
	if ns, ok := c.source.State.Namespace(d.Namespace()); ok && ns.Temporary != (ids.SessionToken{}) {
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
	if t.Owner != c.source.Bootstrap || t.ACL.Explicit {
		return fmt.Errorf("stuntgres: dump cannot serialize altered tablespace authority")
	}
	return nil
}

func collectLanguageDump(_ context.Context, c *dumpCollection, _ ids.ObjectAddress, descriptor catalog.Descriptor) error {
	d := descriptor.(*catalog.Language)
	if err := (catalog.LanguageProvider{}).Validate(c.CatalogView, d); err != nil {
		return err
	}
	if d.Owner != c.source.Bootstrap || d.ACL.Explicit {
		return fmt.Errorf("stuntgres: dump cannot serialize altered language authority")
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
	if t.PLHandler != catalog.NoPLHandler {
		if err := catalog.ValidatePLHandler(c.CatalogView, t); err != nil {
			return err
		}
		if t.Owner != c.source.Bootstrap || t.ACL.Explicit {
			return fmt.Errorf("stuntgres: dump cannot serialize altered PL handler authority")
		}
		return nil
	}
	if t.DynamicCallable() {
		if err := (stored.ProcedureProvider{}).Validate(c.CatalogView, t); err != nil {
			return err
		}
		c.OwnedRoutines = append(c.OwnedRoutines, t)
		return nil
	}
	if t.Foreign != nil {
		if err := (stored.ProcedureProvider{}).Validate(c.CatalogView, t); err != nil {
			return err
		}
		return c.collectProvider(t)
	}
	if t.Builtin != nil {
		if err := (stored.ProcedureProvider{}).Validate(c.CatalogView, t); err != nil {
			return err
		}
		return c.collectProvider(t)
	}
	if t.AccessMethod != catalog.NoAccessMethod {
		if err := (stored.ProcedureProvider{}).Validate(c.CatalogView, t); err != nil {
			return err
		}
		return c.collectProvider(t)
	}
	if t.Conversion.Name() != "" {
		if err := (stored.ProcedureProvider{}).Validate(c.CatalogView, t); err != nil {
			return err
		}
		return c.collectProvider(t)
	}
	if t.ACL.Explicit {
		return fmt.Errorf("stuntgres: dump cannot serialize explicit routine ACL")
	}
	if catalog.ExtensionMembership(t) == (ids.ObjectAddress{}) {
		return fmt.Errorf("stuntgres: dump cannot serialize loose extension member")
	}
	return c.collectProvider(t)
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
	required, err := catalog.ExtensionPrerequisites(c.CatalogView, t)
	if err != nil {
		return err
	}
	for _, predecessor := range required {
		c.Graph[dump.Event{OID: t.Address().ObjectID}] = append(c.Graph[dump.Event{OID: t.Address().ObjectID}], dump.Event{OID: predecessor.ObjectID})
	}
	c.ViewIndices[t.Address().ObjectID] = len(c.Views)
	c.Views = append(c.Views, OwnedStatement{principal.Name, text})
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
	if t.SQLType.IsQueryInt() || t.SQLType.IsCube() || t.SQLType.IsCitext() || t.Extension != (ids.ObjectAddress{}) {
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

func isDumpAccessMethod(d catalog.Descriptor) bool { _, ok := d.(*catalog.AccessMethod); return ok }
func collectAccessMethodDump(_ context.Context, c *dumpCollection, _ ids.ObjectAddress, d catalog.Descriptor) error {
	method := d.(*catalog.AccessMethod)
	if err := (catalog.AccessMethodProvider{}).Validate(c.CatalogView, method); err != nil {
		return err
	}
	if !catalog.AccessMethodSeeded(method) {
		c.AccessMethods = append(c.AccessMethods, method)
	}
	return nil
}

func collectNamespaceDump(_ context.Context, c *dumpCollection, a ids.ObjectAddress, d catalog.Descriptor) error {
	ns, valid := d.(*catalog.Namespace)
	if valid && ns.Temporary != (ids.SessionToken{}) {
		return nil
	}
	if valid && ns.Comment != catalog.NamespaceCommentBaseline(a.ObjectID) {
		c.SchemaComments = append(c.SchemaComments, SchemaComment{ns.Name(), ns.Comment})
	}
	if valid && (a.ObjectID == ids.PublicNamespaceOID || a.ObjectID == ids.PGCatalogNamespaceOID || a.ObjectID == ids.PGToastNamespaceOID || a.ObjectID == 13229) &&
		!BootstrapNamespaceDumpable(ns) {
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
		_, configured := c.source.Foreign.NamespaceName(a)
		c.Schemas = append(c.Schemas, Schema{Name: ns.Name(), Owner: role.Name, Configured: configured})
	}
	return nil
}

// BootstrapNamespaceDumpable reports whether a builtin namespace keeps an ACL
// Restore reproduces. Namespace ACL entry order and split entries do not
// affect reproducibility. Principal/grantor references do: losing them would
// change DROP dependencies.
func BootstrapNamespaceDumpable(d *catalog.Namespace) bool {
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
