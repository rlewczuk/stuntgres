package reference_test

import "testing"

func TestReferenceFTSJSONDocuments(t *testing.T) {
	for _, typ := range []string{"json", "jsonb"} {
		forms := []ftsJSONForm{{"to_tsvector", typ, true, false}, {"to_tsvector", typ, false, false}, {typ + "_to_tsvector", typ, true, true}, {typ + "_to_tsvector", typ, false, true}}
		duplicate := "'cat':1 'dog':3"
		if typ == "jsonb" {
			duplicate = "'dog':1"
		}
		ftsJSONCases(t, forms, []ftsJSONCase{
			{"english", `["cat","dog"]`, `"string"`, "'cat':1 'dog':3"},
			{"english", `{"k":"cat","k":"dog"}`, `"string"`, duplicate},
			{"english", `{"outer":["red",{"inner":"blue"}]}`, `"string"`, "'blue':3 'red':1"},
			{"english", `["C\u0061t",["dog"],{"key":"Rats"},null,true,123]`, `"string"`, "'cat':1 'dog':3 'rat':5"},
		})
	}
	ftsJSONCases(t, []ftsJSONForm{{"to_tsvector", "json", true, false}}, []ftsJSONCase{
		{"english", `{"aa":"The Fat Rats","b":"dog"}`, nil, "'dog':5 'fat':2 'rat':3"},
		{"english", `{"z":"dog","a":"cat"}`, nil, "'cat':3 'dog':1"},
	})
}

func TestReferenceFTSJSONScalars(t *testing.T) {
	ftsJSONCases(t, ftsJSONForms(), []ftsJSONCase{
		{"english", `"Cat"`, `"string"`, "'cat':1"},
		{"english", `""`, `"all"`, ""},
		{"english", `null`, `"all"`, ""},
		{"english", `[]`, `"all"`, ""},
		{"english", `{}`, `"all"`, ""},
		{"english", nil, `{}`, nil},
	})
	for _, typ := range []string{"json", "jsonb"} {
		ftsJSONCases(t, []ftsJSONForm{{"to_tsvector", typ, true, false}, {"to_tsvector", typ, false, false}}, []ftsJSONCase{
			{"english", `123`, nil, ""}, {"english", `true`, nil, ""}, {"english", `false`, nil, ""},
		})
		ftsJSONCases(t, []ftsJSONForm{{typ + "_to_tsvector", typ, true, true}, {typ + "_to_tsvector", typ, false, true}}, []ftsJSONCase{
			{"english", `123`, `"numeric"`, "'123':1"},
			{"english", `true`, `"boolean"`, "'true':1"},
			{"english", `false`, `"boolean"`, "'fals':1"},
			{"english", `123`, `[]`, ""},
			{"english", `"cat"`, nil, nil},
		})
		for _, form := range []ftsJSONForm{{"to_tsvector", typ, true, false}, {typ + "_to_tsvector", typ, true, true}} {
			ftsJSONCases(t, []ftsJSONForm{form}, []ftsJSONCase{{nil, `"cat"`, `{}`, nil}, {nil, nil, nil, nil}})
		}
		want := "'1.00':3 '1e2':1"
		if typ == "jsonb" {
			want = "'1.00':3 '100':1"
		}
		ftsJSONCases(t, []ftsJSONForm{{typ + "_to_tsvector", typ, true, true}}, []ftsJSONCase{
			{"simple", `[1e2,1.00]`, `"numeric"`, want},
			{"simple", `123456789012345678901234567890.00100`, `"numeric"`, "'123456789012345678901234567890.00100':1"},
		})
	}
}

func TestReferenceFTSJSONGaps(t *testing.T) {
	ftsJSONCases(t, ftsJSONForms(), []ftsJSONCase{
		{"english", `["cat","dog"]`, `"string"`, "'cat':1 'dog':3"},
		{"english", `["cat","","dog"]`, `"string"`, "'cat':1 'dog':3"},
		{"english", `["cat","the","dog"]`, `"string"`, "'cat':1 'dog':4"},
		{"english", `["cat the","dog"]`, `"string"`, "'cat':1 'dog':4"},
		{"english", `["the","cat"]`, `"string"`, "'cat':2"},
		{"english", `["cat","cat","the cat"]`, `"string"`, "'cat':1,3,6"},
		{"english", `["the","","the"]`, `"string"`, ""},
	})
}
