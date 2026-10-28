package tablecmds_test

import (
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/tablecmds"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func TestInheritanceColumnStorageCandidate(t *testing.T) {
	for _, reverse := range []bool{false, true} {
		t.Run(map[bool]string{false: "forward", true: "reverse"}[reverse], func(t *testing.T) {
			e := engine.New()
			for _, query := range []string{
				`CREATE TABLE policy_p1(gone integer,t text,n numeric,i integer)`,
				`ALTER TABLE policy_p1 DROP COLUMN gone`,
				`CREATE TABLE policy_p2(t text,n numeric,i integer)`,
				`ALTER TABLE ONLY policy_p1 ALTER t SET STORAGE EXTERNAL, ALTER t SET COMPRESSION pglz`,
				`ALTER TABLE ONLY policy_p2 ALTER t SET STORAGE EXTERNAL`,
			} { enginetest.Run(t, e, query) }
			p1ref, p2ref := enginetest.RoleTableRef(t, e, "policy_p1"), enginetest.RoleTableRef(t, e, "policy_p2")
			view := e.CatalogRead()
			p1, p2 := view.MustRelation(p1ref.Address), view.MustRelation(p2ref.Address)
			p1store, p2store := e.Storage.Relations[p1ref.Address], e.Storage.Relations[p2ref.Address]
			p1revision, p2revision := p1.Revision(), p2.Revision()
			parents := "policy_p1,policy_p2"
			if reverse { parents = "policy_p2,policy_p1" }
			enginetest.Run(t, e, "CREATE TABLE policy_child(t text,n numeric,i integer) INHERITS("+parents+")")
			childref := enginetest.RoleTableRef(t, e, "policy_child")
			child := e.CatalogRead().MustRelation(childref.Address)
			if !reflect.DeepEqual(child.Attributes, []int32{1, 2, 3}) || len(child.ColumnMeta.Storage) != 1 || child.ColumnMeta.Storage[1] != (catalog.ColumnStorage{Storage: "e", Compression: "p"}) {
				t.Fatal("merge did not normalize destination stable attributes", child.Attributes, child.ColumnMeta.Storage)
			}
			for slot, attr := range child.Attributes {
				provenance := child.Inheritance.Columns[attr]
				if !provenance.Local || len(provenance.Origins) != 2 { t.Fatal("local redeclaration lost parent origins", provenance) }
				for _, origin := range provenance.Origins {
					want := p1.Attributes[slot]
					if origin.Parent == p2.Address() { want = p2.Attributes[slot] }
					if origin.Attribute != want { t.Fatal("parent physical gap leaked into destination mapping", origin, want) }
				}
			}
			// CREATE legitimately publishes the parents' first-child markers.
			publishedP1, publishedP2 := e.CatalogRead().MustRelation(p1ref.Address), e.CatalogRead().MustRelation(p2ref.Address)
			publishedRevision1, publishedRevision2 := publishedP1.Revision(), publishedP2.Revision()
			enginetest.Run(t, e, `ALTER TABLE ONLY policy_child ALTER t SET COMPRESSION lz4`)
			if e.CatalogRead().MustRelation(p1ref.Address) != publishedP1 || e.CatalogRead().MustRelation(p2ref.Address) != publishedP2 || publishedP1.Revision() != publishedRevision1 || publishedP2.Revision() != publishedRevision2 || p1.Revision() != p1revision || p2.Revision() != p2revision || p1.ColumnStorage(0) != (catalog.ColumnStorage{Storage: "e", Compression: "p"}) || p2.ColumnStorage(0) != (catalog.ColumnStorage{Storage: "e"}) {
				t.Fatal("candidate aliased or replaced parent policies")
			}
			if e.Storage.Relations[p1ref.Address] != p1store || e.Storage.Relations[p2ref.Address] != p2store || child.ColumnStorage(0) != (catalog.ColumnStorage{Storage: "e", Compression: "p"}) {
				t.Fatal("candidate changed parent storage or retained child snapshot")
			}
		})
	}
}

