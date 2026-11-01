package core_test

import (
	"context"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/sqltype"
)

func bloomRoot(t *testing.T, e *core.Engine, name string) (*catalog.Relation, *storage.IndexRoot) {
	t.Helper()
	d, root := btreeRoot(t, e, name)
	if d.SQLIndex.Method != catalog.BloomMethod || root.Bloom == nil || root.Hash != nil || root.Brin != nil || len(root.Entries) != len(root.Bloom.Entries) {
		t.Fatal("not a real bloom root", root)
	}
	return d, root
}

func TestBloomProviderIdentity(t *testing.T) {
	e := engine.New()
	if _, ok := e.CatalogRead().LookupAccessMethod("bloom"); ok {
		t.Fatal("bloom installed before CREATE EXTENSION")
	}
	enginetest.Run(t, e, "CREATE EXTENSION bloom")
	v := e.CatalogRead()
	method, ok := v.LookupAccessMethod("bloom")
	if !ok || method.Native != catalog.BloomAccessMethod || method.MethodKind() != "i" || method.Namespace() != (ids.ObjectAddress{}) || catalog.NativeHandler(method.Handler) != catalog.NoAccessMethod {
		t.Fatal("dynamic SQL identity impersonated builtin authority", method)
	}
	p, ok := v.Procedure(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: method.Handler})
	if !ok || p.Signature != "2281" || p.Result != 325 || p.Strict || p.Installed.Parallel != 'u' || p.Implementation != extension.StockBloom {
		t.Fatal("released handler declaration", p)
	}
	if err := (catalog.AccessMethodProvider{}).Validate(v, method); err != nil {
		t.Fatal(err)
	}
	i := method.Origin.Installation
	if len(i.Members) != 6 || i.Members[0] != p.Address() || i.Members[1] != method.Address() || i.Provider != "bloom" || i.Version != "1.0" {
		t.Fatal("six-member recipe", i)
	}
	for _, a := range i.Members {
		d := v.Get(a)
		if !catalog.SameInstallation(d, i) || catalog.ExtensionMembership(d) != i.Cohort {
			t.Fatal("lost reciprocal membership or release provenance", a)
		}
	}
	enginetest.Run(t, e, "CREATE ACCESS METHOD bloom_alias TYPE INDEX HANDLER public.blhandler")
	alias, ok := e.CatalogRead().LookupAccessMethod("bloom_alias")
	if !ok || alias.Handler != method.Handler || alias.Address() == method.Address() || alias.Origin != nil || alias.Extension != (ids.ObjectAddress{}) {
		t.Fatal("alias acquired installation ownership", alias)
	}
	if err := (catalog.AccessMethodProvider{}).Validate(e.CatalogRead(), alias); err != nil {
		t.Fatal(err)
	}
	for _, sql := range []string{
		"CREATE OPERATOR CLASS public.bloom_alias_int4 DEFAULT FOR TYPE integer USING bloom_alias AS OPERATOR 1 pg_catalog.=, FUNCTION 1 pg_catalog.hashint4(integer)",
		"CREATE OPERATOR CLASS public.bloom_alias_text DEFAULT FOR TYPE text USING bloom_alias AS OPERATOR 1 pg_catalog.=, FUNCTION 1 pg_catalog.hashtext(text)",
		"CREATE TABLE bloom_alias_rows(i integer,s text)",
		"INSERT INTO bloom_alias_rows VALUES(7,'a'),(7,'a'),(NULL,NULL)",
		"CREATE INDEX bloom_alias_idx ON bloom_alias_rows USING bloom_alias(i,s) WITH(length=1,col1=1,col2=1)",
	} {
		enginetest.Run(t, e, sql)
	}
	aliasIndex, aliasRoot := bloomRoot(t, e, "bloom_alias_idx")
	if aliasIndex.SQLIndex.MethodIdentity() != alias.Address().ObjectID || len(aliasRoot.Entries) != 3 {
		t.Fatal("alias did not build genuine signatures under its own SQL identity")
	}
	current := e.CatalogRead()
	for _, binding := range aliasRoot.Bloom.Bindings {
		class, _ := current.OperatorClass(ids.OID(binding.Class.OID()))
		if class.Origin != nil || class.Method != alias.Address().ObjectID {
			t.Fatal("ordinary alias class acquired stock origin or method", class)
		}
		support, _ := current.FamilySupport(class.Family, class.InputType, class.InputType, 1)
		bad := *support
		bad.Function = 400
		if class.InputType == 25 { bad.Function = 450 }
		state := catalog.CloneState(current.State())
		state.SetDescriptor(bad.Address(), &bad)
		if _, err := routine.BindClassSupport(current.WithState(state), binding.Class); err == nil {
			t.Fatal("alias accepted wrong type-specific support")
		}
	}
	query, source, release := bloomCapture(t, e, "SELECT i,s FROM bloom_alias_rows WHERE i=7 AND s='a'")
	stage := hashFilterStage(t, query, source, nil)
	if stage.HashCandidates == nil || stage.HashCandidates.Bloom != aliasRoot.Bloom {
		t.Fatal("genuine alias did not consume its production root")
	}
	if err := stage.Drain(context.Background()); err != nil || stage.Count() != 2 {
		t.Fatal("alias candidate recheck failed", stage.Count(), err)
	}
	stage.Close()
	release()
	forgedAlias := *alias
	forgedAlias.Handler = 331
	state := catalog.CloneState(current.State())
	state.SetDescriptor(forgedAlias.Address(), &forgedAlias)
	if err := (catalog.AccessMethodProvider{}).Validate(current.WithState(state), &forgedAlias); err == nil {
		t.Fatal("alias spelling granted a foreign handler capability")
	}
	for _, mutate := range []func(*catalog.AccessMethod){
		func(m *catalog.AccessMethod) { m.Native = catalog.HashAccessMethod },
		func(m *catalog.AccessMethod) { m.Handler = 331 },
		func(m *catalog.AccessMethod) { copy := *m.Origin.Installation; m.Origin = &catalog.InstalledOrigin{Installation: &copy, Declaration: 1} },
	} {
		copy := *method
		mutate(&copy)
		state := catalog.CloneState(v.State())
		state.SetDescriptor(copy.Address(), &copy)
		if err := (catalog.AccessMethodProvider{}).Validate(v.WithState(state), &copy); err == nil {
			t.Fatal("forged access-method capability admitted", copy)
		}
	}
	for _, mutate := range []func(*catalog.Procedure){
		func(p *catalog.Procedure) { p.AccessMethod = catalog.HashAccessMethod },
		func(p *catalog.Procedure) { p.Implementation = nil },
		func(p *catalog.Procedure) { p.Implementation = &extension.BloomProvider{} },
		func(p *catalog.Procedure) { p.Result = 23 },
		func(p *catalog.Procedure) { p.Signature = "23" },
		func(p *catalog.Procedure) { p.Strict = true },
		func(p *catalog.Procedure) { p.Display.Binary = "$libdir/foreign" },
	} {
		copy := *p
		mutate(&copy)
		state := catalog.CloneState(v.State())
		state.SetDescriptor(copy.Address(), &copy)
		if err := (stored.ProcedureProvider{}).Validate(v.WithState(state), &copy); err == nil {
			t.Fatal("forged installed handler admitted", copy)
		}
	}
}

