package exec

import (
	"errors"
	"fmt"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

func (c *Context) evalHeapTupleSplit(n plan.InstalledPageinspectExpr) (sqltype.Value, error) {
	input, err := c.pageinspectInput(n)
	if err != nil {
		return sqltype.Value{}, err
	}
	if input.null {
		return sqltype.Null(n.Usage.Type), nil
	}
	values := &input.values
	var oid uint32
	if !values[0].IsNull() {
		oid, err = values[0].OID()
		if err != nil {
			return sqltype.Value{}, err
		}
	}
	raw, err := values[1].Binary()
	if err != nil {
		return sqltype.Value{}, err
	}
	var masks [2]uint16
	for i := range masks {
		if !values[i+2].IsNull() {
			number, err := values[i+2].Integer()
			if err != nil {
				return sqltype.Value{}, err
			}
			masks[i] = uint16(number)
		}
	}
	var bits *string
	if !values[4].IsNull() {
		text, err := values[4].Text()
		if err != nil {
			return sqltype.Value{}, err
		}
		bits = &text
	}
	detoast := false
	if len(n.Args) == 6 && !values[5].IsNull() {
		detoast, err = values[5].Boolean()
		if err != nil {
			return sqltype.Value{}, err
		}
	}
	layout, err := sqltype.ReadHeapTupleLayout(masks[0], masks[1], bits)
	if err != nil {
		return sqltype.Value{}, err
	}
	columns, err := c.LiveHost().PageinspectHeap(n, oid)
	if err != nil {
		return sqltype.Value{}, err
	}
	return c.splitHeapLayout(n, layout, raw, detoast, columns, n.Usage.Type)
}

func (c *Context) splitHeapLayout(n plan.InstalledPageinspectExpr, layout sqltype.HeapTupleLayout, raw string, detoast bool, columns []sqltype.HeapStoredColumn, array *sqltype.Descriptor) (sqltype.Value, error) {
	value, err := layout.Split(c.Context, raw, detoast, columns, array)
	var reference *sqltype.HeapToastReference
	if errors.As(err, &reference) {
		if _, failure := c.LiveHost().PageinspectHeap(n, reference.Relation); failure != nil {
			return sqltype.Value{}, failure
		}
		// A real descriptor is not the physical chunks named by the supplied
		// pointer. Preserve the explicit content boundary, never invent bytes.
	}
	return value, err
}

func (c *Context) heapAttrsContext(n plan.InstalledPageinspectExpr, err error) error {
	view := preparation.DomainCatalog(c)
	current, ok := view.Procedure(n.Ref.Address)
	if !ok {
		return fmt.Errorf("stuntgres: missing heap attrs current declaration")
	}
	if len(n.Args) == 2 {
		origin := current.Origin
		if origin == nil || origin.Installation == nil || len(origin.Installation.Members) <= 19 {
			return fmt.Errorf("stuntgres: missing heap attrs declaration identity")
		}
		inner, ok := view.Procedure(origin.Installation.Members[19])
		if !ok {
			return fmt.Errorf("stuntgres: missing heap attrs inner declaration")
		}
		err = SQLRoutineStatementContext(err, inner.Name(), 1)
	}
	return SQLRoutineStatementContext(err, current.Name(), 1)
}

func (c *Context) openHeapAttrs(n plan.InstalledPageinspectExpr) (cursor expr.SourceCursor, err error) {
	input, err := c.pageinspectInput(n)
	defer func() {
		if err != nil && input.body {
			err = c.heapAttrsContext(n, err)
		}
	}()
	if err != nil {
		return cursor, err
	}
	d := n.Usage.Type
	if input.null {
		return expr.OwnedSource(d, nil), nil
	}
	page, err := sqltype.ReadHeapPage(input.raw)
	if err != nil {
		return cursor, err
	}
	var oid uint32
	if !input.values[1].IsNull() {
		oid, err = input.values[1].ClassOID()
		if err != nil {
			return cursor, err
		}
	}
	detoast := false
	if len(n.Args) == 3 && !input.values[2].IsNull() {
		detoast, err = input.values[2].Boolean()
		if err != nil {
			return cursor, err
		}
	}
	rows := make([]sqltype.Value, page.Len())
	var columns []sqltype.HeapStoredColumn
	captured := false
	for i := range rows {
		item, failure := page.Item(c.Context, i)
		if failure != nil {
			return cursor, failure
		}
		fields, failure := heapItemFields(d, item)
		if failure != nil {
			return cursor, failure
		}
		if item.Tail {
			var bits *string
			if item.HasBits {
				bits = &item.Bits
			}
			layout, failure := sqltype.ReadHeapTupleLayout(item.Infomask, item.Infomask2, bits)
			if failure != nil {
				return cursor, failure
			}
			if !captured {
				columns, failure = c.LiveHost().PageinspectHeap(n, oid)
				if failure != nil {
					return cursor, failure
				}
				captured = true
			}
			fields[13], failure = c.splitHeapLayout(n, layout, item.Data, detoast, columns, d.CompositeMember(13).Usage.Type)
			if failure != nil {
				return cursor, failure
			}
		}
		rows[i], err = sqltype.OwnedCompositeValue(d, fields)
		if err != nil {
			return cursor, err
		}
	}
	return expr.OwnedSource(d, rows), nil
}

// Both heap entry points construct one final field vector. The common thirteen
// fields never pass through an intermediate graph of heap_page_items records.
func heapItemFields(d *sqltype.Descriptor, item sqltype.HeapPageItem) ([]sqltype.Value, error) {
	fields := make([]sqltype.Value, 14)
	for i := range fields {
		fields[i] = sqltype.Null(d.CompositeMember(i).Usage.Type)
	}
	for i, number := range [...]int64{int64(item.LP), int64(item.Offset), int64(item.Flags), int64(item.Length)} {
		value, err := sqltype.Integer(d.CompositeMember(i).Usage.Type, number)
		if err != nil {
			return nil, err
		}
		fields[i] = value
	}
	if item.Header {
		var err error
		fields[4], err = sqltype.XIDValue(d.CompositeMember(4).Usage.Type, item.Xmin)
		if err != nil {
			return nil, err
		}
		fields[5], err = sqltype.XIDValue(d.CompositeMember(5).Usage.Type, item.Xmax)
		if err != nil {
			return nil, err
		}
		fields[6], err = sqltype.Integer(d.CompositeMember(6).Usage.Type, int64(item.Field3))
		if err != nil {
			return nil, err
		}
		fields[7], err = sqltype.TIDValue(d.CompositeMember(7).Usage.Type, item.CTIDBlock, item.CTIDOffset)
		if err != nil {
			return nil, err
		}
		for i, number := range [...]int64{int64(item.Infomask2), int64(item.Infomask), int64(item.Hoff)} {
			fields[i+8], err = sqltype.Integer(d.CompositeMember(i+8).Usage.Type, number)
			if err != nil {
				return nil, err
			}
		}
	}
	if item.Tail {
		if item.HasBits {
			fields[11] = sqltype.String(d.CompositeMember(11).Usage.Type, item.Bits)
		}
		if item.HasOID {
			var err error
			fields[12], err = sqltype.OIDValue(d.CompositeMember(12).Usage.Type, item.OID)
			if err != nil {
				return nil, err
			}
		}
	}
	return fields, nil
}

// SQL-standard bodies initialize each helper invocation before demanding any
// heap rows, even for NULL pages and NULL tuple data. A wrapper grant is not a
// grant on its helpers. Resolve only sealed member identities, never public names.
func admitHeapAttrsHelpers(auth RoutineAccess, outer *catalog.Procedure, arity int) error {
	body := outer
	if arity == 2 {
		inner, err := admitHeapAttrsMember(auth, outer, 19)
		if err != nil {
			return SQLRoutineStatementContext(err, outer.Name(), 1)
		}
		body = inner
	}
	// FunctionScan initializes its table function before its result projection.
	for _, slot := range [...]int{15, 18} {
		if _, err := admitHeapAttrsMember(auth, body, slot); err != nil {
			err = SQLRoutineStatementContext(err, body.Name(), 1)
			if arity == 2 {
				err = SQLRoutineStatementContext(err, outer.Name(), 1)
			}
			return err
		}
	}
	return nil
}

func admitHeapAttrsMember(auth RoutineAccess, body *catalog.Procedure, slot int) (*catalog.Procedure, error) {
	origin := body.Origin
	if origin == nil || origin.Installation == nil || slot >= len(origin.Installation.Members) {
		return nil, fmt.Errorf("stuntgres: missing heap attrs helper recipe")
	}
	address := origin.Installation.Members[slot]
	member, ok := auth.Catalog.Procedure(address)
	if !ok || member.Origin == nil || member.Origin.Installation != origin.Installation || member.Origin.Declaration != slot {
		return nil, ids.ErrCatalogReplaced
	}
	ref := auth.Ref(address)
	if err := extension.ValidatePageinspectIdentity(auth.Catalog, member, ref); err != nil {
		return nil, err
	}
	current, err := ValidateRoutineDescriptor(auth.Catalog, auth.Roles, member)
	if err != nil {
		return nil, err
	}
	if err := auth.Check(access.Requirement{Object: ref, Privileges: authorization.ExecutePrivilege}); err != nil {
		return nil, err
	}
	return current, nil
}
