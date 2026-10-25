package core_test

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// This fixture runs each definition through Engine.Execute in the owner's
// transaction, not through the public SQL adapter.
func definitionUnitStage(t *testing.T, e *core.Engine, owner *session.Session, q string) {
	t.Helper()
	op := enginetest.TransactionOperation(t, owner)
	defer op.Done()
	if _, err := e.Execute(op, enginetest.Statement(t, q), 0, nil); err != nil {
		t.Fatal(err)
	}
}

func definitionUnitGraph(t *testing.T, e *core.Engine, owner *session.Session) {
	t.Helper()
	for _, q := range []string{`CREATE ACCESS METHOD unit_definition_am TYPE INDEX HANDLER bthandler`, `CREATE OPERATOR FAMILY public.unit_definition_family USING unit_definition_am`, `CREATE OPERATOR CLASS public.unit_definition_class DEFAULT FOR TYPE integer USING unit_definition_am FAMILY public.unit_definition_family AS OPERATOR 1 pg_catalog.<, OPERATOR 2 pg_catalog.<=, OPERATOR 3 pg_catalog.=, OPERATOR 4 pg_catalog.>=, OPERATOR 5 pg_catalog.>, FUNCTION 1 pg_catalog.btint4cmp(integer,integer)`} {
		definitionUnitStage(t, e, owner, q)
	}
}

func definitionUnitRead(e *core.Engine, owner *session.Session) catalog.Read {
	e.Mu.Lock()
	defer e.Mu.Unlock()
	return e.PrivateCatalogLocked(owner.Transactions.Catalog)
}

func definitionUnitClass(t *testing.T, v catalog.Read) (*catalog.OperatorClass, sqltype.IndexClass) {
	t.Helper()
	method, ok := v.LookupAccessMethod("unit_definition_am")
	if !ok {
		t.Fatal("missing private method")
	}
	address := v.NameAddress(catalog.ClassNameKey(ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, method.Address().ObjectID, "unit_definition_class"))
	class, ok := v.OperatorClass(address.ObjectID)
	if !ok {
		t.Fatal("missing private class")
	}
	bound, ok := v.IndexClassForMethod(class.Address().ObjectID, method)
	if !ok {
		t.Fatal("missing class adaptation")
	}
	return class, bound
}

func TestTransactionalDefinitionPrivateProviders(t *testing.T) {
	e := engine.New()
	owner := enginetest.StartSession(t, e, nil)
	ref := enginetest.TransactionBegin(t, owner)
	defer enginetest.TransactionEnd(t, owner, ref, false)
	definitionUnitGraph(t, e, owner)
	private := definitionUnitRead(e, owner)
	class, bound := definitionUnitClass(t, private)
	old, err := routine.BindClassSupport(private, bound)
	if err != nil {
		t.Fatal(err)
	}
	member, _ := private.FamilySupport(class.Family, 23, 23, 1)
	family, _ := private.OperatorFamily(ids.ObjectAddress{ClassID: ids.OperatorFamilyClassID, ObjectID: class.Family})
	procedure, _ := private.Procedure(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 351})
	expected := sqltype.IndexSupport{Family: uint32(class.Family), Member: uint32(member.Address().ObjectID), Routine: 351, Left: 23, Right: 23, Revision: uint64(member.Revision()), FamilyRevision: uint64(family.Revision()), RoutineRevision: uint64(procedure.Revision())}
	if old.Support(1) != expected {
		t.Fatal("incoherent private provider", old.Support(1))
	}
	if _, ok := e.CatalogRead().LookupAccessMethod("unit_definition_am"); ok {
		t.Fatal("private method globally visible")
	}
	changed := *member
	changed.Function = 181
	changed.ObjectRevision++
	e.Mu.Lock()
	next := owner.Transactions.Catalog.Clone()
	e.CatalogChangeLocked(next, changed.Address(), &changed)
	owner.Transactions.Catalog = next
	e.Mu.Unlock()
	current := definitionUnitRead(e, owner)
	_, freshClass := definitionUnitClass(t, current)
	fresh, err := routine.BindClassSupport(current, freshClass)
	if err != nil {
		t.Fatal(err)
	}
	typ, _ := private.LookupType(sqltype.Int4)
	a, _ := sqltype.Integer(typ, 1)
	b, _ := sqltype.Integer(typ, 3)
	original, err := sqltype.BindIndexOrdering(typ.Usage(), old)
	if err != nil {
		t.Fatal(err)
	}
	replacement, err := sqltype.BindIndexOrdering(typ.Usage(), fresh)
	if err != nil {
		t.Fatal(err)
	}
	x, err := original.Compare(context.Background(), a, b, sqltype.InputEnvironment{})
	if err != nil || x != -1 {
		t.Fatal("retained real351 comparator", x, err)
	}
	y, err := replacement.Compare(context.Background(), a, b, sqltype.InputEnvironment{})
	newProcedure, _ := current.Procedure(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 181})
	expected.Routine, expected.Revision, expected.RoutineRevision = 181, uint64(changed.Revision()), uint64(newProcedure.Revision())
	if err != nil || y != -2 || fresh.Support(1) != expected {
		t.Fatal("private real181 comparator", y, fresh.Support(1), err)
	}
	if private.Get(member.Address()) != member || old.Support(1).Routine != 351 {
		t.Fatal("private replacement mutated capture")
	}
	// Removing the actual selected support cannot fall back to integer's builtin.
	e.Mu.Lock()
	missing := next.Clone()
	e.CatalogChangeLocked(missing, member.Address(), nil)
	owner.Transactions.Catalog = missing
	e.Mu.Unlock()
	if _, err := routine.BindClassSupport(definitionUnitRead(e, owner), freshClass); err == nil {
		t.Fatal("missing private support silently fell back")
	}
	// A registered metadata member is not an executable capability grant.
	metadata := changed
	metadata.Function = 3130
	metadata.ObjectRevision++
	e.Mu.Lock()
	notCallable := next.Clone()
	e.CatalogChangeLocked(notCallable, metadata.Address(), &metadata)
	owner.Transactions.Catalog = notCallable
	e.Mu.Unlock()
	if _, err := routine.BindClassSupport(definitionUnitRead(e, owner), freshClass); err == nil {
		t.Fatal("physical optional metadata became callable support")
	}
	definitionUnitResolution(t, e, owner)
}

