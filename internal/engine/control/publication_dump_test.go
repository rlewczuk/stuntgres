package control

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"slices"
	"testing"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

// This fixture exercises the real descriptor providers and persistent catalog
// roots, without SQL or a substitute replay engine. Public byte replay and
// failed replacement assertions belong to the reference NativeControls family.
func publicationDumpFixture(t *testing.T) (*Capture, *catalog.Publication, *catalog.PublicationRelation, *catalog.Relation) {
	t.Helper()
	domain := new(authorization.Domain)
	roles := authorization.Seed(domain)
	owner := domain.Principal(10)
	state := catalog.NewState(1)
	address := func(class, oid ids.OID) ids.ObjectAddress {
		return ids.ObjectAddress{ClassID: class, ObjectID: oid}
	}
	ns := &catalog.Namespace{ObjectAddress: address(ids.NamespaceClassID, 91000), ObjectRevision: 1, ObjectName: `current"schema`, Owner: owner}
	r := &catalog.Relation{ObjectAddress: address(ids.RelationClassID, 91001), ObjectNamespace: ns.Address(), ObjectRevision: 1, ObjectName: "current_table", Owner: owner, RelationKind: catalog.TableRelation,
		Columns: []catalog.Column{{Name: "first"}, {Name: `second"column`}}, Attributes: []int32{2, 7}}
	p := &catalog.Publication{ObjectAddress: address(ids.PublicationClassID, 91002), ObjectRevision: 1, ObjectName: "z_explicit", Owner: roles.Read(owner).LookupRef("pg_monitor"), Insert: true, Delete: true, ViaRoot: true, GeneratedColumns: 's'}
	all := &catalog.Publication{ObjectAddress: address(ids.PublicationClassID, 91003), ObjectRevision: 1, ObjectName: "a_all", Owner: owner, AllTables: true, Truncate: true, GeneratedColumns: 'n'}
	schema := &catalog.Publication{ObjectAddress: address(ids.PublicationClassID, 91004), ObjectRevision: 1, ObjectName: "m_schema", Owner: owner, GeneratedColumns: 'n'}
	explicit := &catalog.PublicationRelation{ObjectAddress: address(ids.PublicationRelationClassID, 91005), ObjectRevision: 1, Publication: p.Address(), Relation: r.Address(), ColumnsSpecified: true}
	columns := []int32{2, 7}
	explicit.SetColumns(columns)
	columns[0] = 99 // The input must not become the descriptor's retained backing.
	unrestricted := &catalog.PublicationRelation{ObjectAddress: address(ids.PublicationRelationClassID, 91006), ObjectRevision: 1, Publication: schema.Address(), Relation: r.Address()}
	mapping := &catalog.PublicationNamespace{ObjectAddress: address(ids.PublicationNamespaceClassID, 91007), ObjectRevision: 1, Publication: schema.Address(), Schema: ns.Address()}
	descriptors := []catalog.Descriptor{ns, r, p, all, schema, explicit, unrestricted, mapping}
	for _, d := range descriptors {
		state.SetDescriptor(d.Address(), d)
		state.BindName(catalog.DescriptorNameKey(d), d.Address())
	}
	read := catalog.NewRead(&catalog.Owner{Principals: domain, Providers: map[ids.OID]catalog.Provider{
		ids.PublicationClassID:          catalog.PublicationProvider{},
		ids.PublicationRelationClassID:  catalog.PublicationRelationProvider{},
		ids.PublicationNamespaceClassID: catalog.PublicationNamespaceProvider{},
	}}, state)
	capture := &Capture{CatalogView: read, OriginalCatalog: read, OriginalRoles: roles.Read(owner), DomainRoles: roles.Read(owner)}
	collection := &dumpCollection{Capture: capture}
	for _, d := range descriptors {
		if isDumpPublication(d) {
			if err := collectPublicationDump(context.Background(), collection, d.Address(), d); err != nil {
				t.Fatal(err)
			}
		}
	}
	return capture, p, explicit, r
}

