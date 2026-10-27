package control

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/dump"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/regobj"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/sqltype"
)

// providerKey is a schema/name collision key with the routine input signature.
// Dictionary keys use an empty signature and a separate planner invocation.
type providerKey struct {
	schema, name string
	signature    catalog.ProcedureSignature
}
type providerReplay struct {
	original, current providerKey
	arguments, owner  string
	ownerChanged      bool
	dictionary        bool
	options           string // Rendered dictionary ALTER clause, after identity replay.
	parallel          byte
	cost, rows        float32
	readyAfter        ids.OID // Zero for seeded/installed providers; otherwise the creating type event.
}

func (c *dumpCollection) collectProvider(p *catalog.Procedure) error {
	ns, ok := c.CatalogView.Namespace(p.Namespace())
	if !ok {
		return fmt.Errorf("stuntgres: missing provider namespace")
	}
	r := providerReplay{current: providerKey{ns.Name(), p.Name(), p.Signature}}
	originalOwner := c.source.Bootstrap
	r.parallel, r.rows = p.Parallel, p.Rows
	if p.Cost != 0 && (p.Builtin == nil || p.Cost != routine.BuiltinRoutineCost(p.Builtin)) {
		r.cost = p.Cost
	}
	if p.Extension != (ids.ObjectAddress{}) {
		name, err := extension.ProcedureInstallationName(c.CatalogView, p)
		if err != nil {
			return err
		}
		ext, ok := c.CatalogView.Extension(p.Extension)
		if !ok {
			return fmt.Errorf("stuntgres: missing provider extension")
		}
		originalNS, ok := c.CatalogView.Namespace(ext.TargetNamespace)
		if !ok {
			return fmt.Errorf("stuntgres: missing provider installation namespace")
		}
		r.original = providerKey{originalNS.Name(), name, p.Signature}
		originalOwner = ext.Owner
	} else if p.RangeConstructor != nil {
		// Generated identities belong to the actual type generation, never
		// the released builtin OID domain. The resolver also collapses a
		// multirange constructor to its parent range's CREATE TYPE event.
		r.original = r.current
		var err error
		r.readyAfter, err = (Resolver{}).Predecessor(c.CatalogView, catalog.RangeConstructorOwner(p), p.Address())
		if err != nil {
			return err
		}
		if r.readyAfter == 0 {
			return fmt.Errorf("stuntgres: missing generated provider declaration")
		}
	} else if p.PLHandler != catalog.NoPLHandler {
		name, signature, _ := p.PLHandler.Declaration()
		r.original = providerKey{ns.Name(), name, signature}
	} else if p.Foreign != nil {
		r.original = providerKey{p.Foreign.Name.Schema, p.Foreign.Name.Name, p.Signature}
	} else {
		identity, ok := catalog.BuiltinRoutineIdentityByOID(p.Address().ObjectID)
		if !ok {
			return fmt.Errorf("stuntgres: missing released provider identity")
		}
		originalNS, ok := c.CatalogView.Namespace(ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: identity.Namespace})
		if !ok {
			return fmt.Errorf("stuntgres: missing released provider namespace")
		}
		r.original = providerKey{originalNS.Name(), identity.Name, identity.Signature}
	}
	r.ownerChanged = p.Owner != originalOwner
	if r.ownerChanged {
		owner, err := c.DomainRoles.Descriptor(p.Owner)
		if err != nil {
			return err
		}
		r.owner = owner.Name
	}
	if (r.original != r.current || r.ownerChanged || r.parallel != 0 || r.cost != 0 || r.rows > 0) && p.Signature != "" {
		var args strings.Builder
		for raw := range strings.SplitSeq(string(p.Signature), ",") {
			id, err := strconv.ParseUint(raw, 10, 32)
			if err != nil {
				return fmt.Errorf("stuntgres: invalid provider signature: %w", err)
			}
			_, ok := c.CatalogView.LookupType(sqltype.ID(id))
			if !ok {
				return fmt.Errorf("stuntgres: missing provider argument type")
			}
			// Routine identity admits pseudotypes such as internal; storage
			// declaration Usage deliberately does not. A nil visibility path
			// keeps non-keyword type names qualified by their actual catalog
			// namespace, including installed types with destination-local OIDs.
			if args.Len() != 0 {
				args.WriteString(", ")
			}
			args.WriteString(regobj.TypeSpelling(c.CatalogView, nil, ids.OID(id)))
		}
		r.arguments = args.String()
	}
	c.Providers = append(c.Providers, r)
	return nil
}