func TestTransactionalAccessMethodDefinitionPublication(t *testing.T) {
	t.Run("concurrent_default_journal_and_marks", definitionDefaultPublication)
	for _, commit := range []bool{false, true} {
		t.Run(map[bool]string{false: "rollback", true: "commit"}[commit], func(t *testing.T) {
			e := engine.New()
			owner := enginetest.StartSession(t, e, nil)
			ref := enginetest.TransactionBegin(t, owner)
			definitionUnitGraph(t, e, owner)
			private := definitionUnitRead(e, owner)
			class, _ := definitionUnitClass(t, private)
			peer := enginetest.StartSession(t, e, nil)
			definitionUnitStage(t, e, peer, `CREATE OPERATOR FAMILY public.unit_definition_unrelated USING hash`)
			unrelated := e.CatalogRead().NameAddress(catalog.FamilyNameKey(ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, 405, "unit_definition_unrelated"))
			if unrelated == (ids.ObjectAddress{}) {
				t.Fatal("unrelated stage did not commit")
			}
			enginetest.TransactionEnd(t, owner, ref, commit)
			committed := e.CatalogRead()
			if committed.Get(unrelated) == nil {
				t.Fatal("definition commit lost unrelated catalog work")
			}
			if commit {
				method, ok := committed.LookupAccessMethod("unit_definition_am")
				if !ok {
					t.Fatal("method group missing")
				}
				actual, bound := definitionUnitClass(t, committed)
				support, err := routine.BindClassSupport(committed, bound)
				if err != nil || actual != class || actual.Method != method.Address().ObjectID || support.Support(1).Routine != 351 {
					t.Fatal("split definition/member publication", actual, support, err)
				}
			} else {
				if _, ok := committed.LookupAccessMethod("unit_definition_am"); ok || committed.Get(class.Address()) != nil {
					t.Fatal("rollback leaked definitions")
				}
			}
		})
	}
}

func TestTransactionalDefinitionSavepoints(t *testing.T) {
	e := engine.New()
	owner := enginetest.StartSession(t, e, nil)
	ref := enginetest.TransactionBegin(t, owner)
	definitionUnitGraph(t, e, owner)
	original := definitionUnitRead(e, owner)
	class, bound := definitionUnitClass(t, original)
	owner.Transactions.Mu.Lock()
	mark := core.CaptureSavepoint(owner.Transactions, "graph")
	owner.Transactions.Mu.Unlock()
	definitionUnitStage(t, e, owner, `ALTER OPERATOR FAMILY public.unit_definition_family USING unit_definition_am ADD FUNCTION 2 (integer,integer) pg_catalog.btint4sortsupport(internal)`)
	definitionUnitStage(t, e, owner, `DROP OPERATOR CLASS public.unit_definition_class USING unit_definition_am`)
	if current := definitionUnitRead(e, owner); current.Get(class.Address()) != nil {
		t.Fatal("class retirement did not reach private journal")
	}
	for range 2 {
		owner.Transactions.Mu.Lock()
		core.RestoreSavepoint(owner.Transactions, mark, mark.Settings.Clone())
		owner.Transactions.Mu.Unlock()
		current := definitionUnitRead(e, owner)
		restored, adapted := definitionUnitClass(t, current)
		if restored != class || !adapted.SameIdentity(bound) {
			t.Fatal("savepoint reconstructed instead of restoring exact class")
		}
		if _, ok := current.FamilySupport(class.Family, 23, 23, 2); ok {
			t.Fatal("savepoint retained optional member")
		}
		actual, err := routine.BindClassSupport(current, adapted)
		if err != nil || actual.Support(1).Routine != 351 {
			t.Fatal("savepoint lost callable provider", err)
		}
	}
	enginetest.TransactionEnd(t, owner, ref, true)
	if e.CatalogRead().Get(class.Address()) != class {
		t.Fatal("restored exact graph not published")
	}
}

