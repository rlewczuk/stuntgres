package stored

import (
	"context"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

func TestRewriteRoutineIdentityPresentation(t *testing.T) {
	read, target, _, _ := routineCaptureFixture(t)
	pg := &catalog.Namespace{ObjectAddress: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PGCatalogNamespaceOID}, ObjectName: "pg_catalog", ObjectRevision: 1}
	read.State().SetDescriptor(pg.Address(), pg)
	function := &catalog.Procedure{ObjectAddress: ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 920}, ObjectNamespace: target.Namespace(), ObjectName: "tf", ObjectRevision: 1}
	read.State().SetDescriptor(function.Address(), function)
	read.State().BindName(catalog.DescriptorNameKey(function), function.Address())
	check := func(path []ids.ObjectAddress, want string) {
		t.Helper()
		got, err := (Deparser{View: read, Path: path}).sqlRoutineName(function)
		if err != nil || got != want {
			t.Fatalf("routine name = %q, %v; want %q", got, err, want)
		}
	}
	check([]ids.ObjectAddress{target.Namespace()}, "tf")
	check([]ids.ObjectAddress{pg.Address()}, "capture.tf")
	// A new current descriptor, not the retained creation spelling or a decoy,
	// owns the displayed name. The earlier snapshot remains unchanged.
	next := catalog.CloneState(read.State())
	current := *function
	current.ObjectName = "tf_now"
	next.SetDescriptor(current.Address(), &current)
	next.BindName(catalog.DescriptorNameKey(&current), current.Address())
	got, err := (Deparser{View: read.WithState(next), Path: []ids.ObjectAddress{pg.Address()}}).sqlRoutineName(&current)
	if err != nil || got != "capture.tf_now" {
		t.Fatal("current routine identity", got, err)
	}
	check([]ids.ObjectAddress{pg.Address()}, "capture.tf")

	// RI functions are release identities, not executable Procedure payloads.
	if _, exists := read.Procedure(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 1644}); exists {
		t.Fatal("fixture unexpectedly admits an executable RI function")
	}
	fact := catalog.ForeignKeyTriggerFact{Relation: target.Address(), Name: "ri"}
	attributes := catalog.ForeignKeyTriggerAttributes{Opposite: target.Address(), Function: 1644, Flags: 5}
	text, err := ForeignKeyTriggerDefinitionSQL(context.Background(), fact, attributes, Deparser{View: read, Path: []ids.ObjectAddress{pg.Address()}})
	want := "CREATE CONSTRAINT TRIGGER ri AFTER INSERT ON capture.target FROM capture.target NOT DEFERRABLE INITIALLY IMMEDIATE FOR EACH ROW EXECUTE FUNCTION \"RI_FKey_check_ins\"()"
	if err != nil || text != want {
		t.Fatalf("metadata-only RI definition = %q, %v; want %q", text, err, want)
	}
}

func TestRewriteSelectSourceTemporaryNaming(t *testing.T) {
	read, target, _, _ := routineCaptureFixture(t)
	owner := read.Owner().Domain.Session(1)
	namespace := &catalog.Namespace{ObjectAddress: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 930}, ObjectName: "pg_temp_42", Temporary: owner}
	source := *target
	source.ObjectNamespace, source.ObjectName = namespace.Address(), "t"
	decoy := source
	decoy.ObjectAddress = ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 931}
	decoy.ObjectNamespace = target.Namespace()
	read.State().SetDescriptor(namespace.Address(), namespace)
	read.State().SetDescriptor(source.Address(), &source)
	read.State().BindName(catalog.DescriptorNameKey(&source), source.Address())
	read.State().SetDescriptor(decoy.Address(), &decoy)
	read.State().BindName(catalog.DescriptorNameKey(&decoy), decoy.Address())
	for _, tc := range []struct {
		name  string
		token ids.SessionToken
		want  string
	}{
		{"own", owner, "pg_temp"},
		{"foreign", read.Owner().Domain.Session(2), "pg_temp_42"},
		{"zero", ids.SessionToken{}, "pg_temp_42"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, pretty := range []bool{false, true} {
				for _, quoteAll := range []bool{false, true} {
					p := Presentation{Deparse: true, Pretty: pretty, TemporaryOwner: tc.token}
					p.Output.QuoteAllIdentifiers = quoteAll
					for _, visible := range []bool{false, true} {
						p.Path = []ids.ObjectAddress{target.Namespace(), namespace.Address()}
						if visible {
							p.Path = []ids.ObjectAddress{namespace.Address(), target.Namespace()}
						}
						want := p.identifier("t")
						if !visible {
							want = p.identifier(tc.want) + "." + want
						}
						got, err := p.sourceRelationName(read, &source)
						if err != nil || got != want {
							t.Fatalf("pretty=%v quote-all=%v visible=%v: source = %q, %v; want %q", pretty, quoteAll, visible, got, err, want)
						}
					}
					// Executable rendering never substitutes the session alias,
					// even when display settings and an own-session token exist.
					p.Deparse = false
					p.Path = []ids.ObjectAddress{target.Namespace(), namespace.Address()}
					got, err := p.sourceRelationName(read, &source)
					want := "pg_temp_42.t"
					if quoteAll {
						want = "\"pg_temp_42\".\"t\""
					}
					if err != nil || got != want {
						t.Fatal("executable source naming changed", got, err)
					}
				}
			}
		})
	}
}