func TestBloomInstalledOrigin(t *testing.T) {
	e := engine.New()
	enginetest.Run(t, e, "CREATE EXTENSION bloom")
	method, _ := e.CatalogRead().LookupAccessMethod("bloom")
	i := method.Origin.Installation
	enginetest.Run(t, e, "ALTER EXTENSION bloom DROP ACCESS METHOD bloom")
	enginetest.Run(t, e, "ALTER EXTENSION bloom DROP FUNCTION blhandler(internal)")
	live, _ := e.CatalogRead().LookupAccessMethod("bloom")
	if live.Extension != (ids.ObjectAddress{}) || live.Origin != method.Origin {
		t.Fatal("detachment erased or manufactured release authority")
	}
	if err := (catalog.AccessMethodProvider{}).Validate(e.CatalogRead(), live); err != nil {
		t.Fatal("current membership selected native capability", err)
	}
	mapping := make(map[ids.ObjectAddress]ids.ObjectAddress)
	for _, a := range append(append([]ids.ObjectAddress(nil), i.Members...), i.Cohort) {
		next := a
		next.ObjectID += 100000
		mapping[a] = next
	}
	remap := extension.DescriptorRemapper(mapping)
	mapped := remap(live).(*catalog.AccessMethod)
	if mapped == live || !mapped.Origin.Valid(mapped.Address()) || mapped.Handler != mapping[i.Members[0]].ObjectID || mapped.Origin.Installation == i || mapped.Origin.Installation.Declarations[1].(*catalog.AccessMethod).Handler != mapped.Handler {
		t.Fatal("AM or canonical handler identity not remapped", mapped)
	}
	if !i.Valid() || i.Members[1] != method.Address() || i.Declarations[1].(*catalog.AccessMethod).Handler != method.Handler {
		t.Fatal("remapping mutated source recipe")
	}
	enginetest.Run(t, e, "ALTER EXTENSION bloom ADD ACCESS METHOD bloom")
	enginetest.Run(t, e, "ALTER EXTENSION bloom ADD FUNCTION blhandler(internal)")
	if err := extension.StockBloom.ValidateExtension(e.CatalogRead(), e.CatalogRead().Get(i.Cohort).(*catalog.Extension)); err != nil {
		t.Fatal(err)
	}
}