func definitionUnitResolution(t *testing.T, e *core.Engine, owner *session.Session) {
	t.Helper()
	base := e.CatalogRead()
	// Intrinsic cast metadata supplies the released endpoints/context/method,
	// not necessarily a stored Procedure for its function OID. A declared cast
	// needs a genuine registered dependency, so stage an actual SQL widening
	// provider with the same signature/result rather than fabricate a builtin.
	var original catalog.Cast
	for i := range sqltype.CastMetadataCount() {
		fact, _ := sqltype.CastMetadataAt(i)
		if fact.Source == sqltype.Int4 && fact.Target == sqltype.Int8 {
			original = catalog.Cast{OID: catalogUnitOID(t, e), ObjectRevision: 1, Source: fact.Source, Target: fact.Target, Context: string(fact.Context), Method: string(fact.Method), Function: ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: ids.OID(fact.Function)}}
			break
		}
	}
	if original.OID == 0 || original.Context != "i" || original.Method != "f" || original.Function.ObjectID == 0 {
		t.Fatal("missing genuine released integer widening fact", original)
	}
	if _, stored := base.Cast(sqltype.Int4, sqltype.Int8); stored {
		t.Fatal("intrinsic cast unexpectedly stored")
	}
	definitionUnitStage(t, e, owner, `CREATE FUNCTION public.unit_definition_widen(integer) RETURNS bigint LANGUAGE SQL RETURN $1::bigint`)
	providers := definitionUnitRead(e, owner)
	providerAddress := providers.NameAddress(ids.NameKey{ClassID: ids.ProcedureClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, Name: "unit_definition_widen", Signature: "23"})
	provider, present := providers.Procedure(providerAddress)
	if !present || provider.SQL == nil || provider.RoutineKind != catalog.FunctionRoutine || provider.Signature != "23" || provider.Result != ids.OID(sqltype.Int8) {
		t.Fatal("missing genuine SQL widening provider", provider)
	}
	original.Function = provider.Address()
	changed := original
	changed.ObjectRevision++
	changed.Context = "e"
	operator, ok := base.Operator(ids.ObjectAddress{ClassID: ids.OperatorClassID, ObjectID: 97})
	if !ok {
		t.Fatal("missing genuine integer less-than operator")
	}
	renamed := *operator
	renamed.ObjectRevision++
	renamed.ObjectName = "##"
	e.Mu.Lock()
	journal := owner.Transactions.Catalog.Clone()
	e.CatalogChangeLocked(journal, changed.Address(), &changed)
	e.CatalogChangeLocked(journal, renamed.Address(), &renamed)
	owner.Transactions.Catalog = journal
	e.Mu.Unlock()
	private := definitionUnitRead(e, owner)
	if err := (catalog.CastProvider{}).Validate(private, &changed); err != nil {
		t.Fatal("invalid private cast fixture", err)
	}
	if private.Get(provider.Address()) != provider || base.Get(provider.Address()) != nil {
		t.Fatal("cast dependency did not retain the actual private routine")
	}
	from, _ := private.LookupType(sqltype.Int4)
	to, _ := private.LookupType(sqltype.Int8)
	if err := catalog.CanCoerce(private, from.Usage(), to.Usage(), sqltype.Implicit); err == nil {
		t.Fatal("private declared cast context ignored in favor of builtin fallback")
	}
	if err := catalog.CanCoerce(base, from.Usage(), to.Usage(), sqltype.Implicit); err != nil {
		t.Fatal("private cast leaked into committed coercion", err)
	}
	selected, err := catalog.SelectCoercion(private, from.Usage(), to.Usage(), sqltype.Explicit)
	if err != nil || selected != &changed {
		t.Fatal("private explicit coercion selected wrong descriptor", selected, err)
	}
	if private.NameAddress(catalog.DescriptorNameKey(operator)) != (ids.ObjectAddress{}) || private.NameAddress(catalog.DescriptorNameKey(&renamed)) != operator.Address() {
		t.Fatal("private operator relabel lost exact identity/name authority")
	}
	if actual, ok := private.Operator(operator.Address()); !ok || actual != &renamed || actual.Function != operator.Function {
		t.Fatal("private operator selection lost actual implementation", actual)
	}
	if original.Context != "i" || base.Get(original.Address()) != nil || base.Get(operator.Address()) != operator {
		t.Fatal("private resolution mutated committed providers")
	}
}