func TestInheritanceColumnStorageLocalDeclaration(t *testing.T) {
	e := engine.New()
	for _, query := range []string{
		`CREATE TABLE policy_parent(t text)`,
		`ALTER TABLE policy_parent ALTER t SET COMPRESSION pglz`,
		`CREATE TABLE policy_template(gone integer,n numeric,t text,i integer)`,
		`ALTER TABLE policy_template DROP COLUMN gone`,
		`ALTER TABLE policy_template ALTER n SET COMPRESSION lz4`,
	} {
		enginetest.Run(t, e, query)
	}
	op, auth := conversionUnitAuth(t, e)
	e.Mu.Lock()
	defer e.Mu.Unlock()
	stage := e.BeginCatalogStage(auth.Catalog)
	defer e.DiscardCatalogStage(stage)
	h := tablecmds.TableHost{DDLHost: core.DDLHost{E: e, Op: op, CatalogStage: stage}}
	for _, tc := range []struct {
		options     string
		storage     []string
		compression []string
	}{
		{"", []string{"", "", "", ""}, []string{"", "", "", ""}},
		{"INCLUDING STORAGE", []string{"x", "", "m", "p"}, []string{"", "", "", ""}},
		{"INCLUDING COMPRESSION", []string{"", "", "", ""}, []string{"", "", "l", ""}},
		{"INCLUDING STORAGE INCLUDING COMPRESSION", []string{"x", "", "m", "p"}, []string{"", "", "l", ""}},
		{"INCLUDING ALL EXCLUDING STORAGE EXCLUDING COMPRESSION", []string{"", "", "", ""}, []string{"", "", "", ""}},
	} {
		s := enginetest.Statement(t, `CREATE TABLE policy_child(prefix integer,LIKE public."policy_template" `+tc.options+`) INHERITS(public."policy_parent")`).(*parser.CreateTable)
		b, err := h.DescribeCreateTable(auth, s, sqltype.InputEnvironment{})
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(b.Create.Likes[0].TargetSlots, []int{2, 0, 3}) {
			t.Fatal("LIKE remap lost inherited slot movement", b.Create.Likes[0].TargetSlots)
		}
		for slot, declaration := range b.Create.Declarations {
			if declaration.Storage != tc.storage[slot] || declaration.Compression != tc.compression[slot] {
				t.Fatalf("%s slot %d: unexpected specified policy %+v", tc.options, slot, declaration)
			}
		}
		if b.Create.InheritedStorage["t"] != (catalog.ColumnStorage{Storage: "x", Compression: "p"}) {
			t.Fatal("selected default compression erased the checked inherited method")
		}
	}
}

func TestInheritanceColumnStorageLocalPrecedence(t *testing.T) {
	for _, tc := range []struct {
		name, parentType, localType, code, message string
	}{
		{"type", "text", "integer", "42804", `column "t" has a type conflict`},
		{"typmod", "varchar(2)", "varchar(3)", "42804", `column "t" has a type conflict`},
		{"collation", `text COLLATE "C"`, `text COLLATE "POSIX"`, "42P21", `column "t" has a collation conflict`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := engine.New()
			enginetest.Run(t, e, `CREATE TABLE policy_parent(t `+tc.parentType+`)`)
			enginetest.Run(t, e, `ALTER TABLE policy_parent ALTER t SET STORAGE EXTERNAL, ALTER t SET COMPRESSION pglz`)
			enginetest.Run(t, e, `CREATE TABLE policy_template(t `+tc.localType+`)`)
			op, auth := conversionUnitAuth(t, e)
			e.Mu.Lock()
			defer e.Mu.Unlock()
			stage := e.BeginCatalogStage(auth.Catalog)
			defer e.DiscardCatalogStage(stage)
			h := tablecmds.TableHost{DDLHost: core.DDLHost{E: e, Op: op, CatalogStage: stage}}
			s := enginetest.Statement(t, `CREATE TABLE policy_child(LIKE policy_template INCLUDING STORAGE INCLUDING COMPRESSION) INHERITS(policy_parent)`).(*parser.CreateTable)
			_, err := h.DescribeCreateTable(auth, s, sqltype.InputEnvironment{})
			d, ok := err.(*sqlstate.Error)
			if !ok || d.Code != tc.code || d.Message != tc.message {
				t.Fatalf("type/typmod/collation must precede local policy validation: %v", err)
			}
		})
	}
}

