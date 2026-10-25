package control

import (
	"fmt"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

// CheckPortable rejects catalog state not representable by the supported SQL
// stream before any writer output.
func CheckPortable(state *catalog.State) error {
	if catalog.NewRead(nil, state).HasMultiplePersistentDefaults() {
		return fmt.Errorf("stuntgres: multiple default operator classes cannot be dumped")
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
