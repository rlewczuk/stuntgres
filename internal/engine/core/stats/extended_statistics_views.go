package stats

import (
	"context"
	"fmt"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/sqltype"
)

// VisitExtendedView enumerates current definitions, attaching only compatible
// session-resolved populations. It runs under the catalog capture lock. Nothing
// emitted retains authority, the journal, callbacks, or mutable table storage.
func VisitExtendedView[A CatalogAuthority](ctx context.Context, auth A, store *storage.Store, source *catalog.Relation, presentation stored.Presentation, temporary ids.SessionToken, visit func([]sqltype.Value) error) error {
	view := auth.Access()
	if !pgcatalog.RegisteredSource(view.Catalog, source) || source.Address().ObjectID != pgcatalog.StatsExtOID && source.Address().ObjectID != pgcatalog.StatsExtExprsOID {
		return fmt.Errorf("stuntgres: invalid extended statistics view")
	}
	expressions := source.Address().ObjectID == pgcatalog.StatsExtExprsOID
	// Reuse each relation's canonical/private root once in this capture, not a
	// second statistics store or a cache surviving role/generation changes.
	resolved := make(map[ids.ObjectAddress]*storage.RelationStatistics)
	for _, raw := range view.Catalog.Descriptors() {
		if err := ctx.Err(); err != nil {
			return err
		}
		definition, ok := raw.(*catalog.Statistics)
		if !ok || expressions && definition.Expressions.Len() == 0 {
			continue
		}
		relation, ok := view.Catalog.Relation(definition.Relation.Address)
		if !ok {
			return fmt.Errorf("stuntgres: statistics relation unavailable")
		}
		allowed, err := view.Roles.InheritsRole(view.State.CurrentUser, relation.Owner)
		if err != nil {
			return err
		}
		if !allowed {
			continue
		}
		active, err := access.RowSecurityApplies(view.Roles, relation, view.State.CurrentUser)
		if err != nil {
			return err
		}
		if active {
			continue
		}
		if err := (catalog.StatisticsProvider{}).Validate(view.Catalog, definition); err != nil {
			return err
		}
		facts, found := resolved[relation.Address()]
		if !found {
			metadata := session.ResolveRelationMetadata(auth.PendingCatalog(), store.RelationMetadata, relation.Address())
			facts = session.ResolveStatistics(auth.PendingCatalog(), relation.Address(), metadata, relation, view.Catalog)
			resolved[relation.Address()] = facts
		}
		ref := view.Owner.Domain.Ref(view.Catalog.Epoch(), definition.Address())
		var flavors [2]*storage.ExtendedStatistics
		if facts != nil {
			flavors[0] = facts.Extended[storage.ExtendedStatisticsKey{Definition: ref}]
			flavors[1] = facts.Extended[storage.ExtendedStatisticsKey{Definition: ref, Inherited: true}]
		}
		if !expressions && flavors[0] == nil && flavors[1] == nil {
			continue
		}
		namespace, ok := view.Catalog.Namespace(relation.Namespace())
		if !ok {
			return fmt.Errorf("stuntgres: statistics relation namespace unavailable")
		}
		statsNamespace, ok := view.Catalog.Namespace(definition.Namespace())
		if !ok {
			return fmt.Errorf("stuntgres: statistics namespace unavailable")
		}
		owner, err := view.Roles.Descriptor(definition.Owner)
		if err != nil {
			return err
		}
		identity := [5]string{namespace.Name(), relation.Name(), statsNamespace.Name(), definition.Name(), owner.Name}
		renderer := stored.Deparser{View: view.Catalog, Relation: relation, Path: presentation.Path, Output: presentation.Output, NonstandardStrings: presentation.NonstandardStrings, TemporaryOwner: temporary}
		texts := make([]string, definition.Expressions.Len())
		for i := range texts {
			texts[i], err = renderer.SQL(ctx, definition.Expressions.At(i).Definition(), stored.DeparseParent{})
			if err != nil {
				return err
			}
		}
		emitted := false
		for flavor, fact := range flavors {
			if fact == nil {
				continue
			}
			inherited := flavor == 1
			emitted = true
			if err := extendedViewRows(ctx, source, relation, definition, identity, texts, &inherited, fact, presentation.Output, visit); err != nil {
				return err
			}
		}
		// LEFT JOIN/parallel unnest preserves unbuilt expression identities,
		// including NULL inherited (not false). The joint view is INNER JOIN.
		if expressions && !emitted {
			if err := extendedViewRows(ctx, source, relation, definition, identity, texts, nil, nil, presentation.Output, visit); err != nil {
				return err
			}
		}
	}
	return ctx.Err()
}

