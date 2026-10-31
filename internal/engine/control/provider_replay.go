package control

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/dump"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/regobj"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/engine/settings"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// providerKey is a schema/name collision key with the routine input signature.
// Dictionary keys use an empty signature and a separate planner invocation.
type providerKey struct {
	schema, name string
	signature    catalog.ProcedureSignature
}
type providerFamily uint8

const (
	routineProvider providerFamily = iota
	dictionaryProvider
	templateProvider
	configurationProvider
)

type providerReplay struct {
	original, current providerKey
	arguments, owner  string
	ownerChanged      bool
	family            providerFamily
	objectKind        string // Empty is the ordinary FUNCTION planner.
	options           string // Rendered dictionary ALTER clause, after identity replay.
	parallel          byte
	volatility        byte // Explicit genuine HostConversion promise; zero keeps bootstrap VOLATILE.
	cost, rows        float32
	securityDefiner   bool
	configuration     *catalog.RoutineConfiguration
	readyAfter        ids.OID // Zero for seeded/installed providers; otherwise the creating type event.
	hostConversion    bool    // Created by genuine type installation, before aliases.
}

func (c *dumpCollection) collectProvider(p *catalog.Procedure) error {
	ns, ok := c.CatalogView.Namespace(p.Namespace())
	if !ok {
		return fmt.Errorf("stuntgres: missing provider namespace")
	}
	r := providerReplay{current: providerKey{ns.Name(), p.Name(), p.Signature}}
	originalOwner := c.source.Bootstrap
	if catalog.DescriptorOrigin(p) != nil {
		// Released objects belong to the sequential installation replay owner.
		return nil
	}
	r.parallel, r.rows = p.Parallel, p.Rows
	if p.Cost != 0 && (p.Builtin == nil || p.Cost != routine.BuiltinRoutineCost(p.Builtin)) {
		r.cost = p.Cost
	}
	if p.RangeConstructor != nil {
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
	} else if p.SnowballHelper != catalog.NoSnowballHelper {
		name, signature := p.SnowballHelper.Declaration()
		originalNS := c.CatalogView.MustNamespace(ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PGCatalogNamespaceOID})
		r.original = providerKey{originalNS.Name(), name, signature}
	} else if p.HostHandler != nil {
		name := p.HostHandler.HandlerName()
		r.original = providerKey{name.Schema, name.Name, p.Signature}
	} else if p.HostConversion != nil {
		conversion := p.HostConversion
		definition := conversion.Token.Definition()
		name := definition.ToName
		if conversion.From {
			name = definition.FromName
		}
		r.original = providerKey{name.Schema, name.Name, p.Signature}
		r.hostConversion = true
		switch p.Volatility {
		case expr.Stable:
			r.volatility = 's'
		case expr.Immutable:
			r.volatility = 'i'
		}
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
	if p.RangeConstructor != nil {
		// The type epilogue owns constructor identity and current owner replay.
		// Only execution estimates remain at the following declaration boundary.
		r.ownerChanged = false
	}
	if r.ownerChanged {
		owner, err := c.DomainRoles.Descriptor(p.Owner)
		if err != nil {
			return err
		}
		r.owner = owner.Name
	}
	if (r.original != r.current || r.ownerChanged || r.parallel != 0 || r.volatility != 0 || r.cost != 0 || r.rows > 0) && p.Signature != "" {
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
		if p.original != p.current || p.ownerChanged || p.options != "" || p.parallel != 0 || p.volatility != 0 || p.cost != 0 || p.rows > 0 || p.securityDefiner || p.configuration != nil {
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
		switch p.family {
		case dictionaryProvider:
			return "ALTER TEXT SEARCH DICTIONARY " + name
		case templateProvider:
			return "ALTER TEXT SEARCH TEMPLATE " + name
		case configurationProvider:
			return "ALTER TEXT SEARCH CONFIGURATION " + name
		}
		switch p.objectKind {
		case "OPERATOR CLASS", "OPERATOR FAMILY":
			return "ALTER " + p.objectKind + " " + name + " USING " + catalog.QuoteIdentifier(p.arguments)
		case "PROCEDURE", "AGGREGATE":
			return "ALTER " + p.objectKind + " " + name + "(" + p.arguments + ")"
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
				switch p.family {
				case dictionaryProvider:
					kind = "dictionary"
				case templateProvider:
					kind = "template"
				case configurationProvider:
					kind = "configuration"
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
		if p.volatility != 0 {
			promise := "STABLE"
			if p.volatility == 'i' {
				promise = "IMMUTABLE"
			}
			statements = append(statements, alter(p, p.current)+" "+promise+";\n")
		}
		if p.securityDefiner {
			statements = append(statements, alter(p, p.current)+" SECURITY DEFINER;\n")
		}
		for i := range p.configuration.Len() {
			clause, err := providerConfigurationSQL(p.configuration.At(i))
			if err != nil {
				return nil, err
			}
			statements = append(statements, alter(p, p.current)+clause+";\n")
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

func providerConfigurationSQL(entry string) (string, error) {
	name, value, ok := strings.Cut(entry, "=")
	if !ok {
		return "", fmt.Errorf("stuntgres: invalid provider configuration")
	}
	clause := " SET " + catalog.QuoteIdentifier(name) + " TO "
	if !parser.SettingQuotesIdentifiers(name) {
		return clause + dump.CommentLiteral(value), nil
	}
	identifiers, err := settings.ParseIdentifierList(name, value)
	if err != nil {
		return "", err
	}
	if len(identifiers) == 0 {
		return clause + dump.CommentLiteral(""), nil
	}
	for i, identifier := range identifiers {
		if i != 0 {
			clause += ", "
		}
		clause += dump.CommentLiteral(identifier)
	}
	return clause, nil
}

func (s *dumpScript) prepareProviderReplay(ctx context.Context) error {
	var early []providerReplay
	var hostProviders []providerReplay
	byEvent := make(map[ids.OID][]providerReplay)
	for _, p := range s.Providers {
		if p.hostConversion {
			hostProviders = append(hostProviders, p)
			continue
		}
		if p.readyAfter == 0 {
			early = append(early, p)
		} else {
			byEvent[p.readyAfter] = append(byEvent[p.readyAfter], p)
		}
	}
	s.providerEventStatements = make(map[ids.OID][]string, len(byEvent))
	for oid, providers := range byEvent {
		if _, exists := s.Graph[dump.Event{OID: oid}]; !exists {
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
	configurations, err := s.configurationProviderReplays()
	if err != nil {
		return err
	}
	configurationStatements, err := planProviderReplay(configurations)
	if err != nil {
		return err
	}
	statements = append(statements, configurationStatements...)
	retirements, err := s.snowballSeedRetirements(ctx, s.CatalogView.SnowballSeedDefaults())
	if err != nil {
		return err
	}
	s.providerStatements = append(retirements, statements...)
	s.hostProviderStatements, err = planProviderReplay(hostProviders)
	if err != nil {
		return err
	}
	if err := s.prepareLanguageReplay(); err != nil {
		return err
	}
	if err := s.prepareHostReplay(); err != nil {
		return err
	}
	return s.prepareInstallationReplay(ctx)
}

func emitDumpProviderReplay(ctx context.Context, s *dumpScript, w *dumpWriter) error {
	if len(s.providerStatements) == 0 && len(s.installationStatements) == 0 && len(s.languageStatements) == 0 && s.hostAbsenceComment == "" {
		return nil
	}
	if err := w.setOwner("postgres"); err != nil {
		return err
	}
	if err := emitDumpLines(w, s.providerStatements); err != nil {
		return err
	}
	if err := emitDumpLines(w, s.installationStatements); err != nil {
		return err
	}
	if err := emitDumpLines(w, s.hostConverterDrops); err != nil {
		return err
	}
	if err := emitDumpLines(w, s.hostProviderStatements); err != nil {
		return err
	}
	if err := emitDumpLines(w, s.languageStatements); err != nil {
		return err
	}
	if err := emitDumpLines(w, s.hostHandlerDrops); err != nil {
		return err
	}
	if s.hostAbsenceComment != "" {
		if err := w.emit(s.hostAbsenceComment); err != nil {
			return err
		}
		// This actual administrative command is the authenticated replay
		// boundary for bootstrap pair removals, before any consumer DDL.
		if err := w.forceOwner("postgres"); err != nil {
			return err
		}
	}
	if err := emitDumpLines(w, s.installationACLStatements); err != nil {
		return err
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
