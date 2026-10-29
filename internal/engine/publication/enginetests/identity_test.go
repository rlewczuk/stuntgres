package publication_test

import (
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/publication"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

type identityFixture struct {
	v catalog.Read
	r *catalog.Relation
	p *catalog.Publication
	m *catalog.PublicationRelation
}

func identityCatalog(t *testing.T) identityFixture {
	t.Helper()
	e := engine.New()
	base := e.CatalogRead()
	state := catalog.CloneState(base.State())
	ns, ok := base.Namespace(ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID})
	if !ok {
		t.Fatal("missing real public namespace")
	}
	integer, _ := base.LookupType(sqltype.Int4)
	r := &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 48001}, ObjectNamespace: ns.Address(), ObjectRevision: 1, ObjectName: "identity_leaf", Owner: ns.Owner, RelationKind: catalog.TableRelation,
		Columns: []catalog.Column{{Name: "id", Usage: integer.Usage()}, {Name: "v", Usage: integer.Usage()}}, Attributes: []int32{10, 20}, Index: map[string]int{"id": 0, "v": 1}}
	r.Constraints.Unique = []catalog.UniqueConstraint{{OID: 48004, Primary: true, Attributes: []int32{10}}}
	p := &catalog.Publication{ObjectAddress: ids.ObjectAddress{ClassID: ids.PublicationClassID, ObjectID: 48002}, ObjectRevision: 1, ObjectName: "identity_publication", Owner: ns.Owner, Update: true, Delete: true, GeneratedColumns: 'n'}
	m := &catalog.PublicationRelation{ObjectAddress: ids.ObjectAddress{ClassID: ids.PublicationRelationClassID, ObjectID: 48003}, ObjectRevision: 1, Publication: p.Address(), Relation: r.Address()}
	for _, d := range []catalog.Descriptor{r, p, m} {
		state.SetDescriptor(d.Address(), d)
		state.BindName(catalog.DescriptorNameKey(d), d.Address())
	}
	v := base.WithState(state)
	if err := (catalog.PublicationProvider{}).Validate(v, p); err != nil {
		t.Fatal(err)
	}
	if err := (catalog.PublicationRelationProvider{}).Validate(v, m); err != nil {
		t.Fatal(err)
	}
	return identityFixture{v, r, p, m}
}

func (f identityFixture) filter(sub int32) {
	// This is the production retained payload and ordinary bound-column node,
	// not SQL text, a substitute evaluator, or a fake PolicyPayload.
	ref := f.v.Owner().Domain.Ref(f.v.Epoch(), f.r.Address())
	ref.Address.SubID = sub
	f.m.Filter = stored.NewPolicy(nil, plan.ColumnExpr{Info: expr.Info{Usage: f.r.Columns[0].Usage}, Origin: ref}, nil)
}

func identityWant(t *testing.T, err error, update bool, reason string) {
	t.Helper()
	if reason == "" {
		if err != nil {
			t.Fatal(err)
		}
		return
	}
	verb, action, gerund := "delete from table", "deletes", "deleting from"
	if update {
		verb, action, gerund = "update table", "updates", "updating"
	}
	want := &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42P10", Message: "cannot " + verb + ` "identity_leaf"`}
	switch reason {
	case "filter":
		want.Detail = "Column used in the publication WHERE expression is not part of the replica identity."
	case "columns":
		want.Detail = "Column list used by the publication does not cover the replica identity."
	case "generated":
		want.Detail = "Replica identity must not contain unpublished generated columns."
	case "missing":
		want.Code = "55000"
		want.Message += " because it does not have a replica identity and publishes " + action
		want.Hint = "To enable " + gerund + " the table, set REPLICA IDENTITY using ALTER TABLE."
	}
	var got *sqlstate.Error
	if !errors.As(err, &got) || !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v (%v), want %#v", got, err, want)
	}
}

