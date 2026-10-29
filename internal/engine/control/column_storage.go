package control

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/dump"
	"stuntgres/internal/engine/index"
)

func storageMode(value string) string {
	switch value {
	case "p":
		return "PLAIN"
	case "e":
		return "EXTERNAL"
	case "m":
		return "MAIN"
	default:
		return "EXTENDED"
	}
}
func compressionMethod(value string) string {
	switch value {
	case "p":
		return "pglz"
	case "l":
		return "lz4"
	default:
		return "default"
	}
}
func emitColumnProperties(w *dumpWriter, qualified, name string, value catalog.ColumnStorage, storage, compression bool) error {
	prefix := "ALTER TABLE ONLY " + qualified + " ALTER COLUMN " + catalog.QuoteIdentifier(name)
	if storage {
		if err := w.emit(prefix + " SET STORAGE " + storageMode(value.Storage) + ";\n"); err != nil {
			return err
		}
	}
	if compression {
		return w.emit(prefix + " SET COMPRESSION " + compressionMethod(value.Compression) + ";\n")
	}
	return nil
}
func emitRelationColumnStorage(s *dumpScript, w *dumpWriter, r *catalog.Relation) error {
	if r == nil || r.SQLIndex != nil || r.RelationKind == catalog.ViewRelation {
		return nil
	}
	if s.deferredColumnStorage[r.Address()] {
		return nil
	}
	qualified := catalog.QuoteIdentifier(s.OriginalCatalog.Get(r.Namespace()).Name()) + "." + catalog.QuoteIdentifier(r.Name())
	for i, column := range r.Columns {
		value := r.ColumnStorage(i)
		// Inherited constructors can carry nondefault policies. Explicit default
		// completions prevent a parent's saved policy leaking into a child.
		inherited := r.Inheritance != nil || r.Membership != nil
		if err := emitColumnProperties(w, qualified, column.Name, value, inherited || value.Storage != column.Usage.Type.AttributeFacts().Storage, r.RelationKind != catalog.ForeignRelation && (inherited || value.Compression != "")); err != nil {
			return err
		}
		if err := emitColumnStatistics(w, qualified, column.Name, r.ColumnMeta.Statistics[r.Attributes[i]]); err != nil {
			return err
		}
	}
	return nil
}