func TestPublicationDumpDeterministicDefinitions(t *testing.T) {
	capture, _, _, _ := publicationDumpFixture(t)
	script := &dumpScript{Capture: capture}
	if err := script.preparePublications(context.Background()); err != nil {
		t.Fatal(err)
	}
	want := []publicationReplay{
		{owner: "postgres", name: `"a_all"`, sql: "CREATE PUBLICATION \"a_all\" FOR ALL TABLES WITH (publish = 'truncate', publish_via_partition_root = false, publish_generated_columns = none);\nALTER PUBLICATION \"a_all\" OWNER TO \"postgres\";\n"},
		{owner: "postgres", name: `"m_schema"`, sql: "CREATE PUBLICATION \"m_schema\" WITH (publish = '', publish_via_partition_root = false, publish_generated_columns = none);\nALTER PUBLICATION \"m_schema\" ADD TABLE ONLY \"current\"\"schema\".\"current_table\";\nALTER PUBLICATION \"m_schema\" ADD TABLES IN SCHEMA \"current\"\"schema\";\nALTER PUBLICATION \"m_schema\" OWNER TO \"postgres\";\n"},
		{owner: "postgres", name: `"z_explicit"`, sql: "CREATE PUBLICATION \"z_explicit\" WITH (publish = 'insert, delete', publish_via_partition_root = true, publish_generated_columns = stored);\nALTER PUBLICATION \"z_explicit\" ADD TABLE ONLY \"current\"\"schema\".\"current_table\" (\"first\", \"second\"\"column\");\nALTER PUBLICATION \"z_explicit\" OWNER TO \"pg_monitor\";\n"},
	}
	if !reflect.DeepEqual(script.publicationReplay, want) {
		t.Fatalf("replay = %#v; want %#v", script.publicationReplay, want)
	}
	// Provider enumeration order and unrelated numeric allocation order have no
	// authority over the canonical publication section.
	reordered := *capture
	reordered.Publications = slices.Clone(capture.Publications)
	reordered.PublicationRelations = slices.Clone(capture.PublicationRelations)
	reordered.PublicationNamespaces = slices.Clone(capture.PublicationNamespaces)
	slices.Reverse(reordered.Publications)
	slices.Reverse(reordered.PublicationRelations)
	second := &dumpScript{Capture: &reordered}
	if err := second.preparePublications(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(second.publicationReplay, want) {
		t.Fatalf("enumeration order changed replay: %#v", second.publicationReplay)
	}
	var out bytes.Buffer
	if err := emitDumpPublications(context.Background(), script, &dumpWriter{ctx: context.Background(), dst: &out}); err != nil {
		t.Fatal(err)
	}
	var expected bytes.Buffer
	expected.WriteString("SET SESSION AUTHORIZATION 'postgres';\n")
	for _, r := range want {
		expected.WriteString(r.sql)
	}
	if out.String() != expected.String() {
		t.Fatalf("emitted publication section = %q; want %q", out.String(), expected.String())
	}
}

func TestPublicationDumpRetainsCapturedRootAndBacking(t *testing.T) {
	capture, p, mapping, relation := publicationDumpFixture(t)
	columnBacking := &mapping.Columns[0]
	attributeBacking := &relation.Attributes[0]
	next := catalog.CloneState(capture.OriginalCatalog.State())
	replacement := *p
	replacement.ObjectRevision++
	replacement.Insert = false
	replacement.Update = true
	replacement.Owner = capture.OriginalCatalog.Owner().Principals.Principal(10)
	next.SetDescriptor(p.Address(), &replacement)
	r := *relation
	r.ObjectRevision++
	r.Columns = slices.Clone(relation.Columns)
	r.Columns[0].Name = "renamed_after_capture"
	r.Columns = append(r.Columns, catalog.Column{Name: "future"})
	r.Attributes = append(slices.Clone(relation.Attributes), 8)
	next.SetDescriptor(r.Address(), &r)
	m := *mapping
	m.ObjectRevision++
	m.SetColumns([]int32{7})
	next.SetDescriptor(m.Address(), &m)
	current := catalog.NewRead(capture.OriginalCatalog.Owner(), next)
	if capture.OriginalCatalog.Get(p.Address()) != p || capture.OriginalCatalog.Get(mapping.Address()) != mapping || capture.OriginalCatalog.Get(relation.Address()) != relation {
		t.Fatal("replacement changed captured descriptor authority")
	}
	if &mapping.Columns[0] != columnBacking || &relation.Attributes[0] != attributeBacking || !slices.Equal(mapping.Columns, []int32{2, 7}) || relation.Columns[0].Name != "first" {
		t.Fatal("replacement mutated retained capture backing")
	}
	old := &dumpScript{Capture: capture}
	if err := old.preparePublications(context.Background()); err != nil {
		t.Fatal(err)
	}
	fresh := &Capture{CatalogView: current, OriginalCatalog: current, OriginalRoles: capture.OriginalRoles, DomainRoles: capture.DomainRoles}
	collection := &dumpCollection{Capture: fresh}
	for a, d := range next.Descriptors() {
		if isDumpPublication(d) {
			if err := collectPublicationDump(context.Background(), collection, a, d); err != nil {
				t.Fatal(err)
			}
		}
	}
	newer := &dumpScript{Capture: fresh}
	if err := newer.preparePublications(context.Background()); err != nil {
		t.Fatal(err)
	}
	if reflect.DeepEqual(old.publicationReplay, newer.publicationReplay) {
		t.Fatal("new definition failed to distinguish retained capture from current catalog")
	}
	// The absent list remains absent as the table grows. The original explicit
	// list retains both stable attributes; the replacement selects only attr 7.
	if capture.PublicationRelations[0] != mapping || !mapping.ColumnsSpecified || fresh.PublicationRelations == nil {
		t.Fatal("captured membership identity was reconstructed or lost")
	}
	if current.Get(p.Address()) != &replacement || current.Get(mapping.Address()) != &m || current.Get(relation.Address()) != &r {
		t.Fatal("capture preparation mutated current catalog")
	}
}

func TestPublicationDumpPreflightRejectsInvalidAuthority(t *testing.T) {
	for _, failure := range []string{"unknown owner", "foreign owner", "invalid option", "stale mapping", "orphan capture"} {
		t.Run(failure, func(t *testing.T) {
			capture, p, mapping, _ := publicationDumpFixture(t)
			switch failure {
			case "unknown owner":
				p.Owner = capture.OriginalCatalog.Owner().Principals.Principal(999999)
			case "foreign owner":
				p.Owner = new(authorization.Domain).Principal(10)
			case "invalid option":
				p.GeneratedColumns = 'x'
			case "stale mapping":
				copy := *mapping
				capture.PublicationRelations[0] = &copy
			case "orphan capture":
				capture.Publications = nil
			}
			script := &dumpScript{Capture: capture}
			if err := script.preparePublications(context.Background()); err == nil {
				t.Fatal("invalid publication capture accepted")
			}
			if script.publicationReplay != nil {
				t.Fatal("failed preflight published partial replay")
			}
		})
	}
}

func TestPublicationDumpCanceledCaptureAndEmission(t *testing.T) {
	capture, p, _, _ := publicationDumpFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	empty := &Capture{CatalogView: capture.CatalogView, OriginalRoles: capture.OriginalRoles}
	if err := collectPublicationDump(ctx, &dumpCollection{Capture: empty}, p.Address(), p); !errors.Is(err, context.Canceled) || len(empty.Publications) != 0 {
		t.Fatalf("canceled collection = %v, %#v", err, empty.Publications)
	}
	script := &dumpScript{Capture: capture}
	if err := script.preparePublications(ctx); !errors.Is(err, context.Canceled) || script.publicationReplay != nil {
		t.Fatalf("canceled preparation = %v, %#v", err, script.publicationReplay)
	}
	if err := script.preparePublications(context.Background()); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := emitDumpPublications(ctx, script, &dumpWriter{ctx: ctx, dst: &out}); !errors.Is(err, context.Canceled) || out.Len() != 0 {
		t.Fatalf("canceled emission = %v, %q", err, out.String())
	}
}
