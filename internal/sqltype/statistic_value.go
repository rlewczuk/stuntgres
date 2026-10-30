package sqltype

import (
	"context"
	"fmt"
	"math"
	"slices"
)

// statisticArrayPayload is a catalog-result-only typed container. There need
// not be a catalog array type for its element (notably an array-valued column).
type statisticArrayPayload struct { element Usage }

func StatisticArrayValue(slot *Descriptor, element Usage, dimensions []ArrayDimension, elements []Value) (Value, error) {
	return ownedStatisticArrayValue(slot, element, slices.Clone(dimensions), slices.Clone(elements))
}
// OwnedStatisticArrayValue transfers exclusive construction buffers.
func OwnedStatisticArrayValue(slot *Descriptor, element Usage, dimensions []ArrayDimension, elements []Value) (Value,error) {
	return ownedStatisticArrayValue(slot,element,dimensions,elements)
}
func ownedStatisticArrayValue(slot *Descriptor, element Usage, dimensions []ArrayDimension, elements []Value) (Value, error) {
	if slot == nil || slot.ID() != 2277 || !slot.Usage().CatalogNullUsage() || !element.Valid() || !element.Type.Concrete() { return Value{}, fmt.Errorf("invalid statistic anyarray payload") }
	size, err := arrayShapeSize(dimensions)
	if err != nil || size != len(elements) { return Value{}, fmt.Errorf("invalid statistic array shape") }
	for _, v := range elements { if !v.ValidResult(element) { return Value{}, fmt.Errorf("foreign statistic array element") } }
	return Value{typ:slot, array:&arrayPayload{dimensions:dimensions,elements:elements},statisticArray:&statisticArrayPayload{element:element}},nil
}
func ParseStatisticArray(ctx context.Context, slot *Descriptor, element Usage, text string, env InputEnvironment) (Value,error) {
	if err:=ctx.Err();err!=nil{return Value{},err}
	if !element.Valid(){return Value{},fmt.Errorf("invalid statistic array element usage")}
	if err:=ValidateTextInput(text);err!=nil{return Value{},err}
	v,err:=readArrayPayload(ctx,element.Type,text,element.Typmod,env)
	if err!=nil{return Value{},err}
	return ownedStatisticArrayValue(slot,element,v.array.dimensions,v.array.elements)
}
// StatisticArrayUsage exposes element authority; shape and elements use the
// ordinary immutable ArrayRank/ArrayDimension/ArrayElement accessors.
func(v Value) StatisticArrayUsage()(Usage,bool){
	if v.null||v.typ==nil||v.typ.ID()!=2277||!v.typ.Usage().CatalogNullUsage()||v.statisticArray==nil{return Usage{},false}
	return v.statisticArray.element,true
}
// MapStatisticArray translates every retained typed element and its usage.
// The null witness translates usage even for an empty array.
func(v Value) MapStatisticArray(target *Descriptor, transform func(Value,Usage)(Value,Usage,error))(Value,error){
	if v.typ==nil||v.typ.ID()!=2277||!v.typ.Usage().CatalogNullUsage()||target==nil||target.ID()!=2277||!target.Usage().CatalogNullUsage(){return Value{},fmt.Errorf("invalid statistic anyarray mapping")}
	if v.null{return ResultNull(target.Usage()),nil}
	usage,ok:=v.StatisticArrayUsage();if !ok{return Value{},fmt.Errorf("invalid statistic anyarray")}
	if transform==nil{if target==v.typ{return v,nil};next:=v;next.typ=target;return next,nil}
	_,destination,err:=transform(Null(usage.Type),usage);if err!=nil{return Value{},err}
	elements:=make([]Value,v.ArrayLen())
	for i:=range elements{next,u,err:=transform(v.ArrayElement(i),usage);if err!=nil{return Value{},err};if u!=destination{return Value{},fmt.Errorf("inconsistent statistic array usage")};elements[i]=next}
	return ownedStatisticArrayValue(target,destination,slices.Clone(v.array.dimensions),elements)
}

// StatisticRowValue freezes the fixed released pg_statistic result shape.
func StatisticRowValue(d *Descriptor, fields []Value) (Value, error) {
	value, err := OwnedStatisticRowValue(d, fields)
	if err != nil {
		return Value{}, err
	}
	value.composite.fields = slices.Clone(fields)
	return value, nil
}

// OwnedStatisticRowValue transfers an exclusively owned field buffer.
func OwnedStatisticRowValue(d *Descriptor, fields []Value) (Value, error) {
	if d == nil || d.ID() != 10029 || !d.Usage().CatalogNullUsage() || len(fields) != d.CompositeLen() {
		return Value{}, fmt.Errorf("invalid statistic row")
	}
	for i, field := range fields {
		if !field.ValidResult(d.CompositeMember(i).Usage) {
			return Value{}, fmt.Errorf("invalid statistic row field %d", i+1)
		}
	}
	if !validStatisticRowFields(fields) {
		return Value{}, fmt.Errorf("invalid statistic row slots")
	}
	return Value{typ: d, composite: &compositePayload{fields: fields, shape: d}}, nil
}

// StatisticRowsValue freezes a one-dimensional array of authentic statistic rows.
func StatisticRowsValue(d *Descriptor, rows []Value) (Value, error) {
	value, err := OwnedStatisticRowsValue(d, rows)
	if err != nil {
		return Value{}, err
	}
	value.array.elements = slices.Clone(rows)
	return value, nil
}