// unchangedProviders reports whether no provider has a statement to replay:
// each keeps its installed key, owner, options and planner metadata. Dump
// collects every builtin provider, and usually none changed.
func unchangedProviders(providers []providerReplay) bool {
	for _, p := range providers {
		if p.original != p.current || p.ownerChanged || p.options != "" || p.parallel != 0 || p.cost != 0 || p.rows > 0 {
			return false
		}
	}
	return true
}

// planProviderReplay frees destinations before occupying them. A temporary name
// is used only when every remaining first step is blocked: an occupied-key
// cycle, or a move/rename whose two intermediate keys are both occupied.
// Both intermediate schema/name keys and final keys are reserved when choosing it.
func planProviderReplay(providers []providerReplay) ([]string, error) {
	if unchangedProviders(providers) {
		// Nothing moves, so nothing is emitted; keep the duplicate check.
		// Each original key equals its current key.
		seen := make(map[providerKey]bool, len(providers))
		for _, p := range providers {
			if seen[p.current] {
				return nil, fmt.Errorf("stuntgres: duplicate final provider key")
			}
			seen[p.current] = true
		}
		return nil, nil
	}
	providers = slices.Clone(providers)
	slices.SortFunc(providers, func(a, b providerReplay) int {
		if n := strings.Compare(a.original.schema, b.original.schema); n != 0 {
			return n
		}
		if n := strings.Compare(a.original.name, b.original.name); n != 0 {
			return n
		}
		return strings.Compare(string(a.original.signature), string(b.original.signature))
	})
	occupied := make(map[providerKey]int, len(providers))
	reserved := make(map[providerKey]bool, len(providers)*2)
	pending := 0
	for _, p := range providers {
		if reserved[p.current] {
			return nil, fmt.Errorf("stuntgres: duplicate final provider key")
		}
		reserved[p.current] = true
	}
	for i, p := range providers {
		if _, exists := occupied[p.original]; exists {
			return nil, fmt.Errorf("stuntgres: duplicate installed provider key")
		}
		occupied[p.original] = i
		reserved[p.original], reserved[p.current] = true, true
		if p.original != p.current {
			pending++
		}
	}
	var statements []string
	alter := func(p providerReplay, key providerKey) string {
		name := catalog.QuoteIdentifier(key.schema) + "." + catalog.QuoteIdentifier(key.name)
		if p.dictionary {
			return "ALTER TEXT SEARCH DICTIONARY " + name
		}
		return "ALTER FUNCTION " + name + "(" + p.arguments + ")"
	}
	serial := 0
	for pending > 0 {
		advanced := false
		for i := range providers {
			p := &providers[i]
			if p.original == p.current {
				continue
			}
			next := p.original
			action := ""
			// Rename before relocation when that key is free; otherwise try moving
			// first. This avoids inventing a temporary for a vacated-key chain.
			if p.original.name != p.current.name {
				next.name = p.current.name
				action = " RENAME TO " + catalog.QuoteIdentifier(next.name)
			} else {
				next.schema = p.current.schema
				action = " SET SCHEMA " + catalog.QuoteIdentifier(next.schema)
			}
			if _, busy := occupied[next]; busy {
				if p.original.schema == p.current.schema {
					continue
				}
				next = p.original
				next.schema = p.current.schema
				action = " SET SCHEMA " + catalog.QuoteIdentifier(next.schema)
				if _, busy := occupied[next]; busy {
					continue
				}
			}
			statements = append(statements, alter(*p, p.original)+action+";\n")
			delete(occupied, p.original)
			occupied[next] = i
			p.original = next
			if next == p.current {
				pending--
			}
			advanced = true
		}
		if advanced {
			continue
		}
		for i := range providers {
			p := &providers[i]
			if p.original == p.current {
				continue
			}
			next := p.original
			for {
				serial++
				kind := "routine"
				if p.dictionary {
					kind = "dictionary"
				}
				next.name = fmt.Sprintf("__stuntgres_%s_%d", kind, serial)
				other := next
				other.schema = p.current.schema
				if !reserved[next] && !reserved[other] {
					if _, busy := occupied[next]; !busy {
						break
					}
				}
			}
			reserved[next] = true
			statements = append(statements, alter(*p, p.original)+" RENAME TO "+catalog.QuoteIdentifier(next.name)+";\n")
			delete(occupied, p.original)
			occupied[next] = i
			p.original = next
			break
		}
	}
	// Refresh options only after every provider has its final identity.
	for _, p := range providers {
		if p.options != "" {
			statements = append(statements, alter(p, p.current)+p.options+";\n")
		}
		if p.parallel != 0 {
			parallel := ""
			switch p.parallel {
			case 's':
				parallel = "SAFE"
			case 'r':
				parallel = "RESTRICTED"
			case 'u':
				parallel = "UNSAFE"
			default:
				return nil, fmt.Errorf("stuntgres: invalid provider parallel metadata")
			}
			statements = append(statements, alter(p, p.current)+" PARALLEL "+parallel+";\n")
		}
		if p.cost != 0 {
			statements = append(statements, alter(p, p.current)+" COST "+strconv.FormatFloat(float64(p.cost), 'g', -1, 32)+";\n")
		}
		if p.rows > 0 {
			statements = append(statements, alter(p, p.current)+" ROWS "+strconv.FormatFloat(float64(p.rows), 'g', -1, 32)+";\n")
		}
	}
	// Transfer authority last: relabeling must not depend on recipient privileges.
	for _, p := range providers {
		if p.ownerChanged {
			statements = append(statements, alter(p, p.current)+" OWNER TO "+catalog.QuoteIdentifier(p.owner)+";\n")
		}
	}
	return statements, nil
}

