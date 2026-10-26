package extension

import (
	"testing"

	"stuntgres/internal/engine/catalog"
)

func TestRoutineDefaultDeclarationsInstalled(t *testing.T) {
	d := verifyHeapamDeclaration()
	if d.Defaults != 5 || d.Outputs.Inputs() != 6 || d.Outputs.Len() != 4 || verifyHeapamDefaults.Len() != 5 {
		t.Fatal("verify_heapam declaration", d)
	}
	expected := []catalog.RoutineLiteral{{SourceType: 16, Text: "false"}, {SourceType: 16, Text: "false"}, {SourceType: 25, Text: "none"}, {SourceType: 20, Null: true}, {SourceType: 20, Null: true}}
	for i, v := range expected {
		if verifyHeapamDefaults.At(i) != v {
			t.Fatal("default", i, verifyHeapamDefaults.At(i), v)
		}
	}
	names := []string{"relation", "on_error_stop", "check_toast", "skip", "startblock", "endblock", "blkno", "offnum", "attnum", "msg"}
	for i, name := range names {
		if d.Outputs.Name(i) != name {
			t.Fatal("formal name", i)
		}
	}
	for i, typ := range []uint32{20, 23, 23, 25} {
		if uint32(d.Outputs.Type(i)) != typ {
			t.Fatal("output type", i)
		}
	}
	for _, m := range AmcheckMembers {
		if m.Declaration.Defaults != 0 {
			t.Fatal("invented B-tree defaults", m.Name)
		}
	}
}
