package enginetest

import (
	"context"
	"database/sql/driver"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func AccessMethodUnit(t testing.TB, e *core.Engine, name string, native catalog.NativeAccessMethod, handler ids.OID) *catalog.AccessMethod {
	t.Helper()
	return &catalog.AccessMethod{ObjectAddress: ids.ObjectAddress{ClassID: ids.AccessMethodClassID, ObjectID: CatalogUnitOID(t, e)}, ObjectRevision: 1, ObjectName: name, Native: native, Handler: handler}
}

func AccessMethodPublish(t testing.TB, e *core.Engine, definitions ...catalog.Descriptor) catalog.Read {
	t.Helper()
	base := e.CatalogRead()
	stage := e.BeginCatalogStage(base)
	defer e.DiscardCatalogStage(stage)
	for _, d := range definitions {
		if err := stage.Create(d); err != nil {
			t.Fatal(err)
		}
	}
	prepared, err := e.ValidateCatalogStage(context.Background(), stage)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = PublishCatalog(e, prepared, core.PublicationToken{Epoch: base.Epoch()}); err != nil {
		t.Fatal(err)
	}
	return e.CatalogRead()
}

// Fixture identities use the real allocator: bootstrap definitions can occupy
// user-range OIDs without becoming relation descriptors.
func CatalogUnitOID(t testing.TB, e *core.Engine) ids.OID {
	t.Helper()
	e.Mu.Lock()
	oid, err := e.Catalog.AllocateOIDLocked()
	e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	return oid
}

// WithPublicSchemaUnitOwner provisions only a positive CREATE or owner-successor
// precondition. It leaves the tested operation's identity and object ACLs alone.
func WithPublicSchemaUnitOwner(t testing.TB, e *core.Engine, owner string, body func()) {
	t.Helper()
	view := e.CatalogRead()
	addresses := view.Lookup(ids.NameKey{ClassID: ids.NamespaceClassID, Name: "public"})
	if len(addresses) != 1 {
		t.Fatal("public schema fixture requires one public namespace")
	}
	descriptor, ok := view.Descriptor(addresses[0])
	if !ok {
		t.Fatal("public schema fixture requires its namespace descriptor")
	}
	namespace, ok := descriptor.(*catalog.Namespace)
	if !ok {
		t.Fatal("public schema fixture has a non-namespace descriptor")
	}
	roles := e.Roles()
	prior, err := roles.Descriptor(namespace.Owner)
	if err != nil {
		t.Fatal(err)
	}
	bootstrap, err := roles.Descriptor(e.RoleStore.Domain.Principal(10))
	if err != nil {
		t.Fatal(err)
	}
	login, err := e.ResolveLogin(bootstrap.Name)
	if err != nil {
		t.Fatal(err)
	}
	sess, err := OpenSession(context.Background(), e, session.SessionStart{ID: NextSessionID()}, login)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		sess.RequestEnd(session.CleanClose)
		if err := sess.WaitEnd(context.Background()); err != nil {
			t.Error("closing public schema fixture session:", err)
		}
	}()
	admin, err := sess.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Done()
	quote := func(name string) string { return `"` + strings.ReplaceAll(name, `"`, `""`) + `"` }
	// Restore while the borrowed operation and the prior owner are still live,
	// including when the body terminates through testing.T.Fatal.
	defer func() {
		restore, err := parser.Parse("ALTER SCHEMA public OWNER TO " + quote(prior.Name))
		if err != nil {
			t.Error("parsing public schema owner restoration:", err)
			return
		}
		if len(restore) != 1 {
			t.Errorf("expected single public schema owner restoration statement, got %d", len(restore))
			return
		}
		if _, err := e.Execute(admin, restore[0], 0, nil); err != nil {
			t.Error("restoring public schema owner:", err)
		}
	}()
	RoleExecute(t, e, admin, "ALTER SCHEMA public OWNER TO "+quote(owner))
	body()
}

func ExtensionUnitExecute(t testing.TB, e *core.Engine, op *session.Operation, stmt parser.Statement) error {
	t.Helper()
	scope, err := e.StartStatement(op)
	if err != nil {
		return err
	}
	finished := false
	defer func() {
		// A malformed unit AST or an unexpected engine panic must not leave
		// statement admission held while roleOperation closes the session.
		// Preserve the panic; this defer only performs the normal scope unwind.
		if !finished {
			_ = scope.Finish(errors.New("stuntgres: extension unit statement unwound"))
		}
	}()
	_, err = scope.Execute(stmt, 0, nil)
	finished = true
	return scope.Finish(err)
}

