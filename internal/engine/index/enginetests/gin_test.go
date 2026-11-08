package enginetests_test

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/sqltype"
)

func ginInput(t *testing.T, d *sqltype.Descriptor, text string) sqltype.Value {
	t.Helper()
	v, err := d.Input(context.Background(), text, -1)
	if err != nil {
		t.Fatal(err)
	}
	return v
}
func ginClass(t *testing.T, view catalog.Read, oid ids.OID) sqltype.IndexClass {
	t.Helper()
	c, ok := view.IndexClass(oid)
	if !ok {
		t.Fatal("missing GIN class", oid)
	}
	c, err := routine.BindClassSupport(view, c)
	if err != nil {
		t.Fatal(err)
	}
	return c
}
func ginBinding(t *testing.T, typ *sqltype.Descriptor, class sqltype.IndexClass) sqltype.GinBinding {
	t.Helper()
	b, err := sqltype.BindGinTokens(typ.Usage(), class)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
func ginLookup(t *testing.T, column storage.GinColumn, token sqltype.GinToken) []storage.GinPosting {
	t.Helper()
	p, err := column.Lookup(context.Background(), token, sqltype.InputEnvironment{})
	if err != nil {
		t.Fatal(err)
	}
	return p
}
func ginRows(postings []storage.GinPosting) []ids.RowID {
	var out []ids.RowID
	for _, p := range postings {
		out = append(out, p.Row)
	}
	return out
}
func ginWantRows(t *testing.T, got []storage.GinPosting, want ...ids.RowID) {
	t.Helper()
	if !slices.Equal(ginRows(got), want) {
		t.Fatal("exact posting rows", got, want)
	}
}

type ginFixture struct {
	view               catalog.Read
	parent, descriptor *catalog.Relation
	store              *storage.Relation
	evaluation         *exec.Context
	root               *storage.IndexRoot
}

func newGinFixture(t *testing.T) ginFixture {
	t.Helper()
	view := engine.New().CatalogRead()
	array, _ := view.LookupType(1007)
	vector, _ := view.LookupType(sqltype.TSVector)
	parent := &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 90000}, Attributes: []int32{1, 2}, Columns: []catalog.Column{{Name: "a", Usage: array.Usage()}, {Name: "v", Usage: vector.Usage()}}}
	d := &catalog.IndexDefinition{Method: catalog.GinMethod, MethodOID: 2742, Parent: parent.Address(), Keys: []catalog.IndexKey{{Attribute: 1, Class: ginClass(t, view, 10064)}, {Attribute: 2, Class: ginClass(t, view, 10073)}}, Ready: true, Valid: true, Live: true}
	descriptor := &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 90001}, SQLIndex: d}
	store := &storage.Relation{}
	for i, pair := range [][2]string{{"{1,2,2}", "cat:1A rat:2"}, {"{2,3}", "cat:1 rat:3A"}, {"{}", ""}, {"{NULL,2}", "catalog:1 rat:2"}, {"[0:1]={1,2}", "cat rat"}, {"{1,2}", "dog:1"}, {"{1,2}", "dog:1"}} {
		store.Versions = append(store.Versions, storage.RowVersion{Row: ids.RowID(i + 1), Version: ids.VersionID(i + 1), Born: 1, Values: []sqltype.Value{ginInput(t, array, pair[0]), ginInput(t, vector, pair[1])}})
	}
	store.Versions = append(store.Versions, storage.RowVersion{Row: 8, Version: 8, Born: 1, Values: []sqltype.Value{sqltype.Null(array), sqltype.Null(vector)}})
	evaluation := &exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background(), Catalog: view}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}}
	root, err := index.BuildIndexRoot(evaluation, parent, d, store, nil)
	if err != nil {
		t.Fatal(err)
	}
	return ginFixture{view, parent, descriptor, store, evaluation, root}
}

