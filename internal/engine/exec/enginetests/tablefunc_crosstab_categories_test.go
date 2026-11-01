package enginetests_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

type categoriesUnitCursor struct {
	schema []catalog.Column
	rows [][]sqltype.Value
	at, closes int
	failure, closeFailure error
	cancel context.CancelFunc
}

func (c *categoriesUnitCursor) Schema() []catalog.Column { return c.schema }
func (c *categoriesUnitCursor) Next(ctx context.Context, dst []sqltype.Value) (bool, error) {
	if err := ctx.Err(); err != nil { return false, err }
	if c.at == len(c.rows) { return false, c.failure }
	copy(dst, c.rows[c.at])
	c.at++
	if c.cancel != nil { c.cancel() }
	return true, nil
}
func (c *categoriesUnitCursor) Close() error { c.closes++; return c.closeFailure }

func categoriesUnitTypes() map[sqltype.ID]*sqltype.Descriptor {
	types := make(map[sqltype.ID]*sqltype.Descriptor)
	for _, typ := range sqltype.Builtins() { types[typ.ID()] = typ }
	return types
}
func categoriesUnitShape(t *testing.T, types map[sqltype.ID]*sqltype.Descriptor, ids ...sqltype.ID) *sqltype.Descriptor {
	t.Helper()
	members := make([]sqltype.CompositeMember, len(ids))
	for i, id := range ids { members[i] = sqltype.CompositeMember{Name: string(rune('a'+i)), Attribute: int32(i+1), Usage: types[id].Usage()} }
	shape, err := sqltype.RecordShape(members)
	if err != nil { t.Fatal(err) }
	return shape
}
func categoriesUnitRows(types map[sqltype.ID]*sqltype.Descriptor, width int, rows ...[]any) *categoriesUnitCursor {
	text := types[sqltype.Text]
	cursor := &categoriesUnitCursor{schema: make([]catalog.Column, width)}
	for i := range cursor.schema { cursor.schema[i].Usage = text.Usage() }
	for _, row := range rows {
		values := make([]sqltype.Value, len(row))
		for i, value := range row {
			if value == nil { values[i] = sqltype.Null(text) } else { values[i] = sqltype.String(text, value.(string)) }
		}
		cursor.rows = append(cursor.rows, values)
	}
	return cursor
}
func categoriesUnitDiagnostic(t *testing.T, err error, code, message, detail string) {
	t.Helper()
	want := &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: code, Message: message, Detail: detail}
	var got *sqlstate.Error
	if !errors.As(err, &got) || !reflect.DeepEqual(got, want) { t.Fatalf("diagnostic got %#v want %#v", err, want) }
}

func TestTablefuncCrosstabCategoriesPivot(t *testing.T) {
	types := categoriesUnitTypes()
	for _, tc := range []struct {
		name string
		width int
		result []sqltype.ID
		source [][]any
		want []string
	}{
		{"extras_overwrite_and_final_null", 5, []sqltype.ID{sqltype.Int4, sqltype.Text, sqltype.Bool, sqltype.Int4, sqltype.Int4}, [][]any{{"7","first","true","ignored","bad"},{"7","later","bad-extra","a","bad"},{"7","later","bad-extra","a","10"},{"7","later","bad-extra","b","20"},{"7","later","bad-extra","b",nil}}, []string{"(7,first,t,10,)"}},
		{"null_keys_and_null_first_extra", 4, []sqltype.ID{sqltype.Text,sqltype.Int4,sqltype.Int4,sqltype.Int4}, [][]any{{nil,nil,"a","11"},{nil,"bad-later-extra","b",nil}}, []string{"(,,11,)"}},
		{"source_strings_not_destination_keys", 3, []sqltype.ID{sqltype.Int4,sqltype.Int4,sqltype.Int4}, [][]any{{"01","a","10"},{"1","a","20"}}, []string{"(1,10,)","(1,20,)"}},
		{"nonadjacent_keys", 3, []sqltype.ID{sqltype.Text,sqltype.Text,sqltype.Text}, [][]any{{"a","a","one"},{"b","a","two"},{"a","a","three"}}, []string{"(a,one,)","(b,two,)","(a,three,)"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cursor := categoriesUnitRows(types, tc.width, tc.source...)
			before := make([][]sqltype.Value,len(cursor.rows))
			for i, row := range cursor.rows { before[i] = append([]sqltype.Value(nil),row...) }
			shape := categoriesUnitShape(t,types,tc.result...)
			values, err := exec.ConsumeCrosstabHash(context.Background(),cursor,shape,map[string]int{"b":1,"a":0},sqltype.InputEnvironment{},sqltype.OutputEnvironment{},"source")
			if err != nil || len(values)!=len(tc.want) || cursor.closes!=1 || !reflect.DeepEqual(cursor.rows,before) { t.Fatal("pivot/ownership",values,err,cursor.closes) }
			for i, value := range values {
				got, err := shape.Output(context.Background(),value)
				if err!=nil || got!=tc.want[i] || value.Type()!=shape { t.Fatal("owned result",i,got,err) }
			}
			// Overwriting the producer's slots cannot alter any transferred tuple.
			for _, row := range cursor.rows { for i := range row { row[i]=sqltype.Null(types[sqltype.Text]) } }
			for i, value := range values { got,err:=shape.Output(context.Background(),value); if err!=nil || got!=tc.want[i] { t.Fatal("borrowed producer slots",got,err) } }
		})
	}
}

