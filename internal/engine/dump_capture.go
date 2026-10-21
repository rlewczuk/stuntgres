package engine

import (
	"context"
	"fmt"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/dump"
	"stuntgres/internal/engine/fdw"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/regobj"
	"stuntgres/internal/engine/sequence"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/table"
)

// dumpSchema is one ordinary namespace Dump creates.
type dumpSchema struct {
	name, owner string
	configured  bool
}

// dumpView is one owned definition statement: a view, extension or index.
type dumpView struct {
	owner, sql string
}

// dumpComment is a schema comment that differs from its baseline.
type dumpComment struct{ name, text string }

// dumpCapture is everything Dump reads from the engine. captureDumpLocked
// fills it under Engine.mu; after the unlock Dump reads only the capture.
type dumpCapture struct {
	// catalogView binds private key names until the terminal renames;
	// originalCatalog keeps the captured names for later definitions and
	// values.
	catalogView, originalCatalog catalog.Read
	originalRoles, domainRoles   authorization.Read
	constraintReplay             *partitionConstraintReplay
	recipePlan                   map[ids.ObjectAddress]*table.DumpRelation
	aclOutput                    regobj.ACLItemOutput

	// preamble reconstructs bootstrap identity changes before ordinary
	// namespaces are created.
	preamble       []string
	schemas        []dumpSchema
	schemaComments []dumpComment
	sequences      []sequence.Dump
	domains        []*catalog.Type
	collations     []*catalog.Collation
	conversions    []*catalog.Conversion
	operators      []*catalog.Operator
	sqlRoutines    []*catalog.Procedure
	tables         []table.DumpRelation
	materialized   []table.DumpRelation
	views          []dumpView
	indexes        []dumpView
	viewIndices    map[ids.OID]int
	tableIndices   map[ids.OID]int
	indexIndices   map[ids.OID]int
	composites     map[ids.OID]*catalog.Relation
	graph          dump.Graph
	// Deferral removes only eligible definitions' edges. Inline consumers keep
	// their prerequisites, including providers shared with deferred defaults.
	inlinePredecessors map[ids.OID][]ids.OID
	deferDefaults      bool
}

// dumpSource is the engine state the collectors read. It is valid only
// while captureDumpLocked holds Engine.mu.
type dumpSource struct {
	state     *catalog.State
	relations map[ids.ObjectAddress]*storage.Relation
	sequences map[ids.ObjectAddress]*storage.SequenceGenerator
	frontier  ids.CommitSeq
	bootstrap authorization.PrincipalRef
	foreign   fdw.Registry
}

// dumpCollection is one locked collection pass: the capture the collectors
// fill and the engine state they read. It never outlives Engine.mu.
type dumpCollection struct {
	*dumpCapture
	source dumpSource
}

// captureDumpLocked reads the catalog, roles, storage and sequence state Dump
// needs. The caller holds Engine.mu and releases it once on return.
func (e *Engine) captureDumpLocked(ctx context.Context) (*dumpCapture, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	state := e.catalog.state
	if err := dumpPortable(state); err != nil {
		return nil, err
	}
	catalogView := e.catalog.read(state)
	originalCatalog := catalogView
	originalRoles := e.roles.read(e.catalog.owner(), originalCatalog, e.roles.published)
	recipePlan, err := table.CapturedGenerationReplayPlan(originalCatalog)
	if err != nil {
		return nil, err
	}
	aclOutput := regobj.ACLItemOutput{Owner: e.catalog.owner(), Roles: originalRoles, Lossless: true}
	ctx = regobj.ACLItemOutputContext(ctx, aclOutput)
	if err := preflightACLItemDump(ctx, originalCatalog, e.storage.Relations, e.visibility.CommitSeq(), aclOutput); err != nil {
		return nil, err
	}
	// Declarations bind private key names until the terminal renames. Later
	// definitions and values must render against the original captured names.
	catalogView, constraintReplay := partitionConstraintDumpCatalog(dumpResolver{}, catalogView)
	domainRoles := e.roles.read(e.catalog.owner(), catalogView, e.roles.published)
	if err := e.validateTemporaryCollationDump(ctx, catalogView); err != nil {
		return nil, err
	}
	for address, name := range e.foreign.NamespaceEntries() {
		ns, ok := state.Namespace(address)
		if !ok || ns.Name() != name {
			return nil, fmt.Errorf("stuntgres: dump cannot serialize a renamed or retired configured foreign namespace")
		}
	}
	preamble, err := dumpBuiltinSchemaPreamble(state, domainRoles)
	if err != nil {
		return nil, err
	}
	c := &dumpCollection{
		dumpCapture: &dumpCapture{
			catalogView:      catalogView,
			originalCatalog:  originalCatalog,
			originalRoles:    originalRoles,
			domainRoles:      domainRoles,
			constraintReplay: constraintReplay,
			recipePlan:       recipePlan,
			aclOutput:        aclOutput,
			preamble:         preamble,
			tables:           make([]table.DumpRelation, 0),
			viewIndices:      make(map[ids.OID]int),
			tableIndices:     make(map[ids.OID]int),
			indexIndices:     make(map[ids.OID]int),
			composites:       make(map[ids.OID]*catalog.Relation),
			graph:            make(dump.Graph),
		},
		source: dumpSource{
			state:     state,
			relations: e.storage.Relations,
			sequences: e.storage.Sequences,
			frontier:  e.visibility.CommitSeq(),
			bootstrap: e.roles.domain.Principal(authorization.BootstrapPrincipalID),
			foreign:   e.foreign,
		},
	}
	for a, d := range state.Descriptors() {
		if err := c.collect(ctx, a, d); err != nil {
			return nil, err
		}
	}
	return c.dumpCapture, nil
}

