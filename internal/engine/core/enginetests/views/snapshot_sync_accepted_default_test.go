package views_test

import (
	"fmt"
	"reflect"
	"slices"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
)

// Source preparation and admission mint all wrappers in this test. In
// particular a PL language label is not used to manufacture a provider mode.
func TestSnapshotSyncAcceptedDefaultBacking(t *testing.T) {
	for _, language := range []string{"sql", "pl"} {
		for _, exporting := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/exporting-predecessor=%v", language, exporting), func(t *testing.T) {
				e, s, owner, artifact, key, origin := acceptedDefaultFixture(t, language)
				// Prepare a second genuine generation from the chosen original
				// executable rather than transplanting a call into the first one.
				body := "5"
				if exporting {
					body = "length(pg_export_snapshot())"
				}
				enginetest.SessionSQL(t, s, "CREATE OR REPLACE FUNCTION accepted_lineage() RETURNS integer "+acceptedDefaultDeclaration(language, body))
				read := e.CatalogRead()
				_, ref, attribute, _ := artifact.Source()
				relation := read.MustRelation(ref.Address)
				owner.Retire()
				owner = exec.NewAcceptedDefaultSet()
				if _, _, err := prepareAcceptedDefault(t, e, owner, key, relation, ref, attribute); err != nil {
					t.Fatal(err)
				}
				if err := owner.Seal(42); err != nil {
					t.Fatal(err)
				}
				artifact, err := owner.Lookup(key, ref, attribute)
				if err != nil || artifact == nil {
					t.Fatal("missing actual new source preparation", err)
				}
				origin.Consumer, origin.Definition, origin.Accepted = relation, relation.ColumnMeta.Defaults[attribute], artifact
				origin.Fold.Definition = origin.Definition.OID
				var program exec.RoutineProgram
				if err := admitAcceptedDefault(t, e, artifact, key, origin, artifact.Expression(), "postgres", &program); err != nil {
					t.Fatal(err)
				}
				if len(program.Calls) != 1 {
					t.Fatalf("expected exact accepted occurrence, got %d calls", len(program.Calls))
				}
				before := snapshotAcceptedBacking(artifact)
				dependencies := slices.Clone(artifact.Dependencies())
				body = "length(pg_export_snapshot())"
				if exporting {
					body = "9"
				}
				enginetest.SessionSQL(t, s, "CREATE OR REPLACE FUNCTION accepted_lineage() RETURNS integer "+acceptedDefaultDeclaration(language, body))
				read = e.CatalogRead()
				// PL bodies are opaque to the incumbent pure classifier, even
				// when quiet. Never weaken that conservative contract to invent
				// a false negative control. Standard SQL RETURN 5 is closed.
				want := exporting || language == "pl"
				for range 3 {
					if got := program.ExportRootDemand(read); got != want {
						t.Fatalf("pure selected executable demand=%v want=%v", got, want)
					}
					if got := owner.ExportRootDemand(read); got != want {
						t.Fatalf("accepted-set selected demand=%v want=%v", got, want)
					}
					call := artifact.Expression().(interface{ Children() []expr.Expr }).Children()[0]
					if got, handled := exec.AcceptedDefaultExpressionExportRootDemand(read, call); !handled || got != want {
						t.Fatalf("accepted-call selected demand=%v/%v want=%v/true", got, handled, want)
					}
				}
				if !slices.Equal(before, snapshotAcceptedBacking(artifact)) || !slices.Equal(dependencies, artifact.Dependencies()) || !owner.MatchesGeneration(42) || owner.MatchesGeneration(41) {
					t.Fatal("pure demand changed source/path/prepared backing or generation")
				}
				copied := *owner
				if copied.Live() {
					t.Fatal("copied owner acquired original generation")
				}
				if _, err := copied.Lookup(key, ref, attribute); err == nil {
					t.Fatal("pure demand rehabilitated copied owner")
				}
				// Pure retention cannot cache EXECUTE authority. Admission after
				// the probe must still use the current principal/ACL.
				for _, query := range []string{
					"CREATE ROLE snapshot_demand_actor LOGIN",
					"GRANT USAGE ON SCHEMA public TO snapshot_demand_actor",
					"REVOKE EXECUTE ON FUNCTION accepted_lineage() FROM PUBLIC",
				} {
					enginetest.SessionSQL(t, s, query)
				}
				_ = program.ExportRootDemand(e.CatalogRead())
				if err := admitAcceptedDefault(t, e, artifact, key, origin, artifact.Expression(), "snapshot_demand_actor", nil); sqlStateOf(err) != "42501" {
					t.Fatal("demand supplied invocation authority", err)
				}
				owner.Retire()
				_ = program.ExportRootDemand(e.CatalogRead())
				if artifact.Valid() || admitAcceptedDefault(t, e, artifact, key, origin, artifact.Expression(), "postgres", nil) == nil {
					t.Fatal("retention resurrected retired accepted owner")
				}
				if !slices.Equal(before, snapshotAcceptedBacking(artifact)) || len(e.Storage.Relations[relation.Address()].Versions) != 0 {
					t.Fatal("pure demand mutated artifacts or evaluated a DEFAULT")
				}
			})
		}
	}
}

