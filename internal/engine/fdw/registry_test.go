package fdw

import (
	"encoding/base64"
	"testing"

	"stuntgres/internal/engine/catalog"
)

type fdwStubWrapper struct{ catalog.ForeignDataWrapper }

func TestFDWValidateDefinitions(t *testing.T) {
	var missing *fdwStubWrapper
	if _, err := ValidateWrapper(WrapperDefinition{HandlerName: catalog.ForeignFunctionName{Name: "handler"}, Wrapper: missing}); err == nil {
		t.Fatal("typed nil admitted")
	}
	def, err := ValidateWrapper(WrapperDefinition{HandlerName: catalog.ForeignFunctionName{Name: "handler"}, Wrapper: &fdwStubWrapper{}})
	if err != nil || def.HandlerName.Schema != "public" {
		t.Fatal(def, err)
	}
	if _, err = ValidateValidator(ValidatorDefinition{Name: catalog.ForeignFunctionName{Name: "validator"}}); err == nil {
		t.Fatal("nil validator admitted")
	}
}

func TestDblinkConnectionReplay(t *testing.T) {
	for _, tc := range []struct {
		payload                 string
		kind, provider, version string
	}{
		{`{"Kind":"dblink","Handler":{"Schema":"","Name":""},"Provider":"transport","Version":"1"}`, "dblink", "transport", "1"},
		{`{"Kind":"dblink","Handler":{"Schema":"","Name":""},"Provider":"","Version":""}`, "dblink", "", ""},
		{`{"Kind":"handler","Handler":{"Schema":"public","Name":"handler"},"Provider":"transport","Version":"1"}`, "handler", "transport", "1"},
	} {
		comment := ExternalReplayPrefix + base64.RawStdEncoding.EncodeToString([]byte(tc.payload))
		required, recognized, err := DecodeExternalRequirement(comment)
		if err != nil || !recognized || required.Kind != tc.kind || required.Provider != tc.provider || required.Version != tc.version {
			t.Fatal("canonical prerequisite rejected", required, recognized, err)
		}
		if required.Comment() != "-- "+comment+"\n" {
			t.Fatal("canonical prerequisite did not round-trip")
		}
	}
	for _, payload := range []string{
		`{"Kind":"dblink","Handler":{"Schema":"","Name":""},"Provider":"transport","Version":""}`,
		`{"Kind":"dblink","Handler":{"Schema":"","Name":""},"Provider":"","Version":"1"}`,
		`{"Kind":"dblink","Handler":{"Schema":"public","Name":"handler"},"Provider":"transport","Version":"1"}`,
		`{"Kind":"unknown","Handler":{"Schema":"","Name":""},"Provider":"transport","Version":"1"}`,
		`{"Kind":"handler","Handler":{"Schema":"public","Name":"handler"},"Provider":"","Version":""}`,
		`{"Kind":"handler","Handler":{"Schema":"","Name":"handler"},"Provider":"transport","Version":"1"}`,
		`{"Kind":"dblink","Handler":{"Schema":"","Name":""},"Provider":"","Version":"","extra":true}`,
		`{"Kind":"dblink","Kind":"dblink","Handler":{"Schema":"","Name":""},"Provider":"","Version":""}`,
		` {"Kind":"dblink","Handler":{"Schema":"","Name":""},"Provider":"","Version":""}`,
	} {
		if _, recognized, err := DecodeExternalRequirement(ExternalReplayPrefix + base64.RawStdEncoding.EncodeToString([]byte(payload))); !recognized || err == nil {
			t.Fatal("malformed prerequisite admitted")
		}
	}
	for _, comment := range []string{"stuntgres-external-sql-v1 malformed", "stuntgres-external-sql-v99 arbitrary"} {
		if _, recognized, err := DecodeExternalRequirement(comment); !recognized || err == nil {
			t.Fatal("malformed/versioned marker admitted")
		}
	}
	var registry Registry
	if err := registry.ValidateExternalRequirement(ExternalRequirement{Kind: "dblink"}); err != nil {
		t.Fatal("explicit unbound source demanded a capability", err)
	}
	if err := registry.ValidateExternalRequirement(ExternalRequirement{Kind: "dblink", Provider: "transport", Version: "1"}); err == nil {
		t.Fatal("nonempty requirement borrowed unavailable destination capability")
	}
}