func TestInheritanceColumnStorageLocalCandidate(t *testing.T) {
	e := engine.New()
	for _, query := range []string{
		`CREATE TABLE policy_parent(t text)`,
		`ALTER TABLE policy_parent ALTER t SET STORAGE EXTERNAL, ALTER t SET COMPRESSION pglz`,
		`CREATE TABLE policy_template(gone integer,n numeric,t text,i integer)`,
		`ALTER TABLE policy_template DROP COLUMN gone`,
		`ALTER TABLE policy_template ALTER t SET STORAGE EXTERNAL, ALTER n SET COMPRESSION lz4`,
		`CREATE INDEX policy_template_idx ON policy_template(t,t,n)`,
	} {
		enginetest.Run(t, e, query)
	}
	parentRef := enginetest.RoleTableRef(t, e, "policy_parent")
	sourceRef := enginetest.RoleTableRef(t, e, "policy_template")
	parent, source := e.CatalogRead().MustRelation(parentRef.Address), e.CatalogRead().MustRelation(sourceRef.Address)
	parentRevision, sourceRevision := parent.Revision(), source.Revision()
	parentStore, sourceStore := e.Storage.Relations[parentRef.Address], e.Storage.Relations[sourceRef.Address]
	enginetest.Run(t, e, `CREATE TABLE policy_child(prefix integer,LIKE public."policy_template" INCLUDING STORAGE INCLUDING COMPRESSION INCLUDING INDEXES) INHERITS(public."policy_parent")`)
	childRef := enginetest.RoleTableRef(t, e, "policy_child")
	child := e.CatalogRead().MustRelation(childRef.Address)
	want := map[int32]catalog.ColumnStorage{1: {Storage: "e", Compression: "p"}, 3: {Compression: "l"}}
	if !reflect.DeepEqual(child.ColumnMeta.Storage, want) {
		t.Fatal("checked pair not normalized onto final destination attributes", child.ColumnMeta.Storage)
	}
	if len(child.Inheritance.Columns[1].Origins) != 1 || !child.Inheritance.Columns[1].Local {
		t.Fatal("remapped LIKE lost inherited/local provenance")
	}
	var cloned *catalog.Relation
	for _, relation := range e.CatalogRead().Relations() {
		if relation.SQLIndex != nil && relation.SQLIndex.Parent == child.Address() {
			cloned = relation
		}
	}
	if cloned == nil {
		t.Fatal("missing destination index")
	}
	for slot, properties := range []catalog.ColumnStorage{{Storage: "e", Compression: "p"}, {Storage: "e", Compression: "p"}, {Storage: "m", Compression: "l"}} {
		if cloned.ColumnStorage(slot) != properties {
			t.Fatal("index did not consume checked destination pair", slot, cloned.ColumnStorage(slot))
		}
	}
	publishedParent := e.CatalogRead().MustRelation(parentRef.Address)
	enginetest.Run(t, e, `ALTER TABLE ONLY policy_child ALTER t SET COMPRESSION lz4, ALTER n SET STORAGE EXTERNAL`)
	if e.CatalogRead().MustRelation(sourceRef.Address) != source || source.Revision() != sourceRevision || parent.Revision() != parentRevision || e.CatalogRead().MustRelation(parentRef.Address) != publishedParent || parent.ColumnStorage(0) != (catalog.ColumnStorage{Storage: "e", Compression: "p"}) || source.ColumnStorage(1) != (catalog.ColumnStorage{Storage: "e"}) {
		t.Fatal("CREATE/child alteration mutated source policy backing")
	}
	if e.Storage.Relations[parentRef.Address] != parentStore || e.Storage.Relations[sourceRef.Address] != sourceStore || !reflect.DeepEqual(child.ColumnMeta.Storage, want) {
		t.Fatal("candidate aliased source rows or retained child policy map")
	}
}
