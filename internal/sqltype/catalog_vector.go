package sqltype

import (
	"context"
	"fmt"
	"math"
	"slices"
	"strconv"
)

// IsCatalogVector identifies the two builtin, array-compatible catalog vectors.
func (d *Descriptor) IsCatalogVector() bool {
	return d != nil && d.IsBuiltin() && d.element != nil && (d.id == Int2Vector && d.element.id == Int2 || d.id == OidVector && d.element.id == Oid)
}

// RebindCatalogVector retains builtin identity while reowning the element
// generation during catalog import. Ordinary arrays keep their own constructor.
func (d *Descriptor) RebindCatalogVector(element *Descriptor)(*Descriptor,error){
	if !d.IsCatalogVector() || !element.Concrete() || element.id!=d.element.id || !element.IsBuiltin() {
		return nil,fmt.Errorf("invalid catalog vector element authority")
	}
	if element==d.element{return d,nil}
	copy:=*d
	copy.element=element
	return &copy,nil
}

// CatalogVectorValue freezes borrowed elements. Text input and catalog producers
// transfer their exclusively owned buffer through OwnedCatalogVectorValue.
func CatalogVectorValue(d *Descriptor, elements []Value) (Value, error) {
	v, err := OwnedCatalogVectorValue(d, elements)
	if err != nil { return Value{}, err }
	v.array.elements = slices.Clone(elements)
	return v, nil
}

func OwnedCatalogVectorValue(d *Descriptor, elements []Value) (Value, error) {
	if !d.IsCatalogVector() || len(elements) > math.MaxInt32 {
		return Value{}, fmt.Errorf("invalid catalog vector descriptor or length")
	}
	return ownedArray(d,[]ArrayDimension{{Lower:0,Length:int32(len(elements))}},elements)
}

func (v Value) validCatalogVectorPayload() bool {
	return v.array!=nil && len(v.array.dimensions)==1 && v.array.dimensions[0]==(ArrayDimension{Lower:0,Length:int32(len(v.array.elements))})
}

// CatalogInt2Vector and CatalogOIDVector construct from captured numeric facts,
// without formatting and re-inputting a second textual catalog representation.
func CatalogInt2Vector(d *Descriptor, numbers []int16) (Value, error) {
	if !d.IsCatalogVector() || d.id != Int2Vector { return Value{}, fmt.Errorf("invalid int2vector descriptor") }
	elements := make([]Value,len(numbers))
	for i,n := range numbers { elements[i] = Value{typ:d.element, integer:int64(n)} }
	return OwnedCatalogVectorValue(d,elements)
}

func CatalogOIDVector[T ~uint32](d *Descriptor, numbers []T) (Value, error) {
	if !d.IsCatalogVector() || d.id != OidVector { return Value{}, fmt.Errorf("invalid oidvector descriptor") }
	elements := make([]Value,len(numbers))
	for i,n := range numbers { elements[i] = Value{typ:d.element, oid:uint32(n)} }
	return OwnedCatalogVectorValue(d,elements)
}

func vectorSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\v' || c == '\f'
}

func (d *Descriptor) catalogVectorInput(ctx context.Context, text string) (Value,error) {
	var elements []Value
	for at:=0; at<len(text); {
		if err:=ctx.Err(); err!=nil { return Value{},err }
		for at<len(text) && vectorSpace(text[at]) { at++ }
		if at==len(text) { break }
		suffix:=text[at:]
		if d.id == OidVector {
			n,end,overflow:=unsignedPrefix(suffix)
			if overflow { return Value{},vectorInputError("22003","oid",suffix) }
			if end==0 { return Value{},vectorInputError("22P02","oid",suffix) }
			low:=uint32(n)
			if n!=uint64(low) && n!=uint64(int64(int32(low))) { return Value{},vectorInputError("22003","oid",suffix) }
			elements=append(elements,Value{typ:d.element,oid:low})
			at+=end
			continue
		}
		end:=0
		negative:=false
		if suffix[end]=='+' || suffix[end]=='-' { negative=suffix[end]=='-'; end++ }
		start:=end
		var n uint64
		overflow:=false
		for end<len(suffix) && suffix[end]>='0' && suffix[end]<='9' {
			x:=uint64(suffix[end]-'0')
			if n>(math.MaxUint64-x)/10 { overflow=true } else if !overflow { n=n*10+x }
			end++
		}
		if end==start { return Value{},vectorInputError("22P02","smallint",suffix) }
		limit:=uint64(32767); if negative { limit=32768 }
		if overflow || n>limit { return Value{},vectorInputError("22003","smallint",suffix) }
		if end<len(suffix) && suffix[end]!=' ' { return Value{},vectorInputError("22P02","smallint",suffix) }
		number:=int64(n); if negative { number=-number }
		elements=append(elements,Value{typ:d.element,integer:number})
		at+=end
	}
	return OwnedCatalogVectorValue(d,elements)
}

func vectorInputError(code,name,suffix string) error {
	if code=="22003" { return failure(code,fmt.Sprintf("value \"%s\" is out of range for type %s",suffix,name)) }
	return failure(code,fmt.Sprintf("invalid input syntax for type %s: \"%s\"",name,suffix))
}

func (d *Descriptor) catalogVectorOutput(ctx context.Context,v Value)(string,error) {
	if v.array==nil { return "",fmt.Errorf("missing catalog vector payload") }
	width:=7
	if d.id==OidVector {width=11}
	buffer:=make([]byte,0,len(v.array.elements)*width)
	for i,element:=range v.array.elements {
		if err:=ctx.Err();err!=nil{return "",err}
		if i!=0 {buffer=append(buffer,' ')}
		if d.id==Int2Vector {buffer=strconv.AppendInt(buffer,element.integer,10)} else {buffer=strconv.AppendUint(buffer,uint64(element.oid),10)}
	}
	return string(buffer),nil
}