func (s *dumpScript) prepareProviderReplay() error {
	var early []providerReplay
	byEvent := make(map[ids.OID][]providerReplay)
	for _, p := range s.Providers {
		if p.readyAfter == 0 {
			early = append(early, p)
		} else {
			byEvent[p.readyAfter] = append(byEvent[p.readyAfter], p)
		}
	}
	s.providerEventStatements = make(map[ids.OID][]string, len(byEvent))
	for oid, providers := range byEvent {
		if _, exists := s.declarations[oid]; !exists {
			return fmt.Errorf("stuntgres: missing generated provider declaration event")
		}
		statements, err := planProviderReplay(providers)
		if err != nil {
			return err
		}
		s.providerEventStatements[oid] = statements
	}
	statements, err := planProviderReplay(early)
	if err != nil {
		return err
	}
	dictionaries, err := s.dictionaryProviderReplays()
	if err != nil {
		return err
	}
	dictionaryStatements, err := planProviderReplay(dictionaries)
	if err != nil {
		return err
	}
	statements = append(statements, dictionaryStatements...)
	if len(statements) == 0 {
		return nil
	}
	s.providerStatements = statements
	// Metadata must describe the reconstructed live slot, after real ALTER SQL.
	s.extensionMetadata = make(map[ids.OID]string)
	for address, d := range s.CatalogView.State().Descriptors() {
		ext, ok := d.(*catalog.Extension)
		if !ok {
			continue
		}
		metadata, err := extension.OwnerMetadata(s.CatalogView, s.DomainRoles, ext)
		if err != nil {
			return err
		}
		s.extensionMetadata[address.ObjectID] = metadata
		index := s.ViewIndices[address.ObjectID]
		sql := s.Views[index].SQL
		// Owner metadata is appended to the CREATE by the collector.
		s.Views[index].SQL = strings.TrimSuffix(sql, metadata)
	}
	return nil
}

func emitDumpProviderReplay(ctx context.Context, s *dumpScript, w *dumpWriter) error {
	if len(s.providerStatements) == 0 {
		return nil
	}
	// Keep extension prerequisite order, before any provider consumer.
	for _, event := range s.eventOrder {
		if event.Phase != dump.Declaration {
			continue
		}
		if _, ok := s.extensionMetadata[event.OID]; !ok {
			continue
		}
		declaration := s.Views[s.ViewIndices[event.OID]]
		if err := w.setOwner(declaration.Owner); err != nil {
			return err
		}
		if err := w.emit(declaration.SQL); err != nil {
			return err
		}
	}
	if err := w.setOwner("postgres"); err != nil {
		return err
	}
	// This phase owns the extension declaration events, so it also owns their
	// scalar-identity barrier. Routine arguments already use final type names.
	if s.extensionTypeSQL != "" {
		if err := w.emit(s.extensionTypeSQL); err != nil {
			return err
		}
	}
	if err := emitDumpLines(w, s.providerStatements); err != nil {
		return err
	}
	for _, event := range s.eventOrder {
		if event.Phase != dump.Declaration {
			continue
		}
		if metadata, ok := s.extensionMetadata[event.OID]; ok {
			if err := w.emit(metadata); err != nil {
				return err
			}
		}
	}
	return ctx.Err()
}

// emitDumpProviderEvent completes intrinsic metadata at its actual constructor
// installation boundary, before dependent declarations or type ACL repairs.
func emitDumpProviderEvent(s *dumpScript, w *dumpWriter, oid ids.OID) error {
	statements := s.providerEventStatements[oid]
	if len(statements) == 0 {
		return nil
	}
	if err := w.setOwner("postgres"); err != nil {
		return err
	}
	return emitDumpLines(w, statements)
}
