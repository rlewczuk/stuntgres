package engine

import (
	"testing"

	"stuntgres/internal/sqltype"
)

func infoUserMappingsUnitText(t *testing.T, value sqltype.Value) string {
	t.Helper()
	base, err := value.BaseValue()
	if err != nil || base.Type().ID() != sqltype.Name {
		t.Fatal("NAME payload lost", base, err)
	}
	text, err := base.Text()
	if err != nil {
		t.Fatal("NAME text", err)
	}
	return text
}