// Track actual private source/prepared backing without creating an inspection
// export or copying an invocation capability. Existing invocation/provider
// negative families call this around demand before exercising every strict fence.
func snapshotAcceptedBacking(artifact *exec.AcceptedDefault) []uintptr {
	value := reflect.ValueOf(artifact).Elem()
	var out []uintptr
	for _, name := range []string{"self", "owner", "relation", "dependencies", "descriptors", "root", "calls"} {
		out = append(out, value.FieldByName(name).Pointer())
	}
	calls := value.FieldByName("calls")
	for i := range calls.Len() {
		call := calls.Index(i).Elem()
		for _, name := range []string{"self", "artifact", "definition", "path"} {
			out = append(out, call.FieldByName(name).Pointer())
		}
		out = append(out, uintptr(call.FieldByName("mode").Uint()))
		for _, name := range []string{"source", "original"} {
			prepared := call.FieldByName(name)
			out = append(out, prepared.FieldByName("Procedure").Pointer())
			for _, field := range []string{"Args", "ArgNames", "InputMap", "ChildSlots"} {
				out = append(out, prepared.FieldByName(field).Pointer())
			}
			out = append(out, prepared.FieldByName("Signature").FieldByName("Inputs").Pointer())
		}
		path := call.FieldByName("path")
		for j := range path.Len() {
			out = append(out, uintptr(path.Index(j).Int()))
		}
	}
	return out
}

func snapshotAcceptedPureDemand(t *testing.T, program *exec.RoutineProgram, artifact *exec.AcceptedDefault, read catalog.Read) {
	t.Helper()
	before := snapshotAcceptedBacking(artifact)
	_ = program.ExportRootDemand(read)
	_, _ = exec.AcceptedDefaultExpressionExportRootDemand(read, artifact.Expression())
	_ = program.ExportRootDemand(read)
	if !slices.Equal(before, snapshotAcceptedBacking(artifact)) {
		t.Fatal("pure demand changed exact source/path/prepared invocation backing")
	}
}

func TestSnapshotSyncAcceptedDefaultProjection(t *testing.T) {
	for _, language := range []string{"sql", "pl"} {
		t.Run(language, func(t *testing.T) {
			e, _, owner, artifact, key, origin := acceptedDefaultFixture(t, language)
			read := e.CatalogRead()
			root := artifact.Expression()
			call := root.(expr.Composite).Children()[0]
			if demand, handled := exec.AcceptedDefaultExpressionExportRootDemand(read, origin.Definition.Expression); demand || handled {
				t.Fatal("ordinary source acquired accepted-call selection")
			}
			for _, original := range []expr.Expr{root, call} {
				value := reflect.ValueOf(original)
				copied := reflect.New(value.Elem().Type())
				copied.Elem().Set(value.Elem())
				node := copied.Interface().(expr.Expr)
				if demand, handled := exec.AcceptedDefaultExpressionExportRootDemand(read, node); !demand || !handled {
					t.Fatal("copied accepted node was not conservatively fenced")
				}
				if err := admitAcceptedDefault(t, e, artifact, key, origin, node, "postgres", nil); err == nil {
					t.Fatal("conservative retention authorized copied node")
				}
			}
			copied := *owner
			if !copied.ExportRootDemand(read) || copied.Live() {
				t.Fatal("copied set gained original ownership through demand")
			}
			owner.Retire()
			if !owner.ExportRootDemand(read) {
				t.Fatal("retired set lost conservative possible demand")
			}
			for _, node := range []expr.Expr{root, call} {
				if demand, handled := exec.AcceptedDefaultExpressionExportRootDemand(read, node); !demand || !handled {
					t.Fatal("retired occurrence regained a closed executable classification")
				}
			}
		})
	}
}
