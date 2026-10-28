package functions

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func TestOptionsToTableCursor(t *testing.T) {
	var text, array *sqltype.Descriptor
	for _, typ := range sqltype.Builtins() {
		if typ.ID() == sqltype.Text { text = typ }
		if typ.ID() == 1009 { array = typ }
	}
	shape, err := sqltype.RecordShape([]sqltype.CompositeMember{{Attribute:1, Name:"option_name", Usage:text.Usage()}, {Attribute:2, Name:"option_value", Usage:text.Usage()}})
	if err != nil { t.Fatal(err) }
	ctx := context.Background()
	input := []string{"a=b=c", "bare", "empty=", "=value", "", " x = y ", "é🙂=line\n\\quote\"", "a=b=c"}
	values := make([]sqltype.Value,len(input))
	for i,s := range input { values[i] = sqltype.String(text,s) }
	value,err := sqltype.ArrayValue(array, []sqltype.ArrayDimension{{Lower:0,Length:2},{Lower:3,Length:4}}, values)
	if err != nil { t.Fatal(err) }
	cursor,err := openOptionsSource(ctx,shape,value)
	if err != nil { t.Fatal(err) }
	peer,err := openOptionsSource(ctx,shape,value)
	if err != nil { t.Fatal(err) }
	want := [][2]any{{"a","b=c"},{"bare",nil},{"empty",""},{"","value"},{"",nil},{" x "," y "},{"é🙂","line\n\\quote\""},{"a","b=c"}}
	for i, expected := range want {
		row,present,err := cursor.Next(ctx)
		if err != nil || !present || row.Type() != shape { t.Fatal(i,row,present,err) }
		var got [2]any
		for j := range got { if !row.CompositeField(j).IsNull() { got[j],err = row.CompositeField(j).Text(); if err != nil { t.Fatal(err) } } }
		if !reflect.DeepEqual(got,expected) { t.Fatal(i,got,expected) }
	}
	end,present,err := cursor.Next(ctx)
	if err != nil || present || !end.IsNull() || end.Type() != shape || cursor.Value.Type() != nil { t.Fatal("typed EOF/release",end,present,err,cursor) }
	row,present,err := peer.Next(ctx)
	if err != nil || !present { t.Fatal(row,present,err) }
	name,_ := row.CompositeField(0).Text()
	if name != "a" || peer.Position != 1 { t.Fatal("shared cursor position",peer) }
	peer.Close()
	if peer.Value.Type() != nil || !peer.Exhausted { t.Fatal("close retains payload",peer) }
	for _, value := range []sqltype.Value{sqltype.Null(array), mustOptionsArray(t,array,nil)} {
		cursor,err := openOptionsSource(ctx,shape,value)
		if err != nil { t.Fatal(err) }
		row,present,err := cursor.Next(ctx)
		if err != nil || present || !row.IsNull() || row.Type() != shape { t.Fatal(row,present,err) }
	}
	for _, values := range [][]sqltype.Value{{sqltype.Null(text)}, {sqltype.String(text,"prefix=not-returned"),sqltype.Null(text)}} {
		_,err := openOptionsSource(ctx,shape,mustOptionsArray(t,array,values))
		var diagnostic *sqlstate.Error
		if !errors.As(err,&diagnostic) || diagnostic.Code != "22004" || diagnostic.Message != "null array element not allowed in this context" || diagnostic.Position != "" { t.Fatal(err) }
	}
	cancelled,cancel := context.WithCancel(ctx)
	cancel()
	if _,err := openOptionsSource(cancelled,shape,value); !errors.Is(err,context.Canceled) { t.Fatal(err) }
	cursor,err = openOptionsSource(ctx,shape,value)
	if err != nil { t.Fatal(err) }
	if _,_,err := cursor.Next(cancelled); !errors.Is(err,context.Canceled) || cursor.Value.Type() != nil { t.Fatal(err,cursor) }
}

func mustOptionsArray(t *testing.T, array *sqltype.Descriptor, values []sqltype.Value) sqltype.Value {
	t.Helper()
	var dimensions []sqltype.ArrayDimension
	if len(values) != 0 { dimensions = []sqltype.ArrayDimension{{Lower:1,Length:int32(len(values))}} }
	value,err := sqltype.ArrayValue(array,dimensions,values)
	if err != nil { t.Fatal(err) }
	return value
}