func TestGINClassInventory(t *testing.T) {
	view := engine.New().CatalogRead()
	method, ok := view.AccessMethod(2742)
	if !ok || method.Handler != 333 || method.Native != catalog.GinAccessMethod {
		t.Fatal("real AM identity", method)
	}
	handler, ok := view.Procedure(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 333})
	if !ok || handler.AccessMethod != catalog.GinAccessMethod || handler.Signature != "2281" || handler.Result != 325 {
		t.Fatal("genuine handler", handler)
	}
	for _, fact := range []struct {
		oid, family, input, key ids.OID
		slots                   map[int]ids.OID
	}{
		{10064, 2745, 2277, 2283, map[int]ids.OID{2: 2743, 3: 2774, 4: 2744, 6: 3920}},
		{10073, 3659, 3614, 25, map[int]ids.OID{1: 3724, 2: 3656, 3: 3657, 4: 3658, 5: 2700, 6: 3921}},
		{10090, 4036, 3802, 25, map[int]ids.OID{1: 3480, 2: 3482, 3: 3483, 4: 3484, 6: 3488}},
		{10091, 4037, 3802, 23, map[int]ids.OID{1: 351, 2: 3485, 3: 3486, 4: 3487, 6: 3489}},
	} {
		c := ginClass(t, view, fact.oid)
		if !c.IsGin() || c.IsHash() || c.IsGiST() || c.FamilyOID() != uint32(fact.family) || c.InputOID() != uint32(fact.input) || c.StorageOID() != uint32(fact.key) {
			t.Fatal("released class", c)
		}
		for slot := 1; slot <= 6; slot++ {
			if c.Support(slot).Routine != uint32(fact.slots[slot]) {
				t.Fatal("complete support", fact, c.Support(slot))
			}
		}
		state := catalog.CloneState(view.State())
		member, _ := view.FamilySupport(fact.family, fact.input, fact.input, 2)
		state.RemoveDescriptor(member.Address())
		if _, err := routine.BindClassSupport(catalog.NewRead(view.Owner(), state), c); err == nil {
			t.Fatal("missing support accepted")
		}
		state = catalog.CloneState(view.State())
		proc, _ := view.Procedure(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: member.Function})
		changed := *proc
		changed.ObjectRevision = proc.Revision() + 1
		state.SetDescriptor(changed.Address(), &changed)
		if _, err := routine.BindClassSupport(catalog.NewRead(view.Owner(), state), c); err == nil {
			t.Fatal("stale support accepted")
		}
		state = catalog.CloneState(view.State())
		foreignMember := *member
		foreignMember.Function = 333
		foreignMember.ObjectRevision = member.Revision() + 1
		state.SetDescriptor(foreignMember.Address(), &foreignMember)
		if _, err := routine.BindClassSupport(catalog.NewRead(view.Owner(), state), c); err == nil {
			t.Fatal("foreign support role accepted")
		}
		state = catalog.CloneState(view.State())
		number, rightType := int16(1), fact.input
		if profile, ok := routine.LookupGinProfile(fact.family); ok {
			number, rightType = int16(profile.Strategies[0].Number), profile.Strategies[0].Right
		}
		strategy, _ := view.FamilyOperator(fact.family, fact.input, rightType, number, 's')
		state.RemoveDescriptor(strategy.Address())
		if _, err := routine.BindClassSupport(catalog.NewRead(view.Owner(), state), c); err == nil {
			t.Fatal("missing search strategy accepted")
		}
		bad := sqltype.NewIndexClass(sqltype.IndexClassDefinition{OID: uint32(fact.oid), Method: 2742, Family: 424, Input: uint32(fact.input), Storage: uint32(fact.key), Name: c.Name, FamilyName: c.Family, Gin: true})
		if _, err := routine.BindClassSupport(view, bad); err == nil {
			t.Fatal("class name authorized foreign family")
		}
	}
	array, _ := view.LookupType(1007)
	domain, err := sqltype.Domain(90100, "gin_domain", "gin_domain", array.Usage())
	if err != nil {
		t.Fatal(err)
	}
	for _, typ := range []*sqltype.Descriptor{array, domain} {
		c, ok, err := view.DefaultIndexClass(2742, typ)
		metadata, unique := view.UniqueDefaultIndexClass(2742, typ)
		if err != nil || !ok || !unique || c.OID() != 10064 || !c.SameIdentity(metadata) {
			t.Fatal("concrete anyarray default", typ, c, err)
		}
		if _, err := routine.BindIndexOrdering(view, typ.Usage(), c); err == nil {
			t.Fatal("GIN borrowed B-tree ordering")
		}
	}
}

