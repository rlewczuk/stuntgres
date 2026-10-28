package control

import (
	"context"
	"fmt"
	"slices"
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
	{"event trigger", func(d catalog.Descriptor) bool { _, ok := d.(*catalog.EventTrigger); return ok }, collectEventTriggerDump},
	{"subscription", func(d catalog.Descriptor) bool { _, ok := d.(*catalog.Subscription); return ok }, collectSubscriptionDump},
	{"large object", isDumpLargeObject, collectLargeObjectDump},
	{"access method", isDumpAccessMethod, collectAccessMethodDump},
	{"operator family", isDumpOperatorFamily, collectOperatorFamilyDump},
	{"operator", isDumpOperator, collectOperatorDump},
	{"cast", isDumpCast, collectCastDump},
	{"procedure", isDumpProcedure, collectProcedureDump},
	{"configuration", func(d catalog.Descriptor) bool { _, ok := d.(*catalog.TSConfiguration); return ok }, collectConfigurationDump},
	{"text search", isDumpTextSearch, collectTextSearchDump},
	{"extension", isDumpExtension, collectExtensionDump},
	{"type", isDumpType, collectTypeDump},
	{"collation", isDumpCollation, collectCollationDump},
	{"conversion", isDumpConversion, collectConversionDump},
	{"statistics", func(d catalog.Descriptor) bool { _, ok := d.(*catalog.Statistics); return ok }, collectStatisticsDump},
	{"namespace", isDumpNamespace, collectNamespaceDump},
}

func temporaryDumpObject(state *catalog.State, d catalog.Descriptor) bool {
	ns, ok := state.Namespace(d.Namespace())
	return ok && ns.Temporary != (ids.SessionToken{})
}

