package fdw

import (
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