func TestGINTypedTokens(t *testing.T) {
	view := engine.New().CatalogRead()
	class := ginClass(t, view, 10064)
	enum, err := sqltype.Enum(90200, "gin_enum", "gin_enum", []string{"z", "a"})
	if err != nil {
		t.Fatal(err)
	}
	domain, err := sqltype.Domain(90201, "gin_element", "gin_element", enum.Usage())
	if err != nil {
		t.Fatal(err)
	}
	enumArray, err := sqltype.Array(90202, "gin_enums", domain)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		typ      *sqltype.Descriptor
		text     string
		ordinary int
	}{{aclUnitType(t, engine.New(), 1231), "{1.0,1.00,2}", 2}, {aclUnitType(t, engine.New(), 1014), "{\"a\",\"a  \",b}", 2}, {aclUnitType(t, engine.New(), 1009), "{cat,cat,CAT,NULL}", 2}, {enumArray, "{z,a,z,NULL}", 2}} {
		binding := ginBinding(t, tc.typ, class)
		value := ginInput(t, tc.typ, tc.text)
		root, err := storage.BuildGin(context.Background(), []sqltype.GinBinding{binding}, []storage.IndexEntry{{Keys: []sqltype.Value{value}, Row: 1, Version: 1}}, sqltype.InputEnvironment{})
		if err != nil {
			t.Fatal(err)
		}
		ordinary := 0
		for _, list := range root.Columns[0].Tokens {
			if list.Token.Kind == sqltype.GinOrdinary {
				ordinary++
				if list.Token.Element.Type() != tc.typ.Element() {
					t.Fatal("lost concrete element generation")
				}
			}
			ginWantRows(t, list.Postings, 1)
		}
		if ordinary != tc.ordinary {
			t.Fatal("typed equality did not deduplicate", tc.text, ordinary)
		}
	}
	f := newGinFixture(t)
	vector := f.parent.Columns[1].Usage.Type
	value := ginInput(t, vector, "rat:2 cat:1A")
	projection, err := sqltype.TSVectorLexemes(value, vector)
	if err != nil || projection.Count() != 2 || projection.At(0) != "cat" || projection.At(1) != "rat" {
		t.Fatal("canonical lexeme view", projection, err)
	}
	first, err := sqltype.Domain(90300, "gin_foreign", "gin_foreign", vector.Usage())
	if err != nil {
		t.Fatal(err)
	}
	foreign, err := sqltype.Domain(90300, "gin_foreign", "gin_foreign", vector.Usage())
	if err != nil {
		t.Fatal(err)
	}
	wrapped, err := first.Wrap(value)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sqltype.TSVectorLexemes(wrapped, foreign); err == nil {
		t.Fatal("foreign same-OID vector generation accepted")
	}
	query, _ := f.view.LookupType(sqltype.TSQuery)
	if _, err := sqltype.TSVectorLexemes(ginInput(t, query, "cat"), query); err == nil {
		t.Fatal("wrong FTS kind accepted")
	}
	if _, err := sqltype.TSVectorLexemes(sqltype.Null(vector), vector); err == nil {
		t.Fatal("SQL NULL treated as empty vector")
	}
	array := f.parent.Columns[0].Usage.Type
	token := sqltype.GinToken{Element: ginInput(t, array, "{2}").ArrayElement(0)}
	if _, err := f.root.Gin.Columns[1].Lookup(context.Background(), token, sqltype.InputEnvironment{}); err == nil {
		t.Fatal("array token accepted by vector dictionary")
	}
	repeated, err := storage.BuildGin(context.Background(), []sqltype.GinBinding{f.root.Gin.Columns[0].Binding, f.root.Gin.Columns[0].Binding}, []storage.IndexEntry{{Row: 1, Version: 1, Keys: []sqltype.Value{ginInput(t, array, "{1}"), ginInput(t, array, "{2}")}}}, sqltype.InputEnvironment{})
	if err != nil {
		t.Fatal(err)
	}
	one := sqltype.GinToken{Element: ginInput(t, array, "{1}").ArrayElement(0)}
	ginWantRows(t, ginLookup(t, repeated.Columns[0], one), 1)
	ginWantRows(t, ginLookup(t, repeated.Columns[1], one))
}

func TestGINInvertedRoot(t *testing.T) {
	f := newGinFixture(t)
	root := f.root
	if root.Gin == nil || root.Hash != nil || root.Brin != nil || root.Bloom != nil || len(root.Entries) != 8 || len(root.Gin.Columns) != 2 {
		t.Fatal("not substantive inverted root", root)
	}
	array := f.parent.Columns[0].Usage.Type
	token := func(n string) sqltype.GinToken {
		return sqltype.GinToken{Element: ginInput(t, array, "{"+n+"}").ArrayElement(0)}
	}
	ginWantRows(t, ginLookup(t, root.Gin.Columns[0], token("2")), 1, 2, 4, 5, 6, 7)
	ginWantRows(t, ginLookup(t, root.Gin.Columns[0], sqltype.GinToken{Kind: sqltype.GinNullElement}), 4)
	ginWantRows(t, ginLookup(t, root.Gin.Columns[0], sqltype.GinToken{Kind: sqltype.GinEmptyItem}), 3)
	ginWantRows(t, ginLookup(t, root.Gin.Columns[0], sqltype.GinToken{Kind: sqltype.GinNullItem}), 8)
	ginWantRows(t, ginLookup(t, root.Gin.Columns[1], sqltype.GinToken{Lexeme: "cat"}), 1, 2, 5)
	ginWantRows(t, ginLookup(t, root.Gin.Columns[1], sqltype.GinToken{Lexeme: "rat"}), 1, 2, 4, 5)
	prefix, err := index.GinLexemeCandidates(context.Background(), root.Gin, 1, "cat", true, false, sqltype.InputEnvironment{})
	if err != nil {
		t.Fatal(err)
	}
	ginWantRows(t, prefix, 1, 2, 4, 5)
	for _, tc := range []struct {
		query    string
		strategy int
		rows     []ids.RowID
	}{{"{1,2}", 2, []ids.RowID{1, 5, 6, 7}}, {"{NULL}", 2, nil}, {"{}", 2, []ids.RowID{1, 2, 3, 4, 5, 6, 7}}, {"{2}", 1, []ids.RowID{1, 2, 4, 5, 6, 7}}} {
		got, err := index.GinArrayCandidates(context.Background(), root.Gin, 0, ginInput(t, array, tc.query), tc.strategy, sqltype.InputEnvironment{})
		if err != nil {
			t.Fatal(err)
		}
		ginWantRows(t, got, tc.rows...)
	}
	for _, column := range root.Gin.Columns {
		for _, list := range column.Tokens {
			for _, p := range list.Postings {
				row := f.store.Versions[p.Position]
				if row.Row != p.Row || row.Version != p.Version {
					t.Fatal("posting not owned by heap", p)
				}
			}
		}
	}
	empty, err := index.BuildIndexRoot(f.evaluation, f.parent, f.descriptor.SQLIndex, &storage.Relation{}, nil)
	if err != nil || empty.Gin == nil || len(empty.Gin.Columns) != 2 || len(empty.Gin.Columns[0].Tokens) != 0 {
		t.Fatal("unbound empty root", empty, err)
	}
}