// dumpPortable rejects catalogs Dump cannot serialize at all: SQL/MED
// metadata, default privileges and invalid indexes.
func dumpPortable(state *catalog.State) error {
	for _, descriptor := range state.Descriptors() {
		_, foreign := descriptor.(*catalog.ForeignObject)
		if relation, ok := descriptor.(*catalog.Relation); ok && relation.Foreign != nil {
			foreign = true
		}
		if foreign {
			return fmt.Errorf("stuntgres: dump cannot serialize SQL/MED foreign metadata")
		}
	}
	if state.DefaultACLCount() != 0 {
		return fmt.Errorf("stuntgres: dump cannot serialize default privileges")
	}
	var invalid *catalog.Relation
	var invalidSchema string
	for _, descriptor := range state.Descriptors() {
		index, ok := descriptor.(*catalog.Relation)
		if !ok || index.SQLIndex == nil {
			continue
		}
		namespace := state.MustNamespace(index.Namespace())
		if namespace.Temporary != (ids.SessionToken{}) || index.SQLIndex.Ready && index.SQLIndex.Valid && index.SQLIndex.Live {
			continue
		}
		parent, ok := state.Relation(index.SQLIndex.Parent)
		if ok && parent.Partition != nil && index.SQLIndex.Ready && index.SQLIndex.Live {
			continue
		}
		if invalid == nil || namespace.Name() < invalidSchema || namespace.Name() == invalidSchema && index.Name() < invalid.Name() {
			invalid, invalidSchema = index, namespace.Name()
		}
	}
	if invalid != nil {
		return fmt.Errorf("stuntgres: dump cannot serialize invalid index %s.%s", catalog.QuoteIdentifier(invalidSchema), catalog.QuoteIdentifier(invalid.Name()))
	}
	return nil
}

// dumpBuiltinSchemaPreamble reconstructs bootstrap identity changes before
// ordinary namespaces are created. These statements run only in Restore's
// isolated catalog under bootstrap authority; ACLs are reproduced by normal
// owner-transfer semantics.
func dumpBuiltinSchemaPreamble(state *catalog.State, roles authorization.Read) ([]string, error) {
	var preamble []string
	type builtinSchema struct {
		original, name, owner, baselineOwner string
		moved                                bool
	}
	var builtins []builtinSchema
	for _, b := range []struct {
		oid  ids.OID
		name string
	}{{ids.PublicNamespaceOID, "public"}, {ids.PGCatalogNamespaceOID, "pg_catalog"}, {ids.PGToastNamespaceOID, "pg_toast"}, {13229, "information_schema"}} {
		d, exists := state.Find(ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: b.oid})
		if !exists {
			preamble = append(preamble, "DROP SCHEMA "+catalog.QuoteIdentifier(b.name)+";\n")
			continue
		}
		ns := d.(*catalog.Namespace)
		role, err := roles.Descriptor(ns.Owner)
		if err != nil {
			return nil, err
		}
		baselineOwner := "postgres"
		if b.oid == ids.PublicNamespaceOID {
			baselineOwner = "pg_database_owner"
		}
		builtins = append(builtins, builtinSchema{original: b.name, name: ns.Name(), owner: role.Name, baselineOwner: baselineOwner})
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
			preamble = append(preamble, "ALTER SCHEMA "+catalog.QuoteIdentifier(b.original)+" RENAME TO "+catalog.QuoteIdentifier(b.name)+";\n")
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
				if _, exists := state.LookupName(ids.NameKey{ClassID: ids.NamespaceClassID, Name: candidate}); !exists {
					temporary = candidate
					break
				}
			}
			preamble = append(preamble, "ALTER SCHEMA "+catalog.QuoteIdentifier(b.original)+" RENAME TO "+catalog.QuoteIdentifier(temporary)+";\n")
			b.original = temporary
			break
		}
	}
	for _, b := range builtins {
		if b.owner != b.baselineOwner {
			preamble = append(preamble, "ALTER SCHEMA "+catalog.QuoteIdentifier(b.name)+" OWNER TO "+catalog.QuoteIdentifier(b.owner)+";\n")
		}
	}
	return preamble, nil
}
