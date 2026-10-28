package partitioncmds_test

import (
	"context"
	"encoding/base64"
	"errors"
	"strings"
	"stuntgres/internal/engine/partition"
	"stuntgres/internal/engine/partitioncmds"
	"stuntgres/internal/sqlstate"
	"testing"
)

func TestPartitionDetachConcurrentRestoreRepair(t *testing.T) {
	want := partition.PendingDetachReplay{ParentSchema: "p.schema", ParentTable: "P é", ChildSchema: "c.schema", ChildTable: "C name", Strategy: "l"}
	raw := `{"parent_schema":"p.schema","parent_table":"P é","child_schema":"c.schema","child_table":"C name","strategy":"l"}`
	line := "-- stuntgres-partition-detach-pending-v1 " + base64.StdEncoding.EncodeToString([]byte(raw)) + "\n"
	emitted, err := want.Comment()
	if err != nil || emitted != line {
		t.Fatal("canonical logical wire encoding", emitted, err)
	}
	got, recognized, err := partition.DecodePendingDetach(line)
	if err != nil || !recognized || got != want {
		t.Fatal("strict logical decode", got, recognized, err)
	}
	for _, bad := range []string{
		strings.Replace(raw, `"strategy":"l"`, `"strategy":null`, 1),
		strings.Replace(raw, `"strategy":"l"`, `"strategy":1`, 1),
		strings.Replace(raw, `"strategy":"l"`, `"strategy":""`, 1),
		strings.Replace(raw, `"strategy":"l"`, `"strategy":"x"`, 1),
		strings.Replace(raw, `,"strategy":"l"`, "", 1),
		strings.TrimSuffix(raw, "}") + `,"strategy":"l"}`,
		strings.TrimSuffix(raw, "}") + `,"extra":"x"}`,
		raw + "{}", "[]", "null", `{"parent_schema":"p.schema"}`, string([]byte{0xff}),
	} {
		comment := "-- stuntgres-partition-detach-pending-v1 " + base64.StdEncoding.EncodeToString([]byte(bad))
		if _, recognized, err := partition.DecodePendingDetach(comment); !recognized || err == nil {
			t.Fatal("malformed reserved record disappeared", bad, recognized, err)
		}
	}
	for _, comment := range []string{"-- stuntgres-partition-detach-pending-v2 abc", "-- stuntgres-partition-detach-pending-v1 !", line + "extra", "-- stuntgres-partition-detach-pending-v1"} {
		if _, recognized, err := partition.DecodePendingDetach(comment); !recognized || err == nil {
			t.Fatal("malformed marker accepted", comment, recognized, err)
		}
	}
	if _, recognized, err := partition.DecodePendingDetach("-- an unrelated comment"); recognized || err != nil {
		t.Fatal("unrelated comment gained repair authority", recognized, err)
	}
	f := detachConcurrentUnitTree(t)
	scope, op := partitionAttachUnitScope(t, f.e, sessionStart(t, f.e, nil))
	before := f.e.Catalog.State
	record := partition.PendingDetachReplay{ParentSchema: "public", ParentTable: f.parent.Name(), ChildSchema: "public", ChildTable: f.child.Name(), Strategy: "r"}
	err = partitioncmds.ImportPendingDetaches(f.e, context.Background(), op, []partition.PendingDetachReplay{record})
	var diagnostic *sqlstate.Error
	if err == nil || errors.As(err, &diagnostic) || f.e.Catalog.State != before || f.child.Membership.DetachPending {
		t.Fatal("ordinary operation acquired private repair authority", err)
	}
	if scope.Finished {
		t.Fatal("rejected importer consumed unrelated operation")
	}
}
