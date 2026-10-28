package control

import (
	"context"
	"slices"
	"strings"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

// Preferences follow the event stream so implicit constraint indexes and their
// final names exist. The captured catalog, not a live engine, is the authority.
func emitDumpClusteringPreferences(ctx context.Context, s *dumpScript, w *dumpWriter) error {
	var indexes []*catalog.Relation
	for _, r := range s.OriginalCatalog.Relations() {
		if r.SQLIndex == nil || !r.SQLIndex.Clustered {
			continue
		}
		ns, ok := s.OriginalCatalog.Namespace(r.Namespace())
		if !ok || ns.Temporary != (ids.SessionToken{}) {
			continue
		}
		indexes = append(indexes, r)
	}
	slices.SortFunc(indexes, func(a, b *catalog.Relation) int {
		if n := strings.Compare(s.OriginalCatalog.Get(a.Namespace()).Name(), s.OriginalCatalog.Get(b.Namespace()).Name()); n != 0 {
			return n
		}
		pa := s.OriginalCatalog.MustRelation(a.SQLIndex.Parent)
		pb := s.OriginalCatalog.MustRelation(b.SQLIndex.Parent)
		if n := strings.Compare(pa.Name(), pb.Name()); n != 0 {
			return n
		}
		return strings.Compare(a.Name(), b.Name())
	})
	for _, r := range indexes {
		if err := ctx.Err(); err != nil {
			return err
		}
		parent := s.OriginalCatalog.MustRelation(r.SQLIndex.Parent)
		owner, err := s.DomainRoles.Descriptor(parent.Owner)
		if err != nil {
			return err
		}
		if err := w.setOwner(owner.Name); err != nil {
			return err
		}
		kind := "TABLE"
		if parent.RelationKind == catalog.MaterializedViewRelation {
			kind = "MATERIALIZED VIEW"
		}
		target := catalog.QuoteIdentifier(s.OriginalCatalog.Get(parent.Namespace()).Name()) + "." + catalog.QuoteIdentifier(parent.Name())
		if err := w.emit("ALTER " + kind + " " + target + " CLUSTER ON " + catalog.QuoteIdentifier(r.Name()) + ";\n"); err != nil {
			return err
		}
	}
	return nil
}
