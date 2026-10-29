package reference_test

import "testing"

func TestReferenceFTSJSONFilters(t *testing.T) {
	doc := `{"a":"aaa in bbb","b":123,"c":456,"d":true,"f":false,"g":null}`
	forms := []ftsJSONForm{{"json_to_tsvector", "json", true, true}, {"json_to_tsvector", "json", false, true}}
	ftsJSONCases(t, forms, []ftsJSONCase{
		{"english", doc, `"string"`, "'aaa':1 'bbb':3"},
		{"english", doc, `"numeric"`, "'123':1 '456':3"},
		{"english", doc, `"boolean"`, "'fals':3 'true':1"},
		{"english", doc, `"key"`, "'b':2 'c':4 'd':6 'f':8 'g':10"},
		{"english", doc, `["string","numeric"]`, "'123':5 '456':7 'aaa':1 'bbb':3"},
		{"english", doc, `["STRING","numeric","string","NUMERIC"]`, "'123':5 '456':7 'aaa':1 'bbb':3"},
		{"english", doc, `[]`, ""},
		{"english", `{"cat":"The Fat Rats","dog":123}`, `"all"`, "'123':9 'cat':1 'dog':7 'fat':4 'rat':5"},
	})
	// Arrays and single-key objects specify traversal without promising JSONB
	// multi-key field order. Both types still exercise every filter category.
	for _, typ := range []string{"json", "jsonb"} {
		forms := []ftsJSONForm{{typ + "_to_tsvector", typ, true, true}, {typ + "_to_tsvector", typ, false, true}}
		ftsJSONCases(t, forms, []ftsJSONCase{
			{"english", `["aaa in bbb",123,true,false,null]`, `"string"`, "'aaa':1 'bbb':3"},
			{"english", `["aaa in bbb",123,true,false,null]`, `"numeric"`, "'123':1"},
			{"english", `["aaa in bbb",123,true,false,null]`, `"boolean"`, "'fals':3 'true':1"},
			{"english", `{"cat":"dog"}`, `"key"`, "'cat':1"},
			{"english", `{"cat":"dog"}`, `["KEY","key"]`, "'cat':1"},
			{"english", `{"cat":"dog"}`, `"ALL"`, "'cat':1 'dog':3"},
			{"english", `{"cat":"dog"}`, `["all","string","all","key"]`, "'cat':1 'dog':3"},
			{"english", `{"outer":["red",{"inner":"blue"}]}`, `"all"`, "'blue':7 'inner':5 'outer':1 'red':3"},
			{"english", `["cat",123,true]`, `["string","numeric","boolean"]`, "'123':3 'cat':1 'true':5"},
			{"english", `{"cat":"dog"}`, `[]`, ""},
		})
	}
}