// collect hands one descriptor to the first matching Collect entry.
// Objects in temporary namespaces are never dumped.
func (c *dumpCollection) collect(ctx context.Context, a ids.ObjectAddress, d catalog.Descriptor) error {
	if temporaryDumpObject(c.source.State, d) {
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
	if err := catalog.Validate(catalog.LargeObjectProvider{}, c.CatalogView, object); err != nil {
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

func collectParameterDump(_ context.Context, c *dumpCollection, _ ids.ObjectAddress, d catalog.Descriptor) error {
	provider := c.CatalogView.Owner().Provider(ids.ParameterClassID)
	if provider == nil {
		return fmt.Errorf("stuntgres: missing parameter catalog provider")
	}
	if err := catalog.Validate(provider, c.CatalogView, d); err != nil {
		return err
	}
	return access.ValidateACL(c.OriginalRoles, d)
}

func collectTablespaceDump(_ context.Context, c *dumpCollection, _ ids.ObjectAddress, d catalog.Descriptor) error {
	t := d.(*catalog.Tablespace)
	if err := catalog.Validate(catalog.TablespaceProvider{}, c.CatalogView, t); err != nil {
		return err
	}
	return nil
}

func collectLanguageDump(_ context.Context, c *dumpCollection, _ ids.ObjectAddress, descriptor catalog.Descriptor) error {
	d := descriptor.(*catalog.Language)
	if err := catalog.Validate(catalog.LanguageProvider{}, c.CatalogView, d); err != nil {
		return err
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
	if t.RangeConstructor != nil {
		if err := catalog.Validate(stored.ProcedureProvider{}, c.CatalogView, t); err != nil {
			return err
		}
		if t.Owner != c.source.Bootstrap || t.ACL.Explicit {
			return fmt.Errorf("stuntgres: dump cannot serialize altered generated constructor authority")
		}
		return c.collectProvider(t)
	}
	if t.PLHandler != catalog.NoPLHandler {
		if err := catalog.ValidatePLHandler(c.CatalogView, t); err != nil {
			return err
		}
		if t.Owner != c.source.Bootstrap || t.ACL.Explicit {
			return fmt.Errorf("stuntgres: dump cannot serialize altered PL handler authority")
		}
		return c.collectProvider(t)
	}
	if t.DynamicCallable() {
		if err := catalog.Validate(stored.ProcedureProvider{}, c.CatalogView, t); err != nil {
			return err
		}
		c.OwnedRoutines = append(c.OwnedRoutines, t)
		return nil
	}
	if t.Foreign != nil {
		if err := catalog.Validate(stored.ProcedureProvider{}, c.CatalogView, t); err != nil {
			return err
		}
		return c.collectProvider(t)
	}
	if t.Builtin != nil {
		if err := catalog.Validate(stored.ProcedureProvider{}, c.CatalogView, t); err != nil {
			return err
		}
		return c.collectProvider(t)
	}
	if t.AccessMethod != catalog.NoAccessMethod || t.BrinSupport != 0 {
		if err := catalog.Validate(stored.ProcedureProvider{}, c.CatalogView, t); err != nil {
			return err
		}
		return c.collectProvider(t)
	}
	if t.Conversion.Name() != "" {
		if err := catalog.Validate(stored.ProcedureProvider{}, c.CatalogView, t); err != nil {
			return err
		}
		return c.collectProvider(t)
	}
	if catalog.ExtensionMembership(t) == (ids.ObjectAddress{}) {
		return fmt.Errorf("stuntgres: dump cannot serialize loose extension member")
	}
	return c.collectProvider(t)
}

func collectTextSearchDump(_ context.Context, c *dumpCollection, _ ids.ObjectAddress, d catalog.Descriptor) error {
	if err := catalog.Validate(extension.CatalogProvider{Class: d.Address().ClassID}, c.CatalogView, d); err != nil {
		return err
	}
	if catalog.ExtensionMembership(d) != (ids.ObjectAddress{}) {
		return nil
	}
	switch d := d.(type) {
	case *catalog.TSTemplate:
		if d.Seeded() {
			return nil
		}
		c.Templates = append(c.Templates, d)
		event := dump.Event{OID: d.Address().ObjectID}
		c.Graph[event] = nil
		for _, address := range [3]ids.ObjectAddress{d.Namespace(), d.Init, d.Lexize} {
			if address == (ids.ObjectAddress{}) {
				continue
			}
			oid, err := predecessor(c.CatalogView, address, d.Address())
			if err != nil {
				return err
			}
			dependency := dump.Event{OID: oid}
			if oid != 0 && oid != event.OID && !slices.Contains(c.Graph[event], dependency) {
				c.Graph[event] = append(c.Graph[event], dependency)
			}
		}
	case *catalog.TSDictionary:
		if d.Seeded() {
			return nil
		}
		c.Dictionaries = append(c.Dictionaries, d)
		event := dump.Event{OID: d.Address().ObjectID}
		c.Graph[event] = nil
		oid, err := predecessor(c.CatalogView, d.Template, d.Address())
		if err != nil {
			return err
		}
		if oid != 0 {
			c.Graph[event] = []dump.Event{{OID: oid}}
		}
	}
	return nil
}

func collectExtensionDump(_ context.Context, c *dumpCollection, _ ids.ObjectAddress, d catalog.Descriptor) error {
	t := d.(*catalog.Extension)
	if err := catalog.Validate(extension.CatalogProvider{Class: ids.ExtensionClassID}, c.CatalogView, t); err != nil {
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
	if t.SQLType.IsMultirange() && t.SQLType.IsUserDefined() {
		return catalog.Validate(stored.TypeProvider{}, c.CatalogView, t)
	}
	if err := catalog.Validate(stored.TypeProvider{}, c.CatalogView, t); err != nil {
		return err
	}
	if t.SQLType.IsHstore() || t.SQLType.IsSeg() || t.SQLType.IsQueryInt() || (t.SQLType.IsLquery() || t.SQLType.IsLtree() || t.SQLType.IsLtxtquery()) || t.SQLType.IsCube() || t.SQLType.IsCitext() || t.SQLType.IsEan13() || t.SQLType.IsIsbn() || t.SQLType.IsIsbn13() || t.SQLType.IsIsmn() || t.SQLType.IsIsmn13() || t.SQLType.IsIssn() || t.SQLType.IsIssn13() || t.SQLType.IsUpc() || t.Extension != (ids.ObjectAddress{}) {
		return catalog.Validate(stored.TypeProvider{}, c.CatalogView, t)
	}
	if t.SQLType.IsUserDefined() && !t.SQLType.IsComposite() {
		c.Domains = append(c.Domains, t)
	} else if t.SQLType.IsArray() {
		if err := catalog.Validate(stored.TypeProvider{}, c.CatalogView, t); err != nil {
			return err
		}
	} else if t.Namespace().ObjectID != ids.PGCatalogNamespaceOID && t.Namespace().ObjectID != 13229 && t.OwningRelation == (ids.ObjectAddress{}) {
		return fmt.Errorf("stuntgres: dump cannot serialize unsupported user type")
	}
	return nil
}

func collectCollationDump(_ context.Context, c *dumpCollection, _ ids.ObjectAddress, d catalog.Descriptor) error {
	t := d.(*catalog.Collation)
	if err := catalog.Validate(catalog.CollationProvider{}, c.CatalogView, t); err != nil {
		return err
	}
	if !t.Seeded() {
		c.Collations = append(c.Collations, t)
	}
	return nil
}

func collectConversionDump(_ context.Context, c *dumpCollection, _ ids.ObjectAddress, d catalog.Descriptor) error {
	t := d.(*catalog.Conversion)
	if err := catalog.Validate(catalog.ConversionProvider{}, c.CatalogView, t); err != nil {
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
	if err := catalog.Validate(catalog.AccessMethodProvider{}, c.CatalogView, method); err != nil {
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
	if err := catalog.Validate(catalog.NamespaceProvider{}, c.CatalogView, d); err != nil {
		return err
	}
	if valid && ns.Comment != catalog.NamespaceCommentBaseline(a.ObjectID) {
		c.SchemaComments = append(c.SchemaComments, SchemaComment{ns.Name(), ns.Comment})
	}
	if valid && a.ObjectID != ids.PublicNamespaceOID && a.ObjectID != ids.PGCatalogNamespaceOID && a.ObjectID != ids.PGToastNamespaceOID && a.ObjectID != 13229 {
		role, err := c.DomainRoles.Descriptor(ns.Owner)
		if err != nil {
			return err
		}
		_, configured := c.source.Foreign.NamespaceName(a)
		c.Schemas = append(c.Schemas, Schema{Name: ns.Name(), Owner: role.Name, Configured: configured})
	}
	return nil
}

// BootstrapNamespaceDumpable compares the effective bootstrap ACL payload for
// baseline consumers. It is not an admission gate: serialization preserves raw
// entry multiplicity and all changed authority through exact catalog repairs.
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
