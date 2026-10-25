package engine

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func definitionDefaultPublication(t *testing.T) {
	e := New()
	a, b := sessionStart(t, e, nil), sessionStart(t, e, nil)
	for _, q := range []string{`CREATE ACCESS METHOD journal_default_am TYPE INDEX HANDLER bthandler`, `CREATE OPERATOR FAMILY public.journal_default_a USING journal_default_am`, `CREATE OPERATOR FAMILY public.journal_default_b USING journal_default_am`} {
		definitionUnitStage(t, e, a, q)
	}
	method, _ := e.CatalogRead().LookupAccessMethod("journal_default_am")
	ra, rb := transactionBegin(t, a), transactionBegin(t, b)
	items := ` AS OPERATOR 1 pg_catalog.<, OPERATOR 2 pg_catalog.<=, OPERATOR 3 pg_catalog.=, OPERATOR 4 pg_catalog.>=, OPERATOR 5 pg_catalog.>, FUNCTION 1 pg_catalog.btint4cmp(integer,integer)`
	definitionUnitStage(t, e, a, `CREATE OPERATOR CLASS public.journal_default_a DEFAULT FOR TYPE integer USING journal_default_am FAMILY public.journal_default_a`+items)
	definitionUnitStage(t, e, b, `CREATE OPERATOR CLASS public.journal_default_b DEFAULT FOR TYPE integer USING journal_default_am FAMILY public.journal_default_b`+items)
	for _, owner := range []*session.Session{a, b} {
		if _, ok, err := definitionUnitRead(e, owner).DefaultIndexClassID(method.Address().ObjectID, sqltype.Int4); !ok || err != nil {
			t.Fatal("private default was fenced by another transaction", ok, err)
		}
	}
	transactionEnd(t, a, ra, true)
	both := definitionUnitRead(e, b)
	_, ok, err := both.DefaultIndexClassID(method.Address().ObjectID, sqltype.Int4)
	var diagnostic *sqlstate.Error
	if ok || !errors.As(err, &diagnostic) || diagnostic.Code != "42710" {
		t.Fatal("private overlay discarded concurrent committed default", ok, err)
	}
	b.Transactions.Mu.Lock()
	mark := CaptureSavepoint(b.Transactions, "multiple_defaults")
	b.Transactions.Mu.Unlock()
	privateClass, _ := both.LookupOperatorClass(ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, method.Address().ObjectID, "journal_default_b")
	definitionUnitStage(t, e, b, `DROP OPERATOR CLASS public.journal_default_b USING journal_default_am`)
	if selected, ok, err := definitionUnitRead(e, b).DefaultIndexClassID(method.Address().ObjectID, sqltype.Int4); !ok || err != nil || selected.OID() == uint32(privateClass.Address().ObjectID) {
		t.Fatal("private drop did not retain concurrent provider", selected, err)
	}
	for range 2 {
		b.Transactions.Mu.Lock()
		RestoreSavepoint(b.Transactions, mark, mark.Settings.Clone())
		b.Transactions.Mu.Unlock()
		current := definitionUnitRead(e, b)
		if current.Get(privateClass.Address()) != privateClass {
			t.Fatal("mark did not restore exact default identity")
		}
		if _, ok, err := current.DefaultIndexClassID(method.Address().ObjectID, sqltype.Int4); ok || !errors.As(err, &diagnostic) {
			t.Fatal("mark lost immutable default bucket", ok, err)
		}
	}
	transactionEnd(t, b, rb, true)
	committed := e.CatalogRead()
	if _, ok, err := committed.DefaultIndexClassID(method.Address().ObjectID, sqltype.Int4); ok || !errors.As(err, &diagnostic) || diagnostic.Code != "42710" {
		t.Fatal("commit selected a unique default winner", ok, err)
	}
	if both.Get(privateClass.Address()) != privateClass {
		t.Fatal("mark or publication mutated retained read")
	}
	var output bytes.Buffer
	err = Dump(context.Background(), e, &output)
	var state interface{ SQLState() string }
	if err == nil || err.Error() != "stuntgres: multiple default operator classes cannot be dumped" || errors.As(err, &state) || output.Len() != 0 || e.CatalogRead().State() != committed.State() {
		t.Fatal("nonrepresentable Dump changed state, diagnostics or writer", err, output.String())
	}
	class, ok := committed.IndexClass(privateClass.Address().ObjectID)
	if !ok {
		t.Fatal("failed Dump erased named provider")
	}
	bound, err := routine.BindClassSupport(committed, class)
	if err != nil || bound.Support(1).Routine != 351 {
		t.Fatal("failed Dump invalidated retained provider", bound, err)
	}
	// TEMP exclusion belongs to replay only. Build a valid immutable catalog
	// fixture with the second class in its owner's real temporary namespace.
	temporary := catalog.CloneState(committed.State())
	ns := &catalog.Namespace{ObjectAddress: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 98080}, ObjectName: "pg_temp_default_guard", ObjectRevision: 1, Temporary: b.SessionToken, Owner: e.RoleStore.Domain.Principal(10)}
	if temporary.Get(ns.Address()) != nil {
		t.Fatal("temporary fixture address collision")
	}
	temporary.SetDescriptor(ns.Address(), ns)
	temporary.BindName(catalog.DescriptorNameKey(ns), ns.Address())
	tempClass := *privateClass
	tempClass.ObjectNamespace = ns.Address()
	tempClass.ObjectRevision++
	temporary.UnbindName(catalog.DescriptorNameKey(privateClass))
	temporary.SetDescriptor(tempClass.Address(), &tempClass)
	temporary.BindName(catalog.DescriptorNameKey(&tempClass), tempClass.Address())
	replay := committed.WithState(temporary)
	if replay.HasMultiplePersistentDefaults() {
		t.Fatal("excluded TEMP class rejected persistent replay")
	}
	if _, ok, err := replay.DefaultIndexClassID(method.Address().ObjectID, sqltype.Int4); ok || !errors.As(err, &diagnostic) || diagnostic.Code != "42710" {
		t.Fatal("replay filtering leaked into runtime default selection", ok, err)
	}
	e.Mu.Lock()
	e.Catalog.State = temporary
	e.Mu.Unlock()
	var encoded bytes.Buffer
	if err := Dump(context.Background(), e, &encoded); err != nil {
		t.Fatal("one persistent plus one TEMP default must remain representable", err)
	}
	fresh := New()
	if err := Restore(context.Background(), fresh, bytes.NewReader(encoded.Bytes())); err != nil {
		t.Fatal(err)
	}
	restoredMethod, _ := fresh.CatalogRead().LookupAccessMethod("journal_default_am")
	restored, ok, err := fresh.CatalogRead().DefaultIndexClassID(restoredMethod.Address().ObjectID, sqltype.Int4)
	if !ok || err != nil || restored.Name != "journal_default_a" {
		t.Fatal("persistent replay imported TEMP default", restored, err)
	}
	withoutTemp := catalog.CloneState(temporary)
	withoutTemp.RemoveDescriptor(tempClass.Address())
	if read := committed.WithState(withoutTemp); read.HasMultiplePersistentDefaults() {
		t.Fatal("dropping TEMP default retained duplicate replay bucket")
	}
	onlyTemp := catalog.CloneState(temporary)
	persistent, _ := committed.LookupOperatorClass(ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, method.Address().ObjectID, "journal_default_a")
	onlyTemp.RemoveDescriptor(persistent.Address())
	if read := committed.WithState(onlyTemp); read.HasMultiplePersistentDefaults() {
		t.Fatal("TEMP-only default rejected empty persistent replay")
	}
	if !committed.HasMultiplePersistentDefaults() || (catalog.Read{}).HasMultiplePersistentDefaults() {
		t.Fatal("retained/nil replay cardinality changed")
	}
}