func TestPublicationIdentityMatrix(t *testing.T) {
	cases := []struct {
		name                              string
		mode                              parser.ReplicaIdentityMode
		noPK, selected, orphan, generated bool
		virtual                           bool
		filter                            int32
		columns                           []int32
		stored                            bool
		reason                            string
	}{
		{name: "default_key_filter", filter: 10},
		{name: "default_nonkey_filter", filter: 20, reason: "filter"},
		{name: "default_missing_key", columns: []int32{20}, reason: "columns"},
		{name: "default_no_pk", noPK: true, reason: "missing"},
		{name: "default_no_pk_filter", noPK: true, filter: 20, reason: "filter"},
		{name: "default_no_pk_list", noPK: true, columns: []int32{20}, reason: "missing"},
		{name: "full_filter", mode: parser.ReplicaIdentityFull, filter: 20},
		{name: "full_all_columns", mode: parser.ReplicaIdentityFull, columns: []int32{10, 20}, reason: "columns"},
		{name: "nothing", mode: parser.ReplicaIdentityNothing, reason: "missing"},
		{name: "nothing_filter_first", mode: parser.ReplicaIdentityNothing, filter: 10, reason: "filter"},
		{name: "selected_keys", mode: parser.ReplicaIdentityIndex, selected: true, filter: 20, columns: []int32{20}},
		{name: "selected_not_pk_or_include", mode: parser.ReplicaIdentityIndex, selected: true, filter: 10, reason: "filter"},
		{name: "selected_list", mode: parser.ReplicaIdentityIndex, selected: true, columns: []int32{10}, reason: "columns"},
		{name: "orphan", mode: parser.ReplicaIdentityIndex, orphan: true, reason: "missing"},
		{name: "orphan_filter", mode: parser.ReplicaIdentityIndex, orphan: true, filter: 20, reason: "filter"},
		{name: "generated_key", generated: true, reason: "generated"},
		{name: "generated_stored", generated: true, stored: true},
		{name: "generated_explicit", generated: true, columns: []int32{10}},
		{name: "filter_before_list_generated", generated: true, filter: 20, columns: []int32{20}, reason: "filter"},
		{name: "list_before_generated", generated: true, columns: []int32{20}, reason: "columns"},
		{name: "full_virtual", mode: parser.ReplicaIdentityFull, virtual: true, reason: "generated"},
		{name: "full_virtual_stored_option", mode: parser.ReplicaIdentityFull, virtual: true, stored: true, reason: "generated"},
		{name: "full_virtual_list_first", mode: parser.ReplicaIdentityFull, virtual: true, columns: []int32{10}, reason: "columns"},
		{name: "ordinary_key_virtual_nonkey", virtual: true},
		{name: "full_unpublished_stored", mode: parser.ReplicaIdentityFull, generated: true, reason: "generated"},
		{name: "full_published_stored", mode: parser.ReplicaIdentityFull, generated: true, stored: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := identityCatalog(t)
			f.r.ReplicaIdentity = tc.mode
			if tc.noPK {
				f.r.Constraints.Unique = nil
			}
			if tc.selected {
				idx := &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 48005}, ObjectRevision: 1, RelationKind: catalog.IndexRelation, SQLIndex: &catalog.IndexDefinition{Parent: f.r.Address(), ReplicaIdentity: true, Keys: []catalog.IndexKey{{Attribute: 20}}, Include: []int32{10}}}
				f.v.State().SetDescriptor(idx.Address(), idx)
			}
			if tc.generated {
				f.r.ColumnMeta.Generations = map[int32]catalog.ColumnGeneration{10: {Kind: catalog.GenerationStored}}
			}
			if tc.virtual {
				f.r.ColumnMeta.Generations = map[int32]catalog.ColumnGeneration{20: {Kind: catalog.GenerationVirtual}}
			}
			if tc.stored {
				f.p.GeneratedColumns = 's'
			}
			if tc.filter != 0 {
				f.filter(tc.filter)
			}
			if tc.columns != nil {
				f.m.ColumnsSpecified = true
				f.m.SetColumns(tc.columns)
			}
			for _, update := range []bool{false, true} {
				for _, flags := range []publication.Actions{{}, {Update: true}, {Delete: true}, {Update: true, Delete: true}} {
					f.p.Update, f.p.Delete = flags.Update, flags.Delete
					reason := tc.reason
					if update && !flags.Update || !update && !flags.Delete {
						reason = ""
					}
					identityWant(t, publication.CheckReplicaIdentity(f.v, f.r, publication.Actions{Update: update, Delete: !update}), update, reason)
				}
			}
		})
	}
}

func TestPublicationIdentityIndependentDefinitions(t *testing.T) {
	f := identityCatalog(t)
	f.r.ReplicaIdentity = parser.ReplicaIdentityNothing
	second := *f.p
	second.ObjectAddress.ObjectID++
	second.ObjectName = "other"
	second.AllTables = true
	f.v.State().SetDescriptor(second.Address(), &second)
	f.p.Update, f.p.Delete = false, false
	identityWant(t, publication.CheckReplicaIdentity(f.v, f.r, publication.Actions{Update: true}), true, "missing")
	// A filter failure in one relevant definition precedes another's missing
	// identity. Membership is read afresh on the next execution.
	f.p.Update = true
	f.filter(20)
	identityWant(t, publication.CheckReplicaIdentity(f.v, f.r, publication.Actions{Update: true}), true, "filter")
	f.p.Update = false
	second.Update = false
	identityWant(t, publication.CheckReplicaIdentity(f.v, f.r, publication.Actions{Update: true}), true, "")
}