func TestTablefuncCrosstabCategoriesKeys(t *testing.T) {
	types:=categoriesUnitTypes()
	for _, tc:=range []struct{name,first,second string; duplicate bool}{
		{"byte61",strings.Repeat("x",60)+"a",strings.Repeat("x",60)+"b",false},
		{"byte62",strings.Repeat("x",61)+"a",strings.Repeat("x",61)+"b",false},
		{"byte63",strings.Repeat("x",62)+"a",strings.Repeat("x",62)+"b",true},
		{"partial_utf8",strings.Repeat("x",61)+"é",strings.Repeat("x",61)+"ê",true},
	}{
		t.Run(tc.name,func(t *testing.T){
			cursor:=categoriesUnitRows(types,1,[]any{tc.first},[]any{tc.second})
			keys,err:=exec.ConsumeCrosstabCategories(context.Background(),cursor,sqltype.OutputEnvironment{},"categories")
			if tc.duplicate { categoriesUnitDiagnostic(t,err,"42710","duplicate category name",""); if keys!=nil {t.Fatal("partial category map published")} } else if err!=nil || len(keys)!=2 || keys[tc.first]!=0 || keys[tc.second]!=1 {t.Fatal("ordered byte keys",keys,err)}
			if cursor.closes!=1 {t.Fatal("category cursor close",cursor.closes)}
			one:=categoriesUnitRows(types,1,[]any{tc.first})
			keys,err=exec.ConsumeCrosstabCategories(context.Background(),one,sqltype.OutputEnvironment{},"categories")
			if err!=nil {t.Fatal(err)}
			lookup:=tc.first
			if len(lookup)>=62 {lookup+="long-suffix"}
			if tc.name=="partial_utf8" {lookup=tc.second}
			shape:=categoriesUnitShape(t,types,sqltype.Text,sqltype.Text)
			values,err:=exec.ConsumeCrosstabHash(context.Background(),categoriesUnitRows(types,3,[]any{"g",lookup,"value"}),shape,keys,sqltype.InputEnvironment{},sqltype.OutputEnvironment{},"source")
			if err!=nil || len(values)!=1 {t.Fatal(values,err)}
			got,err:=shape.Output(context.Background(),values[0]);if err!=nil || got!="(g,value)" {t.Fatal("insertion/lookup byte mismatch",got,err)}
		})
	}
}

