package control

import (
	"context"
	"slices"
	"strings"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
)

// Identity follows the event stream so constraint-backed indexes have their
// final names. An orphaned i mode has no ordinary SQL replay representation.
func emitDumpReplicaIdentity(ctx context.Context, s *dumpScript, w *dumpWriter) error {
	var relations []*catalog.Relation
	selected := make(map[ids.ObjectAddress]string)
	for _, descriptor := range s.OriginalCatalog.Descriptors() {
		r, ok := descriptor.(*catalog.Relation)
		if !ok {
			continue
		}
		if r.SQLIndex != nil && r.SQLIndex.ReplicaIdentity {
			selected[r.SQLIndex.Parent] = r.Name()
		}
		if r.ReplicaIdentity == 0 || r.ReplicaIdentity == parser.ReplicaIdentityDefault {
			continue
		}
		ns, ok := s.OriginalCatalog.Namespace(r.Namespace())
		if ok && ns.Temporary == (ids.SessionToken{}) {
			relations = append(relations, r)
		}
	}
	slices.SortFunc(relations, func(a, b *catalog.Relation) int {
		if order := strings.Compare(s.OriginalCatalog.Get(a.Namespace()).Name(), s.OriginalCatalog.Get(b.Namespace()).Name()); order != 0 {
			return order
		}
		return strings.Compare(a.Name(), b.Name())
	})
	for _, r := range relations {
		if err := ctx.Err(); err != nil {
			return err
		}
		option := ""
		switch r.ReplicaIdentity {
		case parser.ReplicaIdentityFull:
			option = "FULL"
		case parser.ReplicaIdentityNothing:
			option = "NOTHING"
		case parser.ReplicaIdentityIndex:
			name, ok := selected[r.Address()]
			if !ok {
				continue
			}
			option = "USING INDEX " + catalog.QuoteIdentifier(name)
		}
		owner, err := s.DomainRoles.Descriptor(r.Owner)
		if err != nil {
			return err
		}
		if err := w.setOwner(owner.Name); err != nil {
			return err
		}
		qualified := catalog.QuoteIdentifier(s.OriginalCatalog.Get(r.Namespace()).Name()) + "." + catalog.QuoteIdentifier(r.Name())
		if err := w.emit("ALTER TABLE " + qualified + " REPLICA IDENTITY " + option + ";\n"); err != nil {
			return err
		}
	}
	return nil
}