func TestPublicationIdentityRootLayout(t *testing.T) {
	f := identityCatalog(t)
	root := *f.r
	root.ObjectAddress.ObjectID = 48010
	root.ObjectName = "identity_root"
	root.RelationKind = catalog.PartitionedRelation
	root.Partition = &catalog.PartitionDefinition{Children: []ids.ObjectAddress{f.r.Address()}}
	root.Attributes = []int32{31, 42}
	root.ReplicaIdentity = parser.ReplicaIdentityFull
	f.r.Columns = []catalog.Column{root.Columns[1], root.Columns[0]}
	f.r.Attributes = []int32{20, 10}
	f.r.Membership = &catalog.PartitionMembership{Parent: root.Address()}
	f.v.State().SetDescriptor(root.Address(), &root)
	f.m.Relation = root.Address()
	f.p.ViaRoot = true
	ref := f.v.Owner().Domain.Ref(f.v.Epoch(), root.Address())
	ref.Address.SubID = 42
	f.m.Filter = stored.NewPolicy(nil, plan.ColumnExpr{Origin: ref}, nil)
	identityWant(t, publication.CheckReplicaIdentity(f.v, f.r, publication.Actions{Update: true}), true, "filter")
	identityWant(t, publication.CheckReplicaIdentity(f.v, f.r, publication.Actions{Delete: true}), false, "filter")
	f.r.ReplicaIdentity = parser.ReplicaIdentityFull
	identityWant(t, publication.CheckReplicaIdentity(f.v, f.r, publication.Actions{Update: true}), true, "")
	identityWant(t, publication.CheckReplicaIdentity(f.v, f.r, publication.Actions{Delete: true}), false, "")
	identityWant(t, core.CheckPublicationAdmission(f.v, &root, &parser.Delete{}), false, "")
	f.m.Filter = nil
	f.r.ReplicaIdentity = parser.ReplicaIdentityDefault
	f.m.ColumnsSpecified = true
	f.m.Columns = []int32{31}
	identityWant(t, core.CheckPublicationAdmission(f.v, &root, &parser.Update{}), true, "")
	f.m.Columns = []int32{42}
	identityWant(t, core.CheckPublicationAdmission(f.v, &root, &parser.Update{}), true, "columns")
}

func TestPublicationDeclaredActionAdmission(t *testing.T) {
	f := identityCatalog(t)
	f.r.ReplicaIdentity = parser.ReplicaIdentityNothing
	for _, tc := range []struct {
		stmt   parser.Statement
		update bool
		reason string
	}{
		{&parser.Update{}, true, "missing"}, {&parser.Delete{}, false, "missing"},
		{&parser.Insert{}, true, ""}, {&parser.Insert{Conflict: &parser.InsertConflict{}}, true, ""},
		{&parser.Insert{Conflict: &parser.InsertConflict{Update: true}}, true, "missing"},
		{&parser.Merge{Arms: []parser.MergeArm{{Action: parser.MergeNothingAction}, {Action: parser.MergeUpdateAction}}}, true, "missing"},
		{&parser.Merge{Arms: []parser.MergeArm{{Action: parser.MergeDeleteAction}}}, false, "missing"},
		{&parser.Merge{Arms: []parser.MergeArm{{Action: parser.MergeInsertAction}}}, true, ""},
	} {
		identityWant(t, core.CheckPublicationAdmission(f.v, f.r, tc.stmt), tc.update, tc.reason)
	}
}

func TestPublicationIdentityConstantFilter(t *testing.T) {
	for _, mode := range []parser.ReplicaIdentityMode{parser.ReplicaIdentityDefault, parser.ReplicaIdentityNothing, parser.ReplicaIdentityIndex} {
		f := identityCatalog(t)
		f.r.ReplicaIdentity = mode
		f.r.Constraints.Unique = nil
		boolean, _ := f.v.LookupType(sqltype.Bool)
		f.m.Filter = stored.NewPolicy(nil, plan.LiteralExpr{Info: expr.Info{Usage: boolean.Usage()}, Value: sqltype.Boolean(boolean, true)}, nil)
		f.m.ColumnsSpecified = true
		f.m.Columns = []int32{20}
		for _, update := range []bool{false, true} {
			identityWant(t, publication.CheckReplicaIdentity(f.v, f.r, publication.Actions{Update: update, Delete: !update}), update, "missing")
		}
	}
}

func TestPublicationIdentityVirtualFilter(t *testing.T) {
	for _, sub := range []int32{10, 20} {
		for _, nested := range []bool{false, true} {
			f := identityCatalog(t)
			usage := f.r.Columns[0].Usage
			f.r.Columns = append(f.r.Columns, catalog.Column{Name: "virtual_g", Usage: usage})
			f.r.Attributes = append(f.r.Attributes, 30)
			f.r.Index["virtual_g"] = 2
			ref := f.v.Owner().Domain.Ref(f.v.Epoch(), f.r.Address())
			ref.Address.SubID = sub
			recipe := plan.ColumnExpr{Info: expr.Info{Usage: usage}, Origin: ref}
			virtualRef := ref
			virtualRef.Address.SubID = 30
			wrapper := plan.ColumnExpr{
				Info: expr.Info{Usage: usage}, Origin: virtualRef,
				Virtual: recipe, VirtualOwner: f.r, VirtualAttribute: 30,
			}
			f.r.ColumnMeta.Generations = map[int32]catalog.ColumnGeneration{
				30: {Kind: catalog.GenerationVirtual, Expression: recipe},
			}
			if nested {
				inner := wrapper
				wrapper.Virtual = inner
			}
			f.m.Filter = stored.NewPolicy(nil, wrapper, nil)
			reason := ""
			if sub == 20 {
				reason = "filter"
			}
			for _, update := range []bool{false, true} {
				identityWant(t, publication.CheckReplicaIdentity(f.v, f.r, publication.Actions{Update: update, Delete: !update}), update, reason)
			}
		}
	}
}