func definitionDefaultPublication(t *testing.T) {
	e := engine.New()
	a, b := enginetest.StartSession(t, e, nil), enginetest.StartSession(t, e, nil)
	for _, q := range []string{`CREATE ACCESS METHOD journal_default_am TYPE INDEX HANDLER bthandler`, `CREATE OPERATOR FAMILY public.journal_default_a USING journal_default_am`, `CREATE OPERATOR FAMILY public.journal_default_b USING journal_default_am`} {
		definitionUnitStage(t, e, a, q)
	}
	method, _ := e.CatalogRead().LookupAccessMethod("journal_default_am")
	ra, rb := enginetest.TransactionBegin(t, a), enginetest.TransactionBegin(t, b)
	items := ` AS OPERATOR 1 pg_catalog.<, OPERATOR 2 pg_catalog.<=, OPERATOR 3 pg_catalog.=, OPERATOR 4 pg_catalog.>=, OPERATOR 5 pg_catalog.>, FUNCTION 1 pg_catalog.btint4cmp(integer,integer)`
	definitionUnitStage(t, e, a, `CREATE OPERATOR CLASS public.journal_default_a DEFAULT FOR TYPE integer USING journal_default_am FAMILY public.journal_default_a`+items)
	definitionUnitStage(t, e, b, `CREATE OPERATOR CLASS public.journal_default_b DEFAULT FOR TYPE integer USING journal_default_am FAMILY public.journal_default_b`+items)
	for _, owner := range []*session.Session{a, b} {
		if _, ok, err := definitionUnitRead(e, owner).DefaultIndexClassID(method.Address().ObjectID, sqltype.Int4); !ok || err != nil {
			t.Fatal("private default was fenced by another transaction", ok, err)
		}
	}
	enginetest.TransactionEnd(t, a, ra, true)
	both := definitionUnitRead(e, b)
	_, ok, err := both.DefaultIndexClassID(method.Address().ObjectID, sqltype.Int4)
	var diagnostic *sqlstate.Error
	if ok || !errors.As(err, &diagnostic) || diagnostic.Code != "42710" {
		t.Fatal("private overlay discarded concurrent committed default", ok, err)
	}
	b.Transactions.Mu.Lock()
	mark := core.CaptureSavepoint(b.Transactions, "multiple_defaults")
	b.Transactions.Mu.Unlock()
	privateClass, _ := both.LookupOperatorClass(ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, method.Address().ObjectID, "journal_default_b")
	definitionUnitStage(t, e, b, `DROP OPERATOR CLASS public.journal_default_b USING journal_default_am`)
	if selected, ok, err := definitionUnitRead(e, b).DefaultIndexClassID(method.Address().ObjectID, sqltype.Int4); !ok || err != nil || selected.OID() == uint32(privateClass.Address().ObjectID) {
		t.Fatal("private drop did not retain concurrent provider", selected, err)
	}
	for range 2 {
		b.Transactions.Mu.Lock()
		core.RestoreSavepoint(b.Transactions, mark, mark.Settings.Clone())
		b.Transactions.Mu.Unlock()
		current := definitionUnitRead(e, b)
		if current.Get(privateClass.Address()) != privateClass {
			t.Fatal("mark did not restore exact default identity")
		}
		if _, ok, err := current.DefaultIndexClassID(method.Address().ObjectID, sqltype.Int4); ok || !errors.As(err, &diagnostic) {
			t.Fatal("mark lost immutable default bucket", ok, err)
		}
	}
	enginetest.TransactionEnd(t, b, rb, true)
	committed := e.CatalogRead()
	if _, ok, err := committed.DefaultIndexClassID(method.Address().ObjectID, sqltype.Int4); ok || !errors.As(err, &diagnostic) || diagnostic.Code != "42710" {
		t.Fatal("commit selected a unique default winner", ok, err)
	}
	if both.Get(privateClass.Address()) != privateClass {
		t.Fatal("mark or publication mutated retained read")
	}
	var output bytes.Buffer
	err = engine.Dump(context.Background(), e, &output)
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
	if err := engine.Dump(context.Background(), e, &encoded); err != nil {
		t.Fatal("one persistent plus one TEMP default must remain representable", err)
	}
	fresh := engine.New()
	if err := engine.Restore(context.Background(), fresh, bytes.NewReader(encoded.Bytes())); err != nil {
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