func extendedViewRows(ctx context.Context, source, relation *catalog.Relation, definition *catalog.Statistics, identity [5]string, texts []string, inherited *bool, fact *storage.ExtendedStatistics, output sqltype.OutputEnvironment, visit func([]sqltype.Value) error) error {
	if source.Address().ObjectID == pgcatalog.StatsExtExprsOID {
		for i, text := range texts {
			if err := ctx.Err(); err != nil {
				return err
			}
			row := extendedViewIdentity(source, identity)
			row[5] = sqltype.String(source.Columns[5].Usage.Type, text)
			if fact != nil {
				row[6] = sqltype.Boolean(source.Columns[6].Usage.Type, *inherited)
				if err := columnStatsSummary(source, fact.Expression(i), row, 7); err != nil {
					return err
				}
			}
			if err := visit(row); err != nil {
				return err
			}
		}
		return ctx.Err()
	}
	row := extendedViewIdentity(source, identity)
	if definition.Selection.Len() > 0 {
		values := make([]sqltype.Value, definition.Selection.Len())
		for i := range values {
			slot := relation.IndexForAttribute(definition.Selection.Key(i))
			if slot < 0 {
				return fmt.Errorf("stuntgres: statistics attribute unavailable")
			}
			values[i] = sqltype.String(source.Columns[5].Usage.Type.Element(), relation.Columns[slot].Name)
		}
		var err error
		row[5], err = statisticsViewArray(source.Columns[5].Usage.Type, values)
		if err != nil {
			return err
		}
	}
	if len(texts) > 0 {
		values := make([]sqltype.Value, len(texts))
		for i, text := range texts {
			values[i] = sqltype.String(source.Columns[6].Usage.Type.Element(), text)
		}
		var err error
		row[6], err = statisticsViewArray(source.Columns[6].Usage.Type, values)
		if err != nil {
			return err
		}
	}
	kinds := definition.Selection.Kinds()
	values := make([]sqltype.Value, len(kinds))
	for i := range kinds {
		values[i] = sqltype.String(source.Columns[7].Usage.Type.Element(), kinds[i:i+1])
	}
	var err error
	row[7], err = statisticsViewArray(source.Columns[7].Usage.Type, values)
	if err != nil {
		return err
	}
	row[8] = sqltype.Boolean(source.Columns[8].Usage.Type, *inherited)
	if fact.Value.Type() != nil {
		row[9] = fact.Value
	}
	if fact.Dependencies.Type() != nil {
		row[10] = fact.Dependencies
	}
	if err := extendedViewMCV(ctx, source, fact, row, output); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return visit(row)
}

func extendedViewIdentity(source *catalog.Relation, identity [5]string) []sqltype.Value {
	row := make([]sqltype.Value, len(source.Columns))
	for i, column := range source.Columns {
		row[i] = sqltype.ResultNull(column.Usage)
	}
	for i, text := range identity {
		row[i] = sqltype.String(source.Columns[i].Usage.Type, text)
	}
	return row
}

func statisticsViewArray(descriptor *sqltype.Descriptor, values []sqltype.Value) (sqltype.Value, error) {
	return sqltype.OwnedArrayValue(descriptor, []sqltype.ArrayDimension{{Lower: 1, Length: int32(len(values))}}, values)
}