// Complete live policies only after all constructor/retirement events. Retired
// attributes retain their separate after-consumer, before-DROP barriers.
func emitDumpDeferredColumnStorage(ctx context.Context, s *dumpScript, w *dumpWriter) error {
	var relations []*catalog.Relation
	for _, t := range s.Tables {
		if s.deferredColumnStorage[t.Descriptor.Address()] {
			relations = append(relations, t.Descriptor)
		}
	}
	identity := func(r *catalog.Relation) string {
		return catalog.QuoteIdentifier(s.OriginalCatalog.Get(r.Namespace()).Name()) + "." + catalog.QuoteIdentifier(r.Name())
	}
	slices.SortFunc(relations, func(a, b *catalog.Relation) int { return strings.Compare(identity(a), identity(b)) })
	if len(relations) == 0 {
		return nil
	}
	if err := w.emit("SET SESSION AUTHORIZATION DEFAULT;\n"); err != nil {
		return err
	}
	w.currentOwner = ""
	for _, r := range relations {
		if err := ctx.Err(); err != nil {
			return err
		}
		qualified := identity(r)
		for i, column := range r.Columns {
			if err := emitColumnProperties(w, qualified, column.Name, r.ColumnStorage(i), true, true); err != nil {
				return err
			}
			if err := emitColumnStatistics(w, qualified, column.Name, r.ColumnMeta.Statistics[r.Attributes[i]]); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *dumpScript) retiredAttributeType(r *catalog.Relation, number int32) string {
	h := s.attributeHistory
	if h.policyTypes == nil {
		h.policyTypes = make(map[string]bool)
		// One shared inherited placeholder must have one constructor type,
		// even when its participants retained different policies at retirement.
		for _, t := range s.Tables {
			for attribute, fact := range t.Descriptor.ColumnMeta.Dropped {
				if fact.Facts.Storage != "" && fact.Facts.Storage != "p" || fact.Compression != "" {
					h.policyTypes[h.names[attributeSlot{t.Descriptor.Address(), attribute}]] = true
				}
			}
		}
	}
	if h.policyTypes[h.names[attributeSlot{r.Address(), number}]] {
		return "pg_catalog.text"
	}
	return "pg_catalog.int4"
}
func (s *dumpScript) emitRetiredStorage(w *dumpWriter, r *catalog.Relation, number int32) error {
	fact := r.ColumnMeta.Dropped[number]
	if s.retiredAttributeType(r, number) == "pg_catalog.int4" {
		return nil
	}
	qualified := catalog.QuoteIdentifier(s.OriginalCatalog.Get(r.Namespace()).Name()) + "." + catalog.QuoteIdentifier(r.Name())
	return emitColumnProperties(w, qualified, s.attributeHistory.names[attributeSlot{r.Address(), number}], catalog.ColumnStorage{Storage: fact.Facts.Storage, Compression: fact.Compression}, true, true)
}

func emitDumpIndexStorage(_ context.Context, s *dumpScript, w *dumpWriter) error {
	indexes := make([]*catalog.Relation, 0)
	for _, r := range s.OriginalCatalog.Relations() {
		if r.SQLIndex != nil && !temporaryDumpObject(s.OriginalCatalog.State(), r) {
			indexes = append(indexes, r)
		}
	}
	identity := func(r *catalog.Relation) string {
		return catalog.QuoteIdentifier(s.OriginalCatalog.Get(r.Namespace()).Name()) + "." + catalog.QuoteIdentifier(r.Name())
	}
	slices.SortFunc(indexes, func(a, b *catalog.Relation) int {
		if identity(a) < identity(b) {
			return -1
		}
		if identity(a) > identity(b) {
			return 1
		}
		return 0
	})
	authority := false
	for _, r := range indexes {
		parent := s.OriginalCatalog.MustRelation(r.SQLIndex.Parent)
		// CREATE derives simple attribute labels from the current parent names.
		// A table rename does not rename attributes of existing indexes. Include
		// duplicate and expression labels conservatively instead of rebinding.
		repairNames := false
		for i, column := range r.Columns {
			attribute := int32(0)
			if i < len(r.SQLIndex.Keys) {
				attribute = r.SQLIndex.Keys[i].Attribute
			} else {
				attribute = r.SQLIndex.Include[i-len(r.SQLIndex.Keys)]
			}
			if attribute != 0 {
				slot := parent.IndexForAttribute(attribute)
				if slot < 0 {
					return fmt.Errorf("stuntgres: missing index storage parent attribute")
				}
				repairNames = repairNames || column.Name != parent.Columns[slot].Name
			}
			if attribute == 0 {
				repairNames = true
			}
		}
		if repairNames {
			if !authority {
				if err := w.emit("SET SESSION AUTHORIZATION DEFAULT;\n"); err != nil {
					return err
				}
				authority = true
				w.currentOwner = ""
			}
			// Vacate every label first, so swaps and duplicate-name suffixes
			// cannot collide with pg_attribute's unique name index on SQL replay.
			prefix := indexAttributeReplayPrefix(parent, r)
			for i := range r.Columns {
				if err := w.emit(fmt.Sprintf("UPDATE pg_catalog.pg_attribute SET attname=%s WHERE attrelid=%s::regclass AND attnum=%d;\n", dump.CommentLiteral(fmt.Sprintf("%s%d", prefix, i+1)), dump.CommentLiteral(identity(r)), r.Attributes[i])); err != nil {
					return err
				}
			}
			for i, column := range r.Columns {
				value := r.ColumnStorage(i)
				if err := w.emit(fmt.Sprintf("UPDATE pg_catalog.pg_attribute SET attstorage=%s,attcompression=%s,attname=%s WHERE attrelid=%s::regclass AND attnum=%d;\n", dump.CommentLiteral(value.Storage), dump.CommentLiteral(value.Compression), dump.CommentLiteral(column.Name), dump.CommentLiteral(identity(r)), r.Attributes[i])); err != nil {
					return err
				}
			}
			continue
		}
		for i := range r.Columns {
			value := r.ColumnStorage(i)
			if !s.deferredColumnStorage[parent.Address()] {
				predicted, err := index.ConstructorColumnStorage(s.OriginalCatalog, parent, r, i)
				if err != nil {
					return err
				}
				if value == predicted {
					continue
				}
			}
			if !authority {
				if err := w.emit("SET SESSION AUTHORIZATION DEFAULT;\n"); err != nil {
					return err
				}
				authority = true
				w.currentOwner = ""
			}
			if err := w.emit(fmt.Sprintf("UPDATE pg_catalog.pg_attribute SET attstorage=%s,attcompression=%s WHERE attrelid=%s::regclass AND attnum=%d;\n", dump.CommentLiteral(value.Storage), dump.CommentLiteral(value.Compression), dump.CommentLiteral(identity(r)), r.Attributes[i])); err != nil {
				return err
			}
		}
	}
	return nil
}

// A constructor label is a parent name or an expression label, with a possible
// numeric suffix. Excluding their prefixes also excludes regenerated suffixes.
func indexAttributeReplayPrefix(parent, index *catalog.Relation) string {
	for serial := 0; ; serial++ {
		prefix := fmt.Sprintf("__stuntgres_att_%d_", serial)
		occupied := false
		for _, relation := range []*catalog.Relation{parent, index} {
			for _, column := range relation.Columns {
				if strings.HasPrefix(column.Name, prefix) {
					occupied = true
					break
				}
			}
		}
		if !occupied {
			return prefix
		}
	}
}
