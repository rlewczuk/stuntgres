package extension

import (
	"testing"

	"stuntgres/internal/engine/catalog"
)

func TestPgStatStatementsInstallationResetDefaults(t *testing.T) {
	declaration := pgStatStatementsDeclaration(2)
	if declaration.Parallel != 's' || declaration.ReturnsSet || declaration.Rows != 0 || declaration.Defaults != 4 || declaration.Outputs != nil || declaration.ArgumentNames != ([4]string{"userid", "dbid", "queryid", "minmax_only"}) {
		t.Fatal("released reset declaration", declaration)
	}
	if pgStatStatementsResetDefaults.Len() != 4 {
		t.Fatal("reset must have four genuine trailing defaults")
	}
	for i, expected := range []catalog.RoutineLiteral{{SourceType: 23, Text: "0"}, {SourceType: 23, Text: "0"}, {SourceType: 23, Text: "0"}, {SourceType: 16, Text: "false"}} {
		if pgStatStatementsResetDefaults.At(i) != expected {
			t.Fatal("reset literal recipe", i, pgStatStatementsResetDefaults.At(i), expected)
		}
	}
	if pgStatStatementsDeclaration(0).Outputs != pgStatStatementsOutputs || !pgStatStatementsDeclaration(0).ReturnsSet || pgStatStatementsDeclaration(1).Outputs != pgStatStatementsInfoOutputs || pgStatStatementsDeclaration(1).ReturnsSet {
		t.Fatal("reset cutover changed incumbent reader slots")
	}
}
