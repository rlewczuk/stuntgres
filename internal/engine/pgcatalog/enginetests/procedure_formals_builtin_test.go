package enginetests_test

import (
	"context"
	"strings"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
)

func TestRoutineResultFormalBuiltinCatalog(t *testing.T) {
	// Independent REL_18_6 pg_proc.dat expectations. This test consumes actual
	// declaration owners, but never uses their payload to compute its goldens.
	want := map[ids.OID]string{
		3713: "{26,23,25,25}|{i,o,o,o}", 3714: "{25,23,25,25}|{i,o,o,o}",
		3715: "{26,25,23,25}|{i,i,o,o}", 3716: "{25,25,23,25}|{i,i,o,o}",
		1689: "{1034,26,26,25,16}|{i,o,o,o,o}", 3958: "{114,25,114}|{i,o,o}",
		3959: "{114,25,25}|{i,o,o}", 3208: "{3802,25,3802}|{i,o,o}", 3932: "{3802,25,25}|{i,o,o}",
		3955: "{114,114}|{i,o}", 3969: "{114,25}|{i,o}", 3219: "{3802,3802}|{i,o}", 3465: "{3802,25}|{i,o}",
		3583: "{28,1184,26}|{o,o,o}", 6168: "{28,1184,26}|{i,o,o}", 3819: "{28,28,25}|{i,o,o}",
		6159: "{2205,1009,2205,1009,16,16}|{o,o,o,o,o,o}", 3423: "{2205,2205,2205,16,23}|{i,o,o,o,o}",
		3425: "{2205,2205}|{i,o}", 3084: "{19,25,25,25}|{i,o,o,o}",
		3839: "{26,26,23,25,25,25,25}|{i,i,i,o,o,o,o}", 3382: "{26,26,23,25,1009,1009}|{i,i,i,o,o,o}", 3954: "{25,1009,1009,26,26,23}|{i,i,i,o,o,o}",
		438: "{2276}|{v}", 440: "{2276}|{v}", 3058: "{2276}|{v}", 3059: "{25,2276}|{i,v}", 3539: "{25,2276}|{i,v}",
		3198: "{2276}|{v}", 3200: "{2276}|{v}", 3271: "{2276}|{v}", 3273: "{2276}|{v}",
		3951: "{114,1009}|{i,v}", 3953: "{114,1009}|{i,v}", 3217: "{3802,1009}|{i,v}", 3940: "{3802,1009}|{i,v}",
		3971: "{2281,2276}|{i,v}", 3987: "{2281,2276}|{i,v}", 3989: "{2281,2276}|{i,v}", 3991: "{2281,2276}|{i,v}", 3993: "{2281,2276}|{i,v}",
		3986: "{2276}|{v}", 3988: "{2276}|{v}", 3990: "{2276}|{v}", 3992: "{2276}|{v}",
		1397: "", 3540: "", 3204: "", 3205: "", 3490: "", 3491: "", 2331: "", 3199: "",
	}
	e := engine.New()
	view := e.CatalogRead()
	source := view.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: ids.ProcedureClassID})
	produce, ok := pgcatalog.ProducerFor(ids.ProcedureClassID)
	if !ok {
		t.Fatal("missing procedure producer")
	}
	captured, err := core.CaptureCatalogSourceRows(pgcatalog.Input{Context: context.Background(), Catalog: view, Roles: e.RoleStore.PublishedRead(view.Owner(), view), Ref: e.Catalog.Domain.Ref(view.Epoch(), source.Address()), Source: source, Owner: view.Owner(), Runtime: core.EngineCatalogRuntime{}}, produce)
	if err != nil {
		t.Fatal(err)
	}
	oids, modes := source.Columns[17].Usage.Type, source.Columns[18].Usage.Type
	if oids.ID() != 1028 || modes.ID() != 1002 {
		t.Fatal("formal array descriptor identities")
	}
	seen := map[ids.OID]bool{}
	wantDefaults := map[ids.OID]int64{1397: 0, 3960: 1, 4007: 2, 40: 0, 3717: 0}
	seenDefaults := map[ids.OID]bool{}
	for _, row := range captured.Anonymous {
		id, err := row[0].OID()
		if err != nil {
			t.Fatal(err)
		}
		if len(row) != 22 || row[20].Type() != source.Columns[20].Usage.Type || row[20].IsNull() {
			t.Fatal("default-count projection width/type/nullability", id)
		}
		if want, ok := wantDefaults[ids.OID(id)]; ok {
			got, err := row[20].Integer()
			if err != nil || got != want {
				t.Fatal("canonical default count", id, got, want, err)
			}
			seenDefaults[ids.OID(id)] = true
		}
		expected, ok := want[ids.OID(id)]
		if !ok {
			continue
		}
		if seen[ids.OID(id)] {
			t.Fatal("duplicate represented declaration", id)
		}
		seen[ids.OID(id)] = true
		a, m := row[17], row[18]
		if a.Type() != oids || m.Type() != modes {
			t.Fatal("incorrect typed formal arrays", id)
		}
		if expected == "" {
			if !a.IsNull() || !m.IsNull() {
				t.Error("all-IN not NULL", id)
			}
			continue
		}
		if a.IsNull() || m.IsNull() {
			t.Error("non-IN declared formals lost", id)
			continue
		}
		parts := strings.Split(expected, "|")
		at, err := oids.Output(context.Background(), a)
		if err != nil {
			t.Fatal(err)
		}
		mt, err := modes.Output(context.Background(), m)
		if err != nil {
			t.Fatal(err)
		}
		if at != parts[0] || mt != parts[1] {
			t.Error(id, at, mt, "want", expected)
		}
		if a.ArrayDimension(0).Lower != 1 || m.ArrayDimension(0).Lower != 1 {
			t.Error("formal arrays not one-based", id)
		}
	}
	if len(seen) != len(want) {
		t.Fatal("incomplete represented declaration roster", len(seen), len(want))
	}
	if len(seenDefaults) != len(wantDefaults) {
		t.Fatal("incomplete default-count declaration roster", seenDefaults)
	}
}
