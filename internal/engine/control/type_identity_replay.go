package control

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/dump"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/typedef"
)

// All canonical installations precede identity reconstruction and consumers.
// In particular earthdistance's real script must still see canonical cube.
// This is a render-only schedule over the captured graph, not persisted state.
func (s *dumpScript) orderExtensionTypes(ctx context.Context) error {
	extensions := make(dump.Graph)
	for event, predecessors := range s.Graph {
		if _, ok := s.CatalogView.Extension(ids.ObjectAddress{ClassID: ids.ExtensionClassID, ObjectID: event.OID}); ok {
			extensions[event] = predecessors
		}
	}
	if len(extensions) == 0 {
		return nil
	}
	order, err := dump.Order(ctx, extensions, nil)
	if err != nil {
		return err
	}
	for i, event := range order {
		if i > 0 {
			s.Graph[event] = append(s.Graph[event], order[i-1])
		}
	}
	last := order[len(order)-1]
	for event := range s.Graph {
		if _, ok := extensions[event]; !ok {
			s.Graph[event] = append(s.Graph[event], last)
		}
	}
	s.extensionTypeAfter = last.OID
	return nil
}

func replayTypeName(v catalog.Read, key ids.NameKey) string {
	return catalog.QuoteIdentifier(v.MustNamespace(key.Namespace).Name()) + "." + catalog.QuoteIdentifier(key.Name)
}

func (s *dumpScript) renderExtensionTypes(ctx context.Context, order []ids.OID) (*typedef.DomainReplayNames, error) {
	replay := typedef.NewDomainReplayNames()
	var changed []*catalog.Type
	var isnTypes []*catalog.Type
	for _, oid := range order {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		ext, ok := s.CatalogView.Extension(ids.ObjectAddress{ClassID: ids.ExtensionClassID, ObjectID: oid})
		if !ok {
			continue
		}
		roles, err := extension.InstalledTypes(s.CatalogView, ext)
		if err != nil { return nil, err }
		for _, role := range roles {
			name, typ := role.Name, role.Type
			if typ.SQLType.IsEan13() || typ.SQLType.IsIsbn() || typ.SQLType.IsIsbn13() || typ.SQLType.IsIsmn() || typ.SQLType.IsIsmn13() || typ.SQLType.IsIssn() || typ.SQLType.IsIssn13() { isnTypes = append(isnTypes, typ) }
			key := ids.NameKey{ClassID: ids.TypeClassID, Namespace: ext.TargetNamespace, Name: name}
			if err := replay.ReserveIdentity(typ.SQLType.ID(), key); err != nil { return nil, err }
			replay.AssociateIdentity(typ.SQLType.ID(), typ.Array)
			if typ.Name() != name || typ.Namespace() != ext.TargetNamespace { changed = append(changed, typ) }
		}
	}
	var text strings.Builder
	// Vacate all changed roles before finalizing any destination, so cycles and
	// cross-extension swaps use ordinary legal ALTERs with no shadow registry.
	for _, typ := range changed {
		key := replay.Names[typ.SQLType.ID()]
		for suffix := 0; ; suffix++ {
			candidate := "__stuntgres_type_" + s.CatalogView.MustExtension(typ.Extension).Name() + "_" + strconv.Itoa(suffix)
			target := key
			target.Name = candidate
			// Both final namespaces and current namespaces must admit the same
			// temporary name and its generated companion before a move.
			free := true
			for _, namespace := range []ids.ObjectAddress{key.Namespace, typ.Namespace()} {
				probe := target
				probe.Namespace = namespace
				array := probe
				array.Name = "_" + candidate
				if replay.Occupied(probe) || replay.Occupied(array) || s.CatalogView.NameAddress(probe) != (ids.ObjectAddress{}) || s.CatalogView.NameAddress(array) != (ids.ObjectAddress{}) {
					free = false
					break
				}
			}
			if !free {
				continue
			}
			text.WriteString("ALTER TYPE " + replayTypeName(s.CatalogView, key) + " RENAME TO " + catalog.QuoteIdentifier(candidate) + ";\n")
			if err := replay.Relocate(typ.SQLType.ID(), target, false); err != nil {
				return nil, err
			}
			break
		}
	}
	for _, typ := range changed {
		key := replay.Names[typ.SQLType.ID()]
		if key.Namespace != typ.Namespace() {
			target := key
			target.Namespace = typ.Namespace()
			text.WriteString("ALTER TYPE " + replayTypeName(s.CatalogView, key) + " SET SCHEMA " + catalog.QuoteIdentifier(s.CatalogView.MustNamespace(typ.Namespace()).Name()) + ";\n")
			if err := replay.Relocate(typ.SQLType.ID(), target, true); err != nil {
				return nil, err
			}
			key = target
		}
		target := key
		target.Name = typ.Name()
		text.WriteString("ALTER TYPE " + replayTypeName(s.CatalogView, key) + " RENAME TO " + catalog.QuoteIdentifier(typ.Name()) + ";\n")
		if err := replay.Relocate(typ.SQLType.ID(), target, false); err != nil {
			return nil, err
		}
	}
	for _, typ := range isnTypes {
		owner, err := s.DomainRoles.Descriptor(typ.Owner)
		if err != nil {
			return nil, err
		}
		// PostgreSQL ignores native owner metadata. Transfer the base through
		// ordinary SQL, which also transfers its companion, but not its I/O.
		text.WriteString("ALTER TYPE " + replayTypeName(s.CatalogView, replay.Names[typ.SQLType.ID()]) + " OWNER TO " + catalog.QuoteIdentifier(owner.Name) + ";\n")
	}
	if text.Len() != 0 && s.extensionTypeAfter == 0 {
		return nil, fmt.Errorf("stuntgres: missing extension replay barrier")
	}
	s.extensionTypeSQL = text.String()
	return replay, nil
}