// CatalogStateFixture builds an unpublished state at epoch that holds
// descriptors and names, through the state's write API.
func CatalogStateFixture(epoch ids.CatalogEpoch, descriptors map[ids.ObjectAddress]catalog.Descriptor, names map[ids.NameKey]ids.ObjectAddress) *catalog.State {
	s := catalog.NewState(epoch)
	for a, d := range descriptors {
		s.SetDescriptor(a, d)
	}
	for k, a := range names {
		s.BindName(k, a)
	}
	return s
}

func AssertTemporaryNamespacePair(t testing.TB, e *core.Engine, token ids.SessionToken, table ids.ObjectAddress) ids.ObjectAddress {
	t.Helper()
	ns, ok := e.Catalog.State.Namespace(table)
	if !ok || ns.Temporary != token || ns.TemporaryToast || ns.Owner.ID() != 10 || ns.ACL.Explicit || len(ns.ACL.Entries) != 0 {
		t.Fatal("real table TEMP authority", ns)
	}
	suffix, ok := strings.CutPrefix(ns.Name(), "pg_temp_")
	if !ok {
		t.Fatal(ns.Name())
	}
	var toast ids.ObjectAddress
	count := 0
	for a, d := range e.Catalog.State.Descriptors() {
		other, ok := d.(*catalog.Namespace)
		if !ok || other.Temporary != token {
			continue
		}
		count++
		if other.TemporaryToast {
			if other.Name() != "pg_toast_temp_"+suffix || other.Owner != ns.Owner || other.ACL.Explicit || len(other.ACL.Entries) != 0 || other.Address() == table {
				t.Fatal("real toast TEMP authority", other)
			}
			toast = a
		}
		if e.Catalog.State.NameAddress(catalog.DescriptorNameKey(other)) != a {
			t.Fatal("TEMP name identity", other)
		}
	}
	if count != 2 || toast == (ids.ObjectAddress{}) {
		t.Fatal("TEMP pair cardinality", count, toast)
	}
	return toast
}

func RegisteredCatalogProducer(oid ids.OID) pgcatalog.Producer {
	produce, ok := pgcatalog.ProducerFor(oid)
	if !ok {
		panic(fmt.Sprintf("no catalog producer registered for OID %d", oid))
	}
	return produce
}

func SchemaRef(t testing.TB, e *core.Engine, name string) ids.ObjectRef {
	t.Helper()
	v := e.CatalogRead()
	a := v.NameAddress(ids.NameKey{ClassID: ids.NamespaceClassID, Name: name})
	if a == (ids.ObjectAddress{}) {
		t.Fatal("missing namespace", name)
	}
	return e.Catalog.Domain.Ref(v.Epoch(), a)
}

func InheritanceUnitRelation(e *core.Engine, name string, id ids.OID, names ...string) *catalog.Relation {
	d := &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: id}, Owner: e.RoleStore.Published.Root.Read(authorization.PrincipalRef{}).LookupRef("postgres"), ObjectNamespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, ObjectName: name, ObjectRevision: 1, Index: make(map[string]int)}
	d.TableMethod = 2
	for i, name := range names {
		d.Index[name] = i
		d.Attributes = append(d.Attributes, int32(i+1))
		d.Columns = append(d.Columns, catalog.Column{Name: name, Usage: Usage(e, sqltype.Int4), Nullable: true, NullabilityKnown: true, OriginTable: uint32(id), OriginAttribute: int16(i + 1)})
	}
	d.ColumnMeta.HighWater = int32(len(names))
	return d
}

func PrimaryKeyUnitRelation(t testing.TB, e *core.Engine, name string) *catalog.Relation {
	t.Helper()
	address, ok := e.Catalog.State.LookupName(ids.NameKey{ClassID: ids.RelationClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, Name: name})
	if !ok {
		t.Fatal("missing relation", name)
	}
	return e.Catalog.State.MustRelation(address)
}

// ReplacementProcedurePolicyFixture supplies the real host policy for private
// address/type remap fixtures; it does not manufacture numeric principal links.
func ReplacementProcedurePolicyFixture(source, target *core.Engine) catalog.ProcedureRemapper {
	sourceRoles := source.RoleStore.PublishedRead(&source.Catalog.Owner, source.Catalog.Current())
	targetRoles := target.RoleStore.PublishedRead(&target.Catalog.Owner, target.Catalog.Current())
	return catalog.NewProcedureRemapper(target.RoleStore.PrincipalRemap(&target.Catalog, sourceRoles, targetRoles, nil, routine.OperatorSeeded))
}

func SequenceScalar(t testing.TB, s *session.Session, q string, want int64) {
	t.Helper()
	r := SessionSQL(t, s, q)
	defer r.Rows.Close()
	if got := Tuples(r); !reflect.DeepEqual(got, [][]driver.Value{{want}}) {
		t.Fatalf("%s got %v want %d", q, got, want)
	}
}

func ConfigName(name string) parser.ObjectName {
	return parser.ObjectName{Parts: []parser.Identifier{{Text: name}}}
}
