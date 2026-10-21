package engine

import (
	"context"
	"strings"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/regobj"
	"stuntgres/internal/engine/sequence"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/engine/typedef"
	"stuntgres/internal/engine/visibility"
	"stuntgres/internal/sqltype"
)

// Immutable table metadata and row frontier captured by Dump under Engine.mu.
type dumpRelation struct {
	Owner, Schema, Name string
	Columns             []catalog.Column
	Attributes          []int32
	Versions            []storage.RowVersion
	Ends                *storage.DeletionRoot
	Frontier            ids.CommitSeq
	Comment             string
	ColumnComments      map[int32]string
	NotNullConstraints  []catalog.NotNullConstraint
	Defaults            map[int32]catalog.ColumnDefault
	DeferredDefinitions map[int32]bool
	Descriptor          *catalog.Relation
	Recipes             map[int32]catalog.InheritedColumn
	DeferredChecks      map[string]bool
	RecipePlaceholders  bool
	RecipeComponent     ids.ObjectAddress
	Identities          map[int32]catalog.ColumnIdentity
	IdentitySequences   map[ids.ObjectAddress]sequence.Dump
}

func (t *dumpRelation) EmitDeferredDefaults(ctx context.Context, catalogView catalog.Read, emit func(string) error) error {
	qualified := catalog.QuoteIdentifier(t.Schema) + "." + catalog.QuoteIdentifier(t.Name)
	for i, c := range t.Columns {
		definition, exists := t.Defaults[t.Attributes[i]]
		if !exists || !t.DeferredDefinitions[t.Attributes[i]] {
			continue
		}
		text, err := stored.DefaultSQL(ctx, catalogView, definition.Expression)
		if err != nil {
			return err
		}
		prefix := "ALTER TABLE "
		if t.Descriptor.Partition == nil && t.Descriptor.Membership == nil {
			prefix += "ONLY "
		}
		if err := emit(prefix + qualified + " ALTER COLUMN " + catalog.QuoteIdentifier(c.Name) + " SET DEFAULT " + text + ";\n"); err != nil {
			return err
		}
	}
	return nil
}

func (t *dumpRelation) EmitRows(ctx context.Context, catalogView catalog.Read, emit func(string) error) error {
	destination := index.PartitionHashReplayTarget(catalogView, t.Descriptor)
	qualified := catalog.QuoteIdentifier(catalogView.Get(destination.Namespace()).Name()) + "." + catalog.QuoteIdentifier(destination.Name())
	var line strings.Builder
	for _, version := range t.Versions {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !visibility.LiveVersion(version, t.Frontier, t.Ends, nil) {
			continue
		}
		row := version.Values
		line.Reset()
		var replayCells []typedef.DomainReplayCell
		if len(t.Columns) == 0 {
			if err := emit("INSERT INTO " + qualified + " DEFAULT VALUES;\n"); err != nil {
				return err
			}
			continue
		}
		line.WriteString("INSERT INTO " + qualified)
		// Hierarchy reconstruction can place inherited columns before local
		// columns added earlier. Address captured values by name, not replay slot.
		if t.Descriptor.Membership != nil || t.Descriptor.Inheritance != nil {
			line.WriteString(" (")
			for i, column := range t.Columns {
				if i != 0 {
					line.WriteString(", ")
				}
				line.WriteString(catalog.QuoteIdentifier(column.Name))
			}
			line.WriteString(")")
		}
		if len(t.Identities) != 0 {
			line.WriteString(" OVERRIDING SYSTEM VALUE")
		}
		line.WriteString(" VALUES (")
		for i, v := range row {
			if i > 0 {
				line.WriteString(", ")
			}
			if _, generated := t.Descriptor.ColumnMeta.Generations[t.Attributes[i]]; generated {
				line.WriteString("DEFAULT")
				continue
			}
			var literal string
			var err error
			destination := t.Columns[i].Usage.Type
			base := destination.BaseType().ID()
			if typedef.ContainsStoredDomainNull(v) {
				node, captureErr := captureDomainReplayNode(ctx, catalogView, t.Columns[i].Usage, v)
				if captureErr != nil {
					return captureErr
				}
				replayCells = append(replayCells, typedef.DomainReplayCell{Column: t.Columns[i].Name, Value: &node})
				literal = "NULL"
			} else if destination.IsDomain() && (base == sqltype.Int4Range || base == sqltype.Int8Range || base == sqltype.NumRange || base == sqltype.DateRange || base == sqltype.TimestampRange || base == sqltype.TimestamptzRange || base == sqltype.Int4Multirange || base == sqltype.Int8Multirange || base == sqltype.NumMultirange) {
				// The INSERT destination reconstructs the domain via ordinary
				// contextual input without unsupported range/multirange casts;
				// non-contextual stored expressions keep regobj.Literal.
				literal, err = destination.SQLLiteral(ctx, v)
			} else {
				literal, err = regobj.Literal(ctx, catalogView, plan.LiteralExpr{Info: expr.Info{Usage: t.Columns[i].Usage}, Value: v})
			}
			if err != nil {
				return err
			}
			line.WriteString(literal)
		}
		line.WriteString(");\n")
		if len(replayCells) != 0 {
			record := typedef.DomainReplayRecord{Schema: catalogView.Get(destination.Namespace()).Name(), Table: destination.Name(), Cells: replayCells}
			directive, err := typedef.EncodeDomainReplay(record)
			if err != nil {
				return err
			}
			if err := emit(directive); err != nil {
				return err
			}
		}
		if err := emit(line.String()); err != nil {
			return err
		}
	}
	return nil
}

// Stored regclass values need the actual replay name, not merely the table's
// type declaration. Reuse the ordinary scalar reference walker for arrays and
// opaque owned values, and the same captured visibility frontier as emitRows.
func (t *dumpRelation) RowReplayReferences(ctx context.Context, catalog catalog.Read, add func(ids.ObjectAddress) error) error {
	for _, version := range t.Versions {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !visibility.LiveVersion(version, t.Frontier, t.Ends, nil) {
			continue
		}
		for i, column := range t.Columns {
			if err := regobj.WalkRoutineReferences(catalog, version.Values[i], add); err != nil {
				return err
			}
			if !column.Usage.Type.ContainsRegclass() {
				continue
			}
			literal := plan.LiteralExpr{Info: expr.Info{Usage: column.Usage}, Value: version.Values[i]}
			if err := regobj.ClassReplayReferences(catalog, literal, add); err != nil {
				return err
			}
		}
	}
	return nil
}