func TestBloomHashSupport(t *testing.T) {
	e := engine.New()
	for _, sql := range []string{"CREATE EXTENSION bloom", "CREATE TABLE bloom_hash(i integer,s text)", "CREATE INDEX bloom_hash_idx ON bloom_hash USING bloom(i,s)", "INSERT INTO bloom_hash VALUES(7,'seven'),(NULL,NULL)"} {
		enginetest.Run(t, e, sql)
	}
	d, root := bloomRoot(t, e, "bloom_hash_idx")
	parent := e.CatalogRead().MustRelation(d.SQLIndex.Parent)
	v := e.CatalogRead()
	for j, want := range []uint32{450, 400} {
		b := root.Bloom.Bindings[j]
		if b.Class.MethodOID() != uint32(d.SQLIndex.MethodIdentity()) || b.Class.IsHash() || !b.Class.IsBloom() || b.Class.Support(1).Routine != want || b.Class.Support(2).Routine != 0 {
			t.Fatal("bloom borrowed hash AM identity/slot policy", b.Class)
		}
		// The same genuine native support bound through the existing hash AM
		// supplies an independent type-specific datum hash witness.
		c, ok, err := v.DefaultIndexClass(405, b.Usage.Type)
		if err != nil || !ok { t.Fatal(c, ok, err) }
		c, err = routine.BindClassSupport(v, c)
		if err != nil { t.Fatal(err) }
		native, err := sqltype.BindIndexHash(b.Usage, c)
		if err != nil { t.Fatal(err) }
		value := root.Entries[0].Keys[j]
		got, err := b.Provider.Hash(context.Background(), value)
		if err != nil { t.Fatal(err) }
		wantHash, err := native.Hash(context.Background(), value)
		if err != nil || got != wantHash { t.Fatal("wrong registered typed hash", got, wantHash, err) }
		member, _ := v.FamilySupport(ids.OID(b.Class.FamilyOID()), ids.OID(b.Class.InputOID()), ids.OID(b.Class.InputOID()), 1)
		bad := *member
		bad.Function = 450
		if j == 0 { bad.Function = 400 }
		state := catalog.CloneState(v.State())
		state.SetDescriptor(bad.Address(), &bad)
		for _, rows := range [][]storage.RowVersion{nil, {e.Storage.Relations[parent.Address()].Versions[1]}} {
			store := &storage.Relation{Versions: rows}
			evaluation := &exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background(), Catalog: v.WithState(state)}}
			if built, err := index.BuildBloomIndexRoot(evaluation, parent, d, store, true, nil); err == nil || built != nil {
				t.Fatal("empty/all-NULL build skipped support validation", built, err)
			}
		}
	}
	if reflect.DeepEqual(root.Bloom.Bindings[0].Class.Support(1), root.Bloom.Bindings[1].Class.Support(1)) {
		t.Fatal("type-specific support identities collapsed")
	}
}
