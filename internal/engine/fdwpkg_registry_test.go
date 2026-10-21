package engine

import (
	"testing"

	"stuntgres/internal/engine/catalog"
)

type fdwStubWrapper struct{ catalog.ForeignDataWrapper }

func TestFDWValidateDefinitions(t *testing.T) {
	var missing *fdwStubWrapper
	if _, err := ValidateForeignDataWrapper(ForeignDataWrapperDefinition{HandlerName: catalog.ForeignFunctionName{Name: "handler"}, Wrapper: missing}); err == nil {
		t.Fatal("typed nil admitted")
	}
	def, err := ValidateForeignDataWrapper(ForeignDataWrapperDefinition{HandlerName: catalog.ForeignFunctionName{Name: "handler"}, Wrapper: &fdwStubWrapper{}})
	if err != nil || def.HandlerName.Schema != "public" {
		t.Fatal(def, err)
	}
	if _, err = ValidateForeignValidator(ForeignValidatorDefinition{Name: catalog.ForeignFunctionName{Name: "validator"}}); err == nil {
		t.Fatal("nil validator admitted")
	}
}
