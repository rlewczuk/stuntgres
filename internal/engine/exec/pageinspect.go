package exec

import (
	"fmt"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

// pageinspectValues retains already evaluated integer formals without a second
// child evaluation or a heap-backed argument frame.
type pageinspectValues struct {
	raw     string
	columns []sqltype.BrinStoredColumn
	values  [6]sqltype.Value
	body    bool
	null    bool
	token   sqltype.RoutineBodyToken
}

func (c *Context) pageinspectArguments(n plan.InstalledPageinspectExpr) (input pageinspectValues, err error) {
	if err = EvaluationContext(c); err != nil {
		return input, err
	}
	view := preparation.DomainCatalog(c)
	if n.Procedure == nil || n.CallRoutineReference() != n.Procedure || n.Ref.Address != n.Procedure.Address() || !preparation.StoredReferenceValid(view, c.RoutineConsumer, n.Ref) {
		return input, fmt.Errorf("stuntgres: invalid installed pageinspect reference")
	}
	current, err := ValidateStoredRoutineDescriptor(view, n.Procedure)
	if err != nil {
		return input, err
	}
	symbol, err := extension.ProcedureSymbol(view, current)
	if err != nil {
		return input, err
	}
	if symbol != n.Member {
		return input, fmt.Errorf("stuntgres: invalid pageinspect member")
	}
	boundary := n.Pageinspect
	boundary.RoutineReference = current
	if err = extension.ValidatePageinspectCall(view, current, boundary); err != nil {
		return input, err
	}
	values := &input.values
	for i, child := range n.Args {
		v, failure := child.Eval(c)
		if failure != nil {
			return input, failure
		}
		values[n.Slots[i]] = v
		input.null = input.null || v.IsNull()
	}
	if current.Strict && input.null {
		return input, nil
	}
	return input, nil
}

func (c *Context) pageinspectInput(n plan.InstalledPageinspectExpr) (input pageinspectValues, err error) {
	input, err = c.pageinspectArguments(n)
	if err != nil || input.null && n.Procedure.Strict {
		return input, err
	}
	input.token, err = c.enterPageinspect(n, input.null)
	if err != nil {
		return input, err
	}
	err = c.finishPageinspectInput(n, &input)
	if err != nil {
		input.token.Leave()
		input.token = sqltype.RoutineBodyToken{}
	}
	return input, err
}

func (c *Context) finishPageinspectInput(n plan.InstalledPageinspectExpr, input *pageinspectValues) (err error) {
	values := &input.values
	auth, dependencies, err := RoutineAdmissionAuthority(c)
	if err != nil {
		return err
	}
	if err = CheckRecipeRoutineDescriptor(c, auth, dependencies, n.Procedure); err != nil {
		return err
	}
	current, err := ValidateRoutineDescriptor(auth.Catalog, auth.Roles, n.Procedure)
	if err != nil {
		return err
	}
	n.Ref.Epoch = auth.Catalog.Epoch()
	n.Procedure, n.RoutineReference = current, current
	if n.Member == "tuple_data_split" {
		input.null = values[1].IsNull()
	}
	input.body = true
	var oid uint32
	if n.Member == "brin_page_items" {
		oid, err = values[1].ClassOID()
		if err != nil {
			return err
		}
	}
	host := c.LiveHost()
	if host == nil {
		return fmt.Errorf("stuntgres: missing pageinspect statement runtime")
	}
	input.columns, err = host.Contrib().Pageinspect(n, oid)
	if err != nil {
		return err
	}
	if n.Member == "heap_tuple_infomask_flags" {
		return nil
	}
	if n.Member == "tuple_data_split" {
		return nil
	}
	input.raw, err = values[0].Binary()
	return err
}
func (c *Context) enterPageinspect(n plan.InstalledPageinspectExpr, null bool) (sqltype.RoutineBodyToken, error) {
	facts := expr.RoutineBodyInputs{Count: len(n.Args), HasNull: null, Occurrence: n.Occurrence, Source: n.Source}
	if len(n.Args) > 0 {
		facts.First = n.Args[0].ResultUsage().Type
	}
	if len(n.Args) > 1 {
		facts.Second = n.Args[1].ResultUsage().Type
	}
	return c.FunctionEnterRoutine(n.RoutineReference, n.RoutineRecipeReference, facts)
}
func (c *Context) EvalPageinspect(n plan.InstalledPageinspectExpr) (sqltype.Value, error) {
	if n.Member == "tuple_data_split" {
		return c.evalHeapTupleSplit(n)
	}
	if n.Member == "hash_page_type" || n.Member == "hash_page_stats" || n.Member == "hash_metapage_info" {
		return c.evalHashPageinspect(n)
	}
	if n.Member == "heap_tuple_infomask_flags" {
		return c.evalHeapFlagsPageinspect(n)
	}
	if n.Member == "heap_page_items" || n.Member == "heap_page_item_attrs" {
		return sqltype.Value{}, fmt.Errorf("stuntgres: pageinspect set requires source invocation")
	}
	if n.Member == "page_header" || n.Member == "page_checksum" {
		return c.evalHeaderPageinspect(n)
	}
	if n.Member == "gin_metapage_info" || n.Member == "gin_page_opaque_info" {
		return c.evalGinPageinspect(n)
	}
	if n.Member == "fsm_page_contents" {
		input, err := c.pageinspectInput(n)
		defer input.token.Leave()
		if err != nil {
			return sqltype.Value{}, err
		}
		d := n.Usage.Type
		if input.null {
			return sqltype.Null(d), nil
		}
		contents, isNew, err := sqltype.ReadFSMPageContents(input.raw)
		if err != nil {
			return sqltype.Value{}, err
		}
		if isNew {
			return sqltype.Null(d), nil
		}
		return sqltype.String(d, contents), nil
	}
	input, err := c.pageinspectInput(n)
	defer input.token.Leave()
	if err != nil {
		return sqltype.Value{}, err
	}
	if input.null {
		return sqltype.Null(n.Usage.Type), nil
	}
	required := uint16(0)
	if n.Member == "brin_metapage_info" {
		required = 0xf091
	}
	page, err := sqltype.ReadBrinPage(input.raw, required)
	if err != nil {
		return sqltype.Value{}, err
	}
	d := n.Usage.Type
	if page.IsNew() {
		return sqltype.Null(d), nil
	}
	if n.Member == "brin_page_type" {
		return sqltype.String(d, page.PageType()), nil
	}
	if n.Member != "brin_metapage_info" {
		return sqltype.Value{}, fmt.Errorf("stuntgres: pageinspect set requires source invocation")
	}
	magic, version, width, last := page.Meta()
	fields := make([]sqltype.Value, 4)
	fields[0] = sqltype.String(d.CompositeMember(0).Usage.Type, magic)
	for i, v := range []int64{int64(version), int64(width), last} {
		fields[i+1], err = sqltype.Integer(d.CompositeMember(i+1).Usage.Type, v)
		if err != nil {
			return sqltype.Value{}, err
		}
	}
	return sqltype.OwnedCompositeValue(d, fields)
}
func (c *Context) OpenPageinspect(n plan.InstalledPageinspectExpr) (expr.SourceCursor, error) {
	if pageinspectVPC(n.Member) {
		return c.openPageinspectVPC(n)
	}
	if n.Member == "heap_page_item_attrs" {
		return c.openHeapAttrs(n)
	}
	if n.Member == "tuple_data_split" {
		value, err := c.evalHeapTupleSplit(n)
		if err != nil {
			return expr.SourceCursor{}, err
		}
		return expr.ScalarSource(value), nil
	}
	if n.Member == "hash_page_type" || n.Member == "hash_page_stats" || n.Member == "hash_metapage_info" {
		v, err := c.EvalPageinspect(n)
		if err != nil {
			return expr.SourceCursor{}, err
		}
		return expr.ScalarSource(v), nil
	}
	if n.Member == "heap_tuple_infomask_flags" {
		v, err := c.EvalPageinspect(n)
		if err != nil {
			return expr.SourceCursor{}, err
		}
		return expr.ScalarSource(v), nil
	}
	if n.Member == "brin_page_type" || n.Member == "brin_metapage_info" || n.Member == "fsm_page_contents" || n.Member == "gin_metapage_info" || n.Member == "gin_page_opaque_info" || n.Member == "page_header" || n.Member == "page_checksum" {
		v, err := c.EvalPageinspect(n)
		if err != nil {
			return expr.SourceCursor{}, err
		}
		return expr.ScalarSource(v), nil
	}
	input, err := c.pageinspectInput(n)
	defer input.token.Leave()
	if err != nil {
		return expr.SourceCursor{}, err
	}
	d := n.Usage.Type
	if input.null {
		return expr.OwnedSource(d, nil), nil
	}
	if n.Member != "brin_page_items" {
		return expr.SourceCursor{}, fmt.Errorf("stuntgres: scalar pageinspect requires scalar invocation")
	}
	page, err := sqltype.ReadBrinPage(input.raw, 0xf093)
	if err != nil {
		return expr.SourceCursor{}, err
	}
	items, err := page.Items(c.Context, input.columns, c.FunctionOutput())
	if err != nil {
		return expr.SourceCursor{}, err
	}
	rows := make([]sqltype.Value, len(items))
	for i, item := range items {
		fields := make([]sqltype.Value, 8)
		for j := range fields {
			fields[j] = sqltype.Null(d.CompositeMember(j).Usage.Type)
		}
		fields[0], err = sqltype.Integer(d.CompositeMember(0).Usage.Type, int64(item.Offset))
		if err != nil {
			return expr.SourceCursor{}, err
		}
		if !item.Unused {
			fields[1], err = sqltype.Integer(d.CompositeMember(1).Usage.Type, item.Block)
			if err != nil {
				return expr.SourceCursor{}, err
			}
			fields[2], err = sqltype.Integer(d.CompositeMember(2).Usage.Type, int64(item.Attribute))
			if err != nil {
				return expr.SourceCursor{}, err
			}
			for j, v := range []bool{item.AllNulls, item.HasNulls, item.Placeholder, item.Empty} {
				fields[j+3] = sqltype.Boolean(d.CompositeMember(j+3).Usage.Type, v)
			}
			if !item.AllNulls {
				fields[7] = sqltype.String(d.CompositeMember(7).Usage.Type, item.Value)
			}
		}
		rows[i], err = sqltype.OwnedCompositeValue(d, fields)
		if err != nil {
			return expr.SourceCursor{}, err
		}
	}
	return expr.OwnedSource(d, rows), nil
}

func btreePageItemValue(d *sqltype.Descriptor, item sqltype.BtreePageItem) (sqltype.Value, error) {
	fields := make([]sqltype.Value, 9)
	for j := range fields {
		fields[j] = sqltype.Null(d.CompositeMember(j).Usage.Type)
	}
	var err error
	for _, v := range []struct {
		index  int
		number int32
	}{{0, item.Offset}, {2, item.Length}} {
		fields[v.index], err = sqltype.Integer(d.CompositeMember(v.index).Usage.Type, int64(v.number))
		if err != nil {
			return sqltype.Value{}, err
		}
	}
	fields[1], err = sqltype.TIDValue(d.CompositeMember(1).Usage.Type, item.CTID.Block, item.CTID.Offset)
	if err != nil {
		return sqltype.Value{}, err
	}
	fields[3] = sqltype.Boolean(d.CompositeMember(3).Usage.Type, item.Nulls)
	fields[4] = sqltype.Boolean(d.CompositeMember(4).Usage.Type, item.Vars)
	fields[5] = sqltype.String(d.CompositeMember(5).Usage.Type, item.Data)
	if !item.DeadNull {
		fields[6] = sqltype.Boolean(d.CompositeMember(6).Usage.Type, item.Dead)
	}
	if !item.HeapNull {
		fields[7], err = sqltype.TIDValue(d.CompositeMember(7).Usage.Type, item.HeapTID.Block, item.HeapTID.Offset)
		if err != nil {
			return sqltype.Value{}, err
		}
	}
	if item.Posting != nil {
		a := d.CompositeMember(8).Usage.Type
		values := make([]sqltype.Value, len(item.Posting))
		for j, tid := range item.Posting {
			values[j], err = sqltype.TIDValue(a.Element(), tid.Block, tid.Offset)
			if err != nil {
				return sqltype.Value{}, err
			}
		}
		var dims []sqltype.ArrayDimension
		if len(values) != 0 {
			dims = []sqltype.ArrayDimension{{Lower: 1, Length: int32(len(values))}}
		}
		fields[8], err = sqltype.OwnedArrayValue(a, dims, values)
		if err != nil {
			return sqltype.Value{}, err
		}
	}
	return sqltype.OwnedCompositeValue(d, fields)
}