func TestTablefuncCrosstabCategoriesValidation(t *testing.T) {
	types:=categoriesUnitTypes()
	shape:=categoriesUnitShape(t,types,sqltype.Text,sqltype.Text)
	categoriesUnitDiagnostic(t,exec.ValidateCrosstabCategoriesResult(categoriesUnitShape(t,types,sqltype.Text)),"42804","invalid crosstab return type","Return row must have at least two columns.")
	for _,tc:=range []struct{name string;width int;rows [][]any;code,message,detail string}{
		{"width",2,[][]any{{"a","b"}},"22023","invalid crosstab categories query","The query must return one column."},
		{"null_first",1,[][]any{{nil},{"a"},{"a"}},"22004","crosstab category value must not be null",""},
		{"duplicate_first",1,[][]any{{"a"},{"a"},{nil}},"42710","duplicate category name",""},
	}{t.Run(tc.name,func(t *testing.T){c:=categoriesUnitRows(types,tc.width,tc.rows...);keys,err:=exec.ConsumeCrosstabCategories(context.Background(),c,sqltype.OutputEnvironment{},"categories");categoriesUnitDiagnostic(t,err,tc.code,tc.message,tc.detail);if keys!=nil||c.closes!=1 {t.Fatal("failed category publication",keys,c.closes)}})}
	for _,tc:=range []struct{name string;width int;rows [][]any;keys map[string]int;result *sqltype.Descriptor;code,message,detail string}{
		{"empty_before_width",1,[][]any{{"g"}},nil,shape,"21000","crosstab categories query must return at least one row",""},
		{"source_width",2,[][]any{{"g","v"}},map[string]int{"a":0},shape,"22023","invalid crosstab source data query","The query must return at least 3 columns: row_name, category, and value."},
		{"result_width",4,[][]any{{"g","extra","a","v"}},map[string]int{"a":0,"b":1,"c":2},categoriesUnitShape(t,types,sqltype.Text,sqltype.Text,sqltype.Text,sqltype.Text),"42804","invalid crosstab return type","Return row must have 5 columns, not 4."},
		{"final_bad_value",3,[][]any{{"g","a","bad"}},map[string]int{"a":0},categoriesUnitShape(t,types,sqltype.Text,sqltype.Int4),"22P02","invalid input syntax for type integer: \"bad\"",""},
	}{t.Run(tc.name,func(t *testing.T){c:=categoriesUnitRows(types,tc.width,tc.rows...);values,err:=exec.ConsumeCrosstabHash(context.Background(),c,tc.result,tc.keys,sqltype.InputEnvironment{},sqltype.OutputEnvironment{},"source");categoriesUnitDiagnostic(t,err,tc.code,tc.message,tc.detail);if values!=nil||c.closes!=1 {t.Fatal("failed source publication",values,c.closes)}})}
	for _,width:=range []int{1,2,4}{
		c:=categoriesUnitRows(types,width)
		values,err:=exec.ConsumeCrosstabHash(context.Background(),c,shape,nil,sqltype.InputEnvironment{},sqltype.OutputEnvironment{},"source")
		if err!=nil||len(values)!=0||c.closes!=1{t.Fatal("empty source checked conditional shape",width,values,err)}
		c=categoriesUnitRows(types,width)
		keys,err:=exec.ConsumeCrosstabCategories(context.Background(),c,sqltype.OutputEnvironment{},"categories")
		if err!=nil||len(keys)!=0||c.closes!=1{t.Fatal("empty category checked shape",width,keys,err)}
	}
}

func TestTablefuncCrosstabCategoriesRuntimeCleanup(t *testing.T) {
	types:=categoriesUnitTypes()
	shape:=categoriesUnitShape(t,types,sqltype.Text,sqltype.Text)
	primary,closing:=errors.New("late execution"),errors.New("close")
	for _,category:=range []bool{true,false}{
		for _,mode:=range []string{"success","late_error","close_error","cancel","precancel"}{
			t.Run(map[bool]string{true:"category/",false:"source/"}[category]+mode,func(t *testing.T){
				ctx,cancel:=context.WithCancel(context.Background());defer cancel()
				c:=categoriesUnitRows(types,3,[]any{"g","a","v"})
				if category {c=categoriesUnitRows(types,1,[]any{"a"})}
				var want error
				switch mode {case "late_error":c.failure,c.closeFailure,want=primary,closing,primary;case "close_error":c.closeFailure,want=closing,closing;case "cancel":c.cancel,want=cancel,context.Canceled;case "precancel":cancel();want=context.Canceled}
				var err error
				if category {var keys map[string]int;keys,err=exec.ConsumeCrosstabCategories(ctx,c,sqltype.OutputEnvironment{},"categories");if want!=nil&&keys!=nil{t.Fatal("partial keys",keys)}} else {var values []sqltype.Value;values,err=exec.ConsumeCrosstabHash(ctx,c,shape,map[string]int{"a":0},sqltype.InputEnvironment{},sqltype.OutputEnvironment{},"source");if want!=nil&&values!=nil{t.Fatal("partial tuples",values)}}
				if !errors.Is(err,want)||c.closes!=1{t.Fatal("primary error/close",err,want,c.closes)}
			})
		}
	}
}
