package control_test

import (
	"encoding/base64"
	"reflect"
	"strings"
	"testing"

	"stuntgres/internal/engine/control"
	"stuntgres/internal/engine/sepgsql"
)

func TestSepgsqlRetiredColumnMetadata(t *testing.T) {
	policy, err := sepgsql.Compile(sepgsql.SimulationConfig{Policy: sepgsql.Policy{
		ID: "retired", Version: "1", Contexts: []string{"A", "P", "U"}, InitialContext: "A", UnlabeledContext: "U",
	}})
	if err != nil {
		t.Fatal(err)
	}
	want := control.SepgsqlRequirement{ID: "retired", Version: "1", Database: "source", RetiredColumns: []control.SepgsqlRetiredColumnLabel{{Schema: "odd.schema", Relation: "odd.table", Attribute: 2, Label: "P"}}}
	comment, err := want.Comment()
	if err != nil {
		t.Fatal(err)
	}
	got, recognized, err := control.DecodeSepgsqlRequirement(strings.TrimPrefix(comment, "-- "))
	if err != nil || !recognized || !reflect.DeepEqual(got, &want) || control.ValidateSepgsqlRequirement(got, policy) != nil {
		t.Fatal("typed retired-column round trip", got, recognized, err)
	}
	for _, payload := range []string{
		`{"id":"retired","version":"1","database":"source"}`,
		`{"id":"retired","version":"1","database":"source","retired_columns":[]}`,
	} {
		_, recognized, err := control.DecodeSepgsqlRequirement(control.SepgsqlRequirementMarker + " " + base64.StdEncoding.EncodeToString([]byte(payload)))
		if !recognized || err != nil {
			t.Fatal("ordinary image compatibility", err)
		}
	}
	base := `{"id":"retired","version":"1","database":"source","retired_columns":[RECORD]}`
	record := `{"schema":"public","relation":"items","attribute":2,"label":"P"}`
	for _, payload := range []string{
		strings.Replace(base, "RECORD", record+","+record, 1),
		strings.Replace(base, "RECORD", record+","+strings.Replace(record, `"P"`, `"U"`, 1), 1),
		strings.Replace(base, "RECORD", strings.Replace(record, `"attribute":2`, `"attribute":0`, 1), 1),
		strings.Replace(base, "RECORD", strings.Replace(record, `"attribute":2`, `"attribute":-1`, 1), 1),
		strings.Replace(base, "RECORD", strings.Replace(record, `"schema":"public"`, `"schema":""`, 1), 1),
		strings.Replace(base, "RECORD", strings.Replace(record, `"label":"P"`, `"label":null`, 1), 1),
		strings.Replace(base, "RECORD", strings.Replace(record, `"attribute":2`, `"attribute":2,"attribute":3`, 1), 1),
		strings.Replace(base, "RECORD", strings.Replace(record, `"attribute":2`, `"attribute":2,"oid":42`, 1), 1),
		strings.Replace(base, "RECORD", strings.Replace(record, `"relation":"items"`, `"relation":"a\u0000b"`, 1), 1),
		strings.Replace(base, "[RECORD]", "null", 1),
		strings.Replace(base, "RECORD", record, 1) + `{}`,
	} {
		_, recognized, err := control.DecodeSepgsqlRequirement(control.SepgsqlRequirementMarker + " " + base64.StdEncoding.EncodeToString([]byte(payload)))
		if !recognized || err == nil {
			t.Fatal("malformed retired-column authority accepted", payload, err)
		}
	}
	got.RetiredColumns[0].Label = "undeclared"
	if control.ValidateSepgsqlRequirement(got, policy) == nil {
		t.Fatal("undeclared context accepted")
	}
	got.RetiredColumns[0].Label = "P"
	got.Version = "other"
	if control.ValidateSepgsqlRequirement(got, policy) == nil {
		t.Fatal("incompatible policy accepted")
	}
}
