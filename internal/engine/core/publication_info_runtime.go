package core

import (
	"context"
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/publication"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func (h *statementHost) RelationIsPublishable(oid uint32, result *sqltype.Descriptor) (sqltype.Value, error) {
	_, view, err := h.partitionInquiry()
	if err != nil {
		return sqltype.Value{}, err
	}
	boolean, ok := view.LookupType(sqltype.Bool)
	if !ok || result != boolean {
		return sqltype.Value{}, fmt.Errorf("stuntgres: foreign publication inquiry type")
	}
	r, ok := view.Relation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: ids.OID(oid)})
	if !ok {
		return sqltype.Null(result), nil
	}
	return sqltype.Boolean(result, oid >= 16384 && publication.IsPublishable(view, r)), nil
}

type publicationTableInfo struct {
	publication *catalog.Publication
	relation    *catalog.Relation
}

func (h *statementHost) PublicationTables(names sqltype.Value, result *sqltype.Descriptor) (expr.SourceCursor, error) {
	ctx, view, err := h.partitionInquiry()
	if err != nil {
		return expr.SourceCursor{}, err
	}
	array, ok := view.LookupType(1009)
	if !ok || names.Type() != array {
		return expr.SourceCursor{}, fmt.Errorf("stuntgres: foreign publication name array")
	}
	if err := publicationTableShape(view, result); err != nil {
		return expr.SourceCursor{}, err
	}
	if names.IsNull() {
		return expr.OwnedSource(result, nil), nil
	}
	// deconstruct_array with no null bitmap rejects any NULL before name lookup.
	for i := range names.ArrayLen() {
		if names.ArrayElement(i).IsNull() {
			return expr.SourceCursor{}, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22004", Message: "null array element not allowed in this context"}
		}
	}
	var tables []publicationTableInfo
	viaroot := false
	for i := range names.ArrayLen() {
		if err := ctx.Err(); err != nil {
			return expr.SourceCursor{}, err
		}
		name, err := names.ArrayElement(i).Text()
		if err != nil {
			return expr.SourceCursor{}, err
		}
		var p *catalog.Publication
		for _, candidate := range view.Publications() {
			if candidate.Name() == name {
				p = candidate
				break
			}
		}
		if p == nil {
			return expr.SourceCursor{}, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42704", Message: fmt.Sprintf("publication \"%s\" does not exist", name)}
		}
		if err := (catalog.PublicationProvider{}).Validate(view, p); err != nil {
			return expr.SourceCursor{}, err
		}
		viaroot = viaroot || p.ViaRoot
		err = publication.VisitTables(view, p, func(relation *catalog.Relation) bool {
			if ctx.Err() != nil {
				return false
			}
			tables = append(tables, publicationTableInfo{p, relation})
			return true
		})
		if err != nil {
			return expr.SourceCursor{}, err
		}
		if err := ctx.Err(); err != nil {
			return expr.SourceCursor{}, err
		}
	}
	var present map[ids.ObjectAddress]bool
	if viaroot {
		present = make(map[ids.ObjectAddress]bool, len(tables))
		for _, item := range tables {
			present[item.relation.Address()] = true
		}
	}
	values := make([]sqltype.Value, 0, len(tables))
	for _, item := range tables {
		if err := ctx.Err(); err != nil {
			return expr.SourceCursor{}, err
		}
		if viaroot {
			skip, err := publicationTableAncestor(ctx, view, item.relation, present)
			if err != nil {
				return expr.SourceCursor{}, err
			}
			if skip {
				continue
			}
		}
		value, err := publicationTableValue(ctx, view, item, result)
		if err != nil {
			return expr.SourceCursor{}, err
		}
		values = append(values, value)
	}
	if err := ctx.Err(); err != nil {
		return expr.SourceCursor{}, err
	}
	return expr.OwnedSource(result, values), nil
}

func publicationTableShape(view catalog.Read, result *sqltype.Descriptor) error {
	if result == nil || result.CompositeLen() != 4 {
		return fmt.Errorf("stuntgres: invalid publication table shape")
	}
	for i, field := range [...]struct {
		name string
		id   sqltype.ID
	}{{"pubid", sqltype.Oid}, {"relid", sqltype.Oid}, {"attrs", 22}, {"qual", sqltype.PgNodeTree}} {
		typ, ok := view.LookupType(field.id)
		member := result.CompositeMember(i)
		if !ok || member.Name != field.name || member.Usage.Type != typ {
			return fmt.Errorf("stuntgres: foreign publication table shape")
		}
	}
	return nil
}

func publicationTableAncestor(ctx context.Context, view catalog.Read, relation *catalog.Relation, present map[ids.ObjectAddress]bool) (bool, error) {
	for relation.Membership != nil {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		parent := relation.Membership.Parent
		if present[parent] {
			return true, nil
		}
		var ok bool
		relation, ok = view.Relation(parent)
		if !ok {
			return false, ids.ErrCatalogReplaced
		}
	}
	return false, nil
}

func publicationTableValue(ctx context.Context, view catalog.Read, item publicationTableInfo, result *sqltype.Descriptor) (sqltype.Value, error) {
	p, r := item.publication, item.relation
	metadata, err := publicationTableMetadata(ctx, view, p, r)
	if err != nil {
		return sqltype.Value{}, err
	}
	fields := make([]sqltype.Value, 4)
	fields[0], err = sqltype.OIDValue(result.CompositeMember(0).Usage.Type, uint32(p.Address().ObjectID))
	if err != nil {
		return sqltype.Value{}, err
	}
	fields[1], err = sqltype.OIDValue(result.CompositeMember(1).Usage.Type, uint32(r.Address().ObjectID))
	if err != nil {
		return sqltype.Value{}, err
	}
	attrs := result.CompositeMember(2).Usage.Type
	fields[2] = sqltype.Null(attrs)
	columns := make([]sqltype.Value, len(metadata.Columns))
	for i, sub := range metadata.Columns {
		columns[i], err = sqltype.Integer(attrs.Element(), int64(sub))
		if err != nil {
			return sqltype.Value{}, err
		}
	}
	if len(columns) > 0 {
		fields[2], err = sqltype.OwnedCatalogVectorValue(attrs, columns)
		if err != nil {
			return sqltype.Value{}, err
		}
	}
	fields[3], err = stored.PolicyTree(ctx, view, r, result.CompositeMember(3).Usage.Type, metadata.Filter)
	if err != nil {
		return sqltype.Value{}, err
	}
	return sqltype.OwnedCompositeValue(result, fields)
}
