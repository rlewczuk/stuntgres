package control

import (
	"context"
	"strings"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/dump"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/partition"
)

// Temporary parents share the existing bootstrap-owned scratch phases. The real
// table declaration, owners and data stay with their normal emission authorities.
func emitFreshExclusionPreparation(ctx context.Context, s *dumpScript, w *dumpWriter, item partition.InvalidReplay, event dump.Event) error {
	if item.Fresh == nil {
		return nil
	}
	table := item.Table
	schema := catalog.QuoteIdentifier(s.CatalogView.Get(table.Namespace()).Name())
	target := schema + "." + catalog.QuoteIdentifier(table.Name())
	used := s.ConstraintReplay.Occupied[table.Namespace()]
	if used == nil {
		used = make(map[string]bool)
		s.ConstraintReplay.Occupied[table.Namespace()] = used
	}
	group := item.Fresh
	scratch := schema + "." + catalog.QuoteIdentifier(group.Scratch)
	if group.InvalidChild != "" {
		bound, err := partition.ReplayWholeBoundSQL(table)
		if err != nil {
			return err
		}
		key, err := partition.KeySQL(ctx, s.CatalogView, table)
		if err != nil {
			return err
		}
		child, gap := schema+"."+catalog.QuoteIdentifier(group.InvalidChild), schema+"."+catalog.QuoteIdentifier(group.InvalidGap)
		if err := w.emit("CREATE TABLE " + child + " PARTITION OF " + target + bound + key + ";\nCREATE TABLE " + gap + " PARTITION OF " + child + bound + ";\n"); err != nil {
			return err
		}
		used[group.InvalidChild], used[group.InvalidGap] = true, true
		// Both child constructors clone the indexes actually ready at this point.
		// Reserve their complete native sequence with the shared name owner.
		for _, name := range []string{group.InvalidChild, group.InvalidGap} {
			for _, idx := range index.PartitionIndexDeclarationOrder(s.ConstraintReplay.Original, table) {
				if used[s.ConstraintReplay.Temporary[idx.Address()]] || used[idx.Name()] {
					group.TransientIndexes = append(group.TransientIndexes, s.ConstraintReplay.ReserveCloneName(idx, name))
				}
			}
		}
	}
	if group.Open {
		var create strings.Builder
		create.WriteString("CREATE TABLE " + scratch + " (")
		for j, column := range table.Columns {
			if j != 0 {
				create.WriteString(", ")
			}
			create.WriteString(catalog.QuoteIdentifier(column.Name))
			if err := writeDumpColumnType(s.CatalogView, column, &create); err != nil {
				return err
			}
		}
		create.WriteString(")")
		key, err := partition.KeySQL(ctx, s.CatalogView, group.Parent)
		if err != nil {
			return err
		}
		create.WriteString(key + ";\n")
		if err := w.emit(create.String()); err != nil {
			return err
		}
		used[group.Scratch] = true
		attach := "ALTER TABLE " + scratch + " ATTACH PARTITION " + target + " DEFAULT;\n"
		if group.Recursive {
			if err := w.emit(attach); err != nil {
				return err
			}
		}
	}
	for j, idx := range group.Guards {
		definition, err := index.DefinitionSQL(ctx, s.CatalogView, s.CatalogView.MustRelation(idx.Address()), index.Presentation{Dump: true, Exclusion: true})
		if err != nil {
			return err
		}
		if err := w.emit("ALTER TABLE " + scratch + " ADD CONSTRAINT " + catalog.QuoteIdentifier(group.GuardSeeds[j]) + " " + definition + ";\n"); err != nil {
			return err
		}
		used[group.GuardSeeds[j]] = true
	}
	for j, idx := range group.Indexes {
		projected := s.CatalogView.MustRelation(idx.Address())
		definition, err := index.DefinitionSQL(ctx, s.CatalogView, projected, index.Presentation{Dump: true, Exclusion: true})
		if err != nil {
			return err
		}
		if group.InvalidChild != "" && group.InvalidSeeds[j] != "" {
			child := schema + "." + catalog.QuoteIdentifier(group.InvalidChild)
			if err := w.emit("ALTER TABLE ONLY " + child + " ADD CONSTRAINT " + catalog.QuoteIdentifier(group.InvalidSeeds[j]) + " " + definition + ";\n"); err != nil {
				return err
			}
			used[group.InvalidSeeds[j]] = true
		}
		if err := w.emit("ALTER TABLE " + scratch + " ADD CONSTRAINT " + catalog.QuoteIdentifier(group.Seeds[j]) + " " + definition + ";\n"); err != nil {
			return err
		}
		used[group.Seeds[j]] = true
		if group.InvalidChild != "" {
			// Keep the new root's automatic spelling until the invalid child is
			// dropped. Its capture therefore cannot be refreshed by unrelated renames.
			group.Created[j] = s.ConstraintReplay.ReserveCloneName(idx, table.Name())
			if group.InvalidSeeds[j] == "" {
				group.TransientIndexes = append(group.TransientIndexes, s.ConstraintReplay.ReserveCloneName(idx, group.InvalidChild), s.ConstraintReplay.ReserveCloneName(idx, group.InvalidGap))
			}
		}
	}
	if group.Open && !group.Recursive {
		if err := w.emit("ALTER TABLE " + scratch + " ATTACH PARTITION " + target + " DEFAULT;\n"); err != nil {
			return err
		}
	}
	if group.InvalidChild == "" {
		if err := w.emit(s.ConstraintReplay.CreationRenames(s.CatalogView, group.Indexes, event)); err != nil {
			return err
		}
	}
	return nil
}

func emitFreshExclusionCompletion(s *dumpScript, w *dumpWriter, item partition.InvalidReplay, group *partition.FreshReplay) error {
	schema := catalog.QuoteIdentifier(s.CatalogView.Get(item.Table.Namespace()).Name())
	scratch := schema + "." + catalog.QuoteIdentifier(group.Scratch)
	target := schema + "." + catalog.QuoteIdentifier(item.Table.Name())
	used := s.ConstraintReplay.Occupied[item.Table.Namespace()]
	if group.InvalidChild != "" {
		child := schema + "." + catalog.QuoteIdentifier(group.InvalidChild)
		if err := w.emit("DROP TABLE " + child + ";\n"); err != nil {
			return err
		}
		delete(used, group.InvalidChild)
		delete(used, group.InvalidGap)
		for _, name := range group.InvalidSeeds {
			delete(used, name)
		}
		for _, name := range group.TransientIndexes {
			delete(used, name)
		}
		for j, idx := range group.Indexes {
			if err := w.emit(s.ConstraintReplay.RenameCreated(s.CatalogView, idx, group.Created[j])); err != nil {
				return err
			}
		}
	}
	if !group.Close {
		return nil
	}
	if err := w.emit("ALTER TABLE " + scratch + " DETACH PARTITION " + target + ";\nDROP TABLE " + scratch + ";\n"); err != nil {
		return err
	}
	delete(used, group.Scratch)
	for _, seed := range group.CleanupSeeds {
		delete(used, seed)
	}
	return nil
}