// OwnedStatisticRowsValue transfers an exclusively owned row buffer.
func OwnedStatisticRowsValue(d *Descriptor, rows []Value) (Value, error) {
	if d == nil || d.ID() != 10028 || !d.Usage().CatalogNullUsage() {
		return Value{}, fmt.Errorf("invalid statistic row array")
	}
	for _, row := range rows {
		if row.null || !row.ValidResult(d.Element().Usage()) {
			return Value{}, fmt.Errorf("invalid statistic array row")
		}
	}
	var dims []ArrayDimension
	if len(rows) > 0 {
		dims = []ArrayDimension{{Lower: 1, Length: int32(len(rows))}}
	}
	return Value{typ: d, array: &arrayPayload{dimensions: dims, elements: rows}}, nil
}

func (v Value) validStatisticResult() bool {
	if v.typ == nil || !v.typ.Usage().CatalogNullUsage() {
		return false
	}
	if v.null {
		return true
	}
	switch v.typ.ID() {
	case 2277:
		p := v.statisticArray
		if p == nil || v.array == nil || !p.element.Valid() {
			return false
		}
		for _, element := range v.array.elements {
			if !element.ValidResult(p.element) {
				return false
			}
		}
		return true
	case 10029:
		if !v.validCompositePayload() {
			return false
		}
		for i, field := range v.composite.fields {
			if !field.ValidResult(v.typ.CompositeMember(i).Usage) {
				return false
			}
		}
		return validStatisticRowFields(v.composite.fields)
	case 10028:
		if v.array == nil || len(v.array.dimensions) > 1 {
			return false
		}
		if len(v.array.elements) == 0 {
			return len(v.array.dimensions) == 0
		}
		if len(v.array.dimensions) != 1 || v.array.dimensions[0] != (ArrayDimension{1, int32(len(v.array.elements))}) {
			return false
		}
		for _, row := range v.array.elements {
			if row.null || !row.ValidResult(v.typ.Element().Usage()) {
				return false
			}
		}
		return true
	}
	return false
}

func (v Value) retagStatistic(d *Descriptor) (Value, error) {
	if d == nil || v.typ.ID() != d.ID() || !d.Usage().CatalogNullUsage() || !v.validStatisticResult() {
		return Value{}, fmt.Errorf("incompatible statistic result generation")
	}
	if v.null {
		return ResultNull(d.Usage()), nil
	}
	if v.typ == d {
		return v, nil
	}
	switch d.ID() {
	case 2277:
		return v.MapStatisticArray(d, nil)
	case 10029:
		return v.MapComposite(d, func(field Value, target *Descriptor) (Value, error) { return field.Retag(target) })
	case 10028:
		return v.MapArray(d, func(row Value, target *Descriptor) (Value, error) { return row.Retag(target) })
	}
	return Value{}, fmt.Errorf("invalid statistic retag")
}

func (v Value) statisticArrayOutput(ctx context.Context, env OutputEnvironment) (string, error) {
	if !v.validStatisticResult() || v.statisticArray == nil {
		return "", fmt.Errorf("invalid statistic anyarray output")
	}
	return arrayPayloadOutput(ctx, v, v.statisticArray.element.Type, env)
}

// These are expression-result rows, not arbitrary pg_statistic input tuples.
func validStatisticRowFields(fields []Value) bool {
	for _, field := range fields[:21] {
		if field.null {
			return false
		}
	}
	if fields[0].oid != 0 || fields[1].integer != 0 || fields[2].boolean || fields[4].integer < 0 {
		return false
	}
	nullfrac, err := fields[3].Float()
	if err != nil || math.IsNaN(nullfrac) || nullfrac < 0 || nullfrac > 1 {
		return false
	}
	distinct, err := fields[5].Float()
	if err != nil || math.IsNaN(distinct) || math.IsInf(distinct, 0) || distinct < -1 {
		return false
	}
	seen := uint8(0)
	for i := range 5 {
		kind := fields[6+i].integer
		numbers, values := fields[21+i], fields[26+i]
		if kind == 0 {
			if !numbers.null || !values.null || fields[11+i].oid != 0 || fields[16+i].oid != 0 {
				return false
			}
			continue
		}
		if kind != 1 && kind != 2 || seen&(1<<uint(kind)) != 0 || fields[11+i].oid == 0 {
			return false
		}
		seen |= 1 << uint(kind)
		_, ok := values.StatisticArrayUsage()
		if !ok || values.ArrayRank() != 1 || values.ArrayDimension(0).Lower != 1 || values.ArrayLen() == 0 {
			return false
		}
		for _, value := range values.array.elements {
			if value.null {
				return false
			}
		}
		if kind == 2 {
			if !numbers.null {
				return false
			}
			continue
		}
		if numbers.null || numbers.ArrayRank() != 1 || numbers.ArrayLen() != values.ArrayLen() {
			return false
		}
		sum := float64(0)
		for _, number := range numbers.array.elements {
			frequency, err := number.Float()
			if err != nil || math.IsNaN(frequency) || frequency <= 0 || frequency > 1 {
				return false
			}
			sum += frequency
		}
		// Frequencies are FLOAT4; permit one rounding unit per stored frequency.
		if sum > 1+float64(numbers.ArrayLen())*1e-7 {
			return false
		}
	}
	return true
}