func TestGINCancellation(t *testing.T) {
	f := newGinFixture(t)
	before := ginCopyPayload(f.root.Gin)
	extractContext, cancelExtract := context.WithCancel(context.Background())
	emitted := 0
	err := f.root.Gin.Columns[0].Binding.Extract(extractContext, f.root.Entries[0].Keys[0], func(sqltype.GinToken) error { emitted++; cancelExtract(); return nil })
	cancelExtract()
	if emitted != 1 || !errors.Is(err, context.Canceled) {
		t.Fatal("admitted typed extraction was not interrupted", emitted, err)
	}
	mergeContext := &spgistInterruptContext{Context: context.Background(), remaining: 8}
	interrupted, err := storage.MergeGin(mergeContext, f.root.Gin, nil, nil, map[ids.VersionID]ids.RowID{1: 1}, sqltype.InputEnvironment{})
	if interrupted != nil || !errors.Is(err, context.Canceled) {
		t.Fatal("posting-only merge was not interrupted", interrupted, err)
	}
	// Counter admission is deterministic and interrupts extraction, sorting and
	// dictionary merge, including NULL/duplicate loops, without sleeps or races.
	for _, polls := range []int{1, 8, 40, 90} {
		ctx := &spgistInterruptContext{Context: context.Background(), remaining: polls}
		eval := *f.evaluation
		eval.Context = ctx
		root, err := index.BuildIndexRoot(&eval, f.parent, f.descriptor.SQLIndex, f.store, nil)
		if root != nil || !errors.Is(err, context.Canceled) {
			t.Fatal("canceled build became installable", polls, root, err)
		}
	}
	added := []storage.IndexEntry{{Row: 20, Version: 20, Position: 8, Keys: f.root.Entries[0].Keys}}
	for _, polls := range []int{1, 10, 40} {
		ctx := &spgistInterruptContext{Context: context.Background(), remaining: polls}
		root, err := storage.MergeGin(ctx, f.root.Gin, added, nil, map[ids.VersionID]ids.RowID{1: 1}, sqltype.InputEnvironment{})
		if root != nil || !errors.Is(err, context.Canceled) {
			t.Fatal("canceled delta became installable", polls, root, err)
		}
	}
	calls := 0
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	root, err := storage.TransportGin(ctx, f.root.Gin, func(p storage.GinPosting) (storage.GinPosting, bool, error) { calls++; cancel(); return p, true, nil })
	if root != nil || !errors.Is(err, context.Canceled) || calls != 1 {
		t.Fatal("admitted transport cancellation", root, calls, err)
	}
	if !reflect.DeepEqual(before, f.root.Gin) {
		t.Fatal("cancellation mutated retained postings")
	}
}

func ginCopyPayload(root *storage.GinRoot) *storage.GinRoot {
	copy := *root
	copy.Columns = slices.Clone(root.Columns)
	copy.Pending = slices.Clone(root.Pending)
	for _, columns := range [][]storage.GinColumn{copy.Columns, copy.Pending} {
		for i := range columns {
			columns[i].NonNull = slices.Clone(columns[i].NonNull)
			columns[i].Tokens = slices.Clone(columns[i].Tokens)
			for j := range columns[i].Tokens {
				columns[i].Tokens[j].Postings = slices.Clone(columns[i].Tokens[j].Postings)
			}
		}
	}
	return &copy
}
