package exec

import (
	"errors"
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/proc"
	"stuntgres/internal/sqltype"
)

func (c *Context) evalHeapTupleSplit(n plan.InstalledPageinspectExpr) (sqltype.Value, error) {
	input, err := c.pageinspectInput(n)
	defer input.token.Leave()
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
	columns, err := c.LiveHost().Contrib().PageinspectHeap(n, oid)
	if err != nil {
		return sqltype.Value{}, err
	}
	return c.splitHeapLayout(n, layout, raw, detoast, columns, n.Usage.Type)
}

func (c *Context) splitHeapLayout(n plan.InstalledPageinspectExpr, layout sqltype.HeapTupleLayout, raw string, detoast bool, columns []sqltype.HeapStoredColumn, array *sqltype.Descriptor) (sqltype.Value, error) {
	value, err := layout.Split(c.Context, raw, detoast, columns, array)
	var reference *sqltype.HeapToastReference
	if errors.As(err, &reference) {
		if _, failure := c.LiveHost().Contrib().PageinspectHeap(n, reference.Relation); failure != nil {
			return sqltype.Value{}, failure
		}
		// A real descriptor is not the physical chunks named by the supplied
		// pointer. Preserve the explicit content boundary, never invent bytes.
	}
	return value, err
}

func (c *Context) openHeapAttrs(n plan.InstalledPageinspectExpr) (cursor expr.SourceCursor, err error) {
	input, err := c.pageinspectArguments(n)
	if err != nil {
		return cursor, err
	}
	auth, dependencies, err := RoutineAdmissionAuthority(c)
	if err != nil {
		return cursor, err
	}
	if err = CheckRecipeRoutineDescriptor(c, auth, dependencies, n.Procedure); err != nil {
		return cursor, err
	}
	if n.Procedure.Strict && input.null {
		return expr.OwnedSource(n.Usage.Type, nil), nil
	}
	outer, err := c.enterPageinspect(n, input.null)
	if err != nil {
		return cursor, err
	}
	defer outer.Leave()
	defer func() {
		if err != nil {
			err = nativeDeclarationStatementContext(c, n.Procedure, err)
		}
	}()
	body, err := c.heapAttrsBody(n, input.null)
	if err != nil {
		return cursor, err
	}
	defer body.token.Leave()
	if body.inner != nil {
		defer func() {
			if err != nil {
				err = nativeDeclarationStatementContext(&body.Context, body.inner, err)
			}
		}()
	}
	d := n.Usage.Type
	// Only the C heap child is STRICT. Neither SQL wrapper requires raw-page
	// superuser authority when that child is skipped for a NULL page.
	if input.values[0].IsNull() {
		return expr.OwnedSource(d, nil), nil
	}
	host := body.LiveHost()
	if host == nil {
		return cursor, fmt.Errorf("stuntgres: missing pageinspect statement runtime")
	}
	heapCall, err := body.call(n, body.heap, body.heapInfo, "heap_page_items")
	if err != nil {
		return cursor, err
	}
	splitCall, err := body.call(n, body.split, body.splitInfo, "tuple_data_split")
	if err != nil {
		return cursor, err
	}
	first, err := body.heapInfo.EnterRoutine(&body.Context, input.values[:1])
	if err != nil {
		return cursor, err
	}
	if _, err = host.Contrib().Pageinspect(heapCall, 0); err != nil {
		first.Leave()
		return cursor, err
	}
	input.raw, err = input.values[0].Binary()
	if err != nil {
		first.Leave()
		return cursor, err
	}
	page, err := sqltype.ReadHeapPage(input.raw)
	if err != nil {
		first.Leave()
		return cursor, err
	}
	var oid uint32
	if !input.values[1].IsNull() {
		oid, err = input.values[1].ClassOID()
		if err != nil {
			first.Leave()
			return cursor, err
		}
	}
	detoast := false // genuine two-argument SQL wrapper supplies false
	if len(n.Args) == 3 && !input.values[2].IsNull() {
		detoast, err = input.values[2].Boolean()
		if err != nil {
			first.Leave()
			return cursor, err
		}
	}
	rows := make([]sqltype.Value, page.Len())
	if len(rows) == 0 {
		first.Leave() // first invocation is already the consumed terminal DONE
		return expr.OwnedSource(d, rows), nil
	}
	for i := range rows {
		heapToken := first
		if i != 0 {
			heapToken, err = body.heapInfo.EnterRoutine(&body.Context, input.values[:1])
			if err != nil {
				return cursor, err
			}
			if _, err = host.Contrib().Pageinspect(heapCall, 0); err != nil {
				heapToken.Leave()
				return cursor, err
			}
		}
		item, failure := page.Item(c.Context, i)
		var fields []sqltype.Value
		if failure == nil {
			fields, failure = heapItemFields(d, item)
		}
		heapToken.Leave()
		if failure != nil {
			return cursor, failure
		}
		splitToken, failure := body.FunctionEnterRoutine(body.split, body.splitInfo.RoutineRecipeReference, expr.RoutineBodyInputs{
			Count: 6, First: body.splitFormal(0), Second: body.splitFormal(1),
			HasNull:    !item.Tail || input.values[1].IsNull() || !item.HasBits || len(n.Args) == 3 && input.values[2].IsNull(),
			Occurrence: body.splitInfo.Occurrence, Source: body.splitInfo.Source,
		})
		if failure != nil {
			return cursor, failure
		}
		// tuple_data_split is NONSTRICT: superuser precedes raw_data NULL,
		// bitmap validation precedes relation_open, and detoast stays inside
		// this actual selected child's lexical subject.
		_, failure = host.Contrib().Pageinspect(splitCall, 0)
		if failure == nil && item.Tail {
			var bits *string
			if item.HasBits {
				bits = &item.Bits
			}
			layout, decodeErr := sqltype.ReadHeapTupleLayout(item.Infomask, item.Infomask2, bits)
			var columns []sqltype.HeapStoredColumn
			if decodeErr == nil {
				columns, decodeErr = host.Contrib().PageinspectHeap(splitCall, oid)
			}
			if decodeErr == nil {
				fields[13], decodeErr = body.splitHeapLayout(splitCall, layout, item.Data, detoast, columns, d.CompositeMember(13).Usage.Type)
			}
			failure = decodeErr
		}
		splitToken.Leave()
		if failure != nil {
			return cursor, failure
		}
		rows[i], err = sqltype.OwnedCompositeValue(d, fields)
		if err != nil {
			return cursor, err
		}
	}
	terminal, err := body.heapInfo.EnterRoutine(&body.Context, input.values[:1])
	if err != nil {
		return cursor, err
	}
	_, err = host.Contrib().Pageinspect(heapCall, 0)
	terminal.Leave()
	if err != nil {
		return cursor, err
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

// Select the existing immutable source call by its genuine installation member,
// then reuse the shared parsed-preorder coordinates and canonical resolver.
func heapAttrsCall(c *Context, outer *catalog.Procedure, source expr.Info, slot int) (*catalog.Procedure, expr.Info, error) {
	recipe, ok := proc.RoutineDeclarationRecipe(outer.DeclarationBody)
	if !ok {
		return nil, expr.Info{}, fmt.Errorf("stuntgres: missing heap attrs declaration")
	}
	for ordinal := range recipe.CallCount() {
		_, part := recipe.CallPart(ordinal)
		if part.Kind == proc.DeclarationExtensionMember && part.Slot == slot {
			return NativeDeclarationCall(c, outer, source, ordinal)
		}
	}
	return nil, expr.Info{}, fmt.Errorf("stuntgres: missing heap attrs source call")
}

type heapAttrsNativeBody struct {
	Context
	heap, split, inner  *catalog.Procedure
	heapInfo, splitInfo expr.Info
	token               sqltype.RoutineBodyToken
}

func (b *heapAttrsNativeBody) call(n plan.InstalledPageinspectExpr, p *catalog.Procedure, info expr.Info, member string) (plan.InstalledPageinspectExpr, error) {
	auth, _, err := RoutineInvocationAuthority(&b.Context)
	if err != nil {
		return n, err
	}
	n.Procedure, n.Info, n.Member = p, info, member
	n.Ref = auth.Catalog.Owner().Domain.Ref(auth.Catalog.Epoch(), p.Address())
	return n, nil
}

func (b *heapAttrsNativeBody) splitFormal(index int) *sqltype.Descriptor {
	id := sqltype.Oid
	if index == 1 {
		id = sqltype.Bytea
	}
	typ, _ := b.Catalog.LookupType(id)
	return typ
}

func (c *Context) heapAttrsBody(n plan.InstalledPageinspectExpr, hasNull bool) (body heapAttrsNativeBody, err error) {
	body.Context, err = BeginNativeBody(c, n.Procedure, n.Info)
	if err != nil {
		return body, err
	}
	outer, source := n.Procedure, n.Info
	if len(n.Args) == 2 {
		inner, info, failure := heapAttrsCall(&body.Context, outer, source, 19)
		if failure != nil {
			return body, failure
		}
		body.token, failure = body.FunctionEnterRoutine(inner, info.RoutineRecipeReference, expr.RoutineBodyInputs{
			Count: 3, First: n.Args[0].ResultUsage().Type, Second: n.Args[1].ResultUsage().Type,
			HasNull: hasNull, Occurrence: info.Occurrence, Source: info.Source,
		})
		if failure != nil {
			return body, failure
		}
		body.Context, err = BeginNativeBody(&body.Context, inner, info)
		if err != nil {
			body.token.Leave()
			return body, nativeDeclarationStatementContext(c, inner, err)
		}
		body.inner = inner
		outer, source = inner, info
	}
	body.split, body.splitInfo, err = heapAttrsCall(&body.Context, outer, source, 18)
	if err == nil {
		body.heap, body.heapInfo, err = heapAttrsCall(&body.Context, outer, source, 15)
	}
	if err != nil {
		body.token.Leave()
	}
	return body, err
}
