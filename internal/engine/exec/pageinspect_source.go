package exec

import (
	"context"
	"fmt"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// Only caller values and checked immutable page views survive a yield. The
// evaluator, physical session and body token are borrowed for each real call.
type pageinspectSource struct {
	input       pageinspectValues
	member      string
	initialized bool
	heap        sqltype.HeapPage
	hash        sqltype.HashPage
	btree       sqltype.BtreePage
	gin         sqltype.GinPage
	brin        sqltype.BrinPage
	count       int
	newValue    bool
}

func pageinspectVPC(member string) bool {
	switch member {
	case "heap_page_items", "hash_page_items", "bt_page_items", "gin_leafpage_items", "brin_revmap_data":
		return true
	}
	return false
}
func (c *Context) openPageinspectVPC(n plan.InstalledPageinspectExpr) (expr.SourceCursor, error) {
	input, err := c.pageinspectArguments(n)
	if err != nil {
		return expr.SourceCursor{}, err
	}
	if input.null && n.Procedure.Strict {
		return expr.OwnedSource(n.Usage.Type, nil), nil
	}
	return expr.NewSourceCursor(expr.SourceCursor{Type: n.Usage.Type, State: &pageinspectSource{input: input, member: n.Member}, Advance: nextPageinspectSource}), nil
}
func (c *Context) AdvancePageinspect(n plan.InstalledPageinspectExpr, ctx context.Context, cursor *expr.SourceCursor) (sqltype.Value, bool, error) {
	if ctx.Err() != nil || !cursor.Initialized() || cursor.Exhausted || cursor.State == nil || !pageinspectVPC(n.Member) {
		return cursor.Next(ctx)
	}
	state, ok := cursor.State.(*pageinspectSource)
	if !ok || state.member != n.Member {
		cursor.Close()
		return sqltype.Value{}, false, fmt.Errorf("stuntgres: invalid pageinspect value-per-call state")
	}
	body, err := c.enterPageinspect(n, state.input.null)
	if err != nil {
		cursor.Close()
		return sqltype.Value{}, false, err
	}
	defer body.Leave()
	if !state.initialized {
		if err = c.finishPageinspectInput(n, &state.input); err != nil {
			cursor.Close()
			return sqltype.Value{}, false, err
		}
		switch n.Member {
		case "heap_page_items":
			state.heap, err = sqltype.ReadHeapPage(state.input.raw)
			state.count = state.heap.Len()
		case "hash_page_items":
			state.hash, err = sqltype.ReadHashPage(state.input.raw, sqltype.HashBucketPage)
			if err == nil {
				state.count, err = state.hash.ItemCount()
			}
		case "bt_page_items":
			state.btree, err = sqltype.ReadBtreePage(state.input.raw)
			if err == nil {
				state.newValue = state.btree.IsNew()
				state.count, err = state.btree.ItemCount()
			}
			if err == nil && state.btree.IsDeleted() {
				for _, message := range []string{"page is deleted", "page from block is deleted"} {
					c.LiveHost().Contrib().PageinspectNotice(sqlstate.Error{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: message})
				}
			}
		case "gin_leafpage_items":
			state.gin, err = sqltype.ReadGinPage(state.input.raw, sqltype.GinLeafPage)
			state.newValue = state.gin.IsNew()
			cursor.Position = 32
		case "brin_revmap_data":
			state.brin, err = sqltype.ReadBrinPage(state.input.raw, 0xf092)
			state.count = state.brin.RevmapLen()
			state.newValue = state.brin.IsNew()
		}
		if err != nil {
			cursor.Close()
			return sqltype.Value{}, false, err
		}
		state.input = pageinspectValues{}
		state.initialized = true
	}
	return cursor.Next(ctx)
}
func nextPageinspectSource(ctx context.Context, cursor *expr.SourceCursor) (sqltype.Value, bool, error) {
	state, ok := cursor.State.(*pageinspectSource)
	if !ok || !state.initialized {
		return sqltype.Value{}, false, fmt.Errorf("stuntgres: missing pageinspect value-per-call authority")
	}
	d := cursor.Type
	// PG_RETURN_NULL on a new page is ExprSingleResult, not SRF_RETURN_NEXT.
	// No later consumed terminal call exists for this source-specific path.
	if state.newValue {
		cursor.Exhausted = true
		return sqltype.Null(d), true, nil
	}
	i := cursor.Position
	if state.member != "gin_leafpage_items" && i >= state.count {
		return sqltype.Value{}, false, nil
	}
	var value sqltype.Value
	var err error
	switch state.member {
	case "heap_page_items":
		var item sqltype.HeapPageItem
		item, err = state.heap.Item(ctx, i)
		if err == nil {
			var fields []sqltype.Value
			fields, err = heapItemFields(d, item)
			if err == nil && item.Tail {
				fields[13], err = sqltype.Binary(d.CompositeMember(13).Usage.Type, item.Data)
			}
			if err == nil {
				value, err = sqltype.OwnedCompositeValue(d, fields)
			}
		}
	case "hash_page_items":
		var item sqltype.HashPageItem
		item, err = state.hash.Item(ctx, i)
		if err == nil {
			value, err = hashPageItemValue(d, item)
		}
	case "bt_page_items":
		var item sqltype.BtreePageItem
		item, err = state.btree.Item(ctx, i)
		if err == nil {
			value, err = btreePageItemValue(d, item)
		}
	case "gin_leafpage_items":
		var item sqltype.GinLeafItem
		var next int
		var present bool
		item, next, present, err = state.gin.LeafItem(ctx, i)
		if err != nil || !present {
			return sqltype.Value{}, false, err
		}
		value, err = ginPageItemValue(d, item)
		cursor.Position = next
		return value, err == nil, err
	case "brin_revmap_data":
		value, err = state.brin.RevmapItem(d, i)
	}
	cursor.Position++
	return value, err == nil, err
}
