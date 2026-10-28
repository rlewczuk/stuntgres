package core_test

import (
	"bytes"
	"reflect"
	"strings"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/visibility"
	"stuntgres/internal/sqltype"
)

func TestAlterAccessMethodRestoreRemap(t *testing.T) {
	source,target:=engine.New(),engine.New()
	src,dst:=enginetest.StartSession(t,source,nil),enginetest.StartSession(t,target,nil)
	for _,q:=range []string{"CREATE ACCESS METHOD imported_heap TYPE TABLE HANDLER heap_tableam_handler","CREATE TABLE method_import(id int)","ALTER TABLE method_import SET ACCESS METHOD imported_heap","INSERT INTO method_import VALUES(7)"} {enginetest.SessionSQL(t,src,q)}
	for _,q:=range []string{"CREATE ACCESS METHOD retained_heap TYPE TABLE HANDLER heap_tableam_handler","CREATE TEMP TABLE method_retained(id int)","ALTER TABLE method_retained SET ACCESS METHOD retained_heap","INSERT INTO method_retained VALUES(99)"} {enginetest.SessionSQL(t,dst,q)}
	beforeSource,beforeTarget:=source.CatalogRead(),target.CatalogRead()
	imported,_:=beforeSource.LookupAccessMethod("imported_heap")
	retained,_:=beforeTarget.LookupAccessMethod("retained_heap")
	if imported.Address()!=retained.Address() || imported.Name()==retained.Name() {t.Fatal("fixture lacks actual allocator collision",imported.Address(),retained.Address())}
	original:=primaryKeyUnitRelation(t,source,"method_import");saved:=*original
	dst.Mu.Lock()
	temporaryNamespace:=dst.TemporaryNamespace
	dst.Mu.Unlock()
	retainedAddress:=beforeTarget.NameAddress(ids.NameKey{ClassID:ids.RelationClassID,Namespace:temporaryNamespace,Name:"method_retained"})
	if temporaryNamespace==(ids.ObjectAddress{}) || retainedAddress==(ids.ObjectAddress{}) {t.Fatal("missing destination session-owned TEMP identity")}
	kept:=beforeTarget.MustRelation(retainedAddress);store:=target.Storage.Relations[kept.Address()]
	held:=enginetest.SessionSQL(t,dst,"SELECT id FROM method_retained").Rows;defer held.Close()
	var dump bytes.Buffer
	if err:=engine.Dump(t.Context(),source,&dump);err!=nil {t.Fatal(err)}
	if err:=engine.Restore(t.Context(),target,bytes.NewReader(dump.Bytes()));err!=nil {t.Fatal(err)}
	after:=target.CatalogRead()
	mapped,_:=after.LookupAccessMethod("imported_heap")
	if mapped==nil || mapped.Address()==imported.Address() || mapped.Address()==retained.Address() {t.Fatal("colliding imported AM was not remapped")}
	current:=primaryKeyUnitRelation(t,target,"method_import")
	if current.TableMethod!=mapped.Address().ObjectID || after.Get(retained.Address())!=retained || after.Get(kept.Address())!=kept || kept.TableMethod!=retained.Address().ObjectID || target.Storage.Relations[kept.Address()]!=store {t.Fatal("method mapping or retained TEMP authority lost")}
	if source.CatalogRead().State()!=beforeSource.State() || !reflect.DeepEqual(*original,saved) || original.TableMethod!=imported.Address().ObjectID {t.Fatal("replacement mutated source")}
	if err:=catalog.ValidateRelationTableMethod(after,current);err!=nil {t.Fatal(err)}
	if _,err:=visibility.ResolveHeapAccess(after,current.TableMethod);err!=nil {t.Fatal(err)}
	sourceHandler,_:=beforeSource.Procedure(ids.ObjectAddress{ClassID:ids.ProcedureClassID,ObjectID:3})
	targetHandler,_:=after.Procedure(ids.ObjectAddress{ClassID:ids.ProcedureClassID,ObjectID:3})
	if sourceHandler==targetHandler || targetHandler.AccessMethod!=catalog.HeapAccessMethod {t.Fatal("source operation authority transplanted")}
	var row [1]sqltype.Value
	more,err:=held.Next(t.Context(),row[:])
	if err!=nil || !more {t.Fatal("lost retained cursor",err)}
	if value,err:=row[0].Integer();err!=nil || value!=99 {t.Fatal(value,err)}
	if more,err=held.Next(t.Context(),row[:]);err!=nil || more {t.Fatal("retained cursor changed",err)}
	enginetest.SessionSQL(t,dst,"INSERT INTO method_import VALUES(8)")
	if len(target.Storage.Relations[current.Address()].Versions)!=2 || len(source.Storage.Relations[original.Address()].Versions)!=1 {t.Fatal("remapped consumer failed real insertion/source independence")}
	// Exact catalog publication, not merely matching logical output, is fenced
	// after a late invalid method in the private replay engine.
	committed,frontier:=target.Catalog.State,target.Visibility.CommitSeq()
	if err:=engine.Restore(t.Context(),target,strings.NewReader(dump.String()+"ALTER TABLE public.method_import SET ACCESS METHOD missing_am;"));err==nil {t.Fatal("late invalid replay succeeded")}
	if target.Catalog.State!=committed || target.Visibility.CommitSeq()!=frontier || target.Storage.Relations[kept.Address()]!=store {t.Fatal("failed replay changed epoch/frontier/TEMP")}
	enginetest.SessionSQL(t,dst,"ALTER TABLE method_retained SET ACCESS METHOD imported_heap")
	committed,frontier=target.Catalog.State,target.Visibility.CommitSeq()
	retainedAfter:=target.CatalogRead().MustRelation(kept.Address())
	if err:=engine.Restore(t.Context(),target,bytes.NewReader(dump.Bytes()));err==nil || err.Error()!=`stuntgres: catalog replacement conflicts with live temporary access method "imported_heap"` {t.Fatal("same-name provider was substituted",err)}
	if target.Catalog.State!=committed || target.Visibility.CommitSeq()!=frontier || target.CatalogRead().Get(kept.Address())!=retainedAfter || retainedAfter.TableMethod!=mapped.Address().ObjectID || target.Storage.Relations[kept.Address()]!=store || source.CatalogRead().State()!=beforeSource.State() {t.Fatal("same-name rejection changed exact authority/epoch/source")}
}

func TestAlterAccessMethodRestoreOwners(t *testing.T) {
	e := engine.New()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	for _, q := range []string{
		"CREATE ACCESS METHOD owner_heap TYPE TABLE HANDLER heap_tableam_handler",
		"CREATE USER method_table_owner",
		"CREATE USER method_schema_owner",
		"CREATE SCHEMA method_owned AUTHORIZATION method_table_owner",
		"SET SESSION AUTHORIZATION method_table_owner",
		"SET default_table_access_method = owner_heap",
		"CREATE TABLE method_owned.t(id int)",
		"INSERT INTO method_owned.t VALUES(7)",
		"CREATE MATERIALIZED VIEW method_owned.mv AS SELECT id FROM method_owned.t",
		"RESET SESSION AUTHORIZATION",
		"ALTER SCHEMA method_owned OWNER TO method_schema_owner",
	} { enginetest.RoleExecute(t, e, op, q) }
	var dump bytes.Buffer
	if err := engine.Dump(t.Context(), e, &dump); err != nil { t.Fatal(err) }
	if !strings.Contains(dump.String(), "SET default_table_access_method = E'owner_heap';") ||
		strings.Contains(dump.String(), "SET ACCESS METHOD") {
		t.Fatal("independent creation needs scoped settings, not owner-sensitive ALTER", dump.String())
	}
	if err := engine.Reset(t.Context(), e); err != nil { t.Fatal(err) }
	if err := engine.Restore(t.Context(), e, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal("custom-method mixed-owner replay", err, dump.String())
	}
	tableOwner, _ := e.Roles().Lookup("method_table_owner")
	schemaOwner, _ := e.Roles().Lookup("method_schema_owner")
	method, _ := e.CatalogRead().LookupAccessMethod("owner_heap")
	for _, name := range []string{"method_owned.t", "method_owned.mv"} {
		ref := enginetest.RoleTableRef(t, e, name)
		relation := e.CatalogRead().MustRelation(ref.Address)
		namespace := e.CatalogRead().MustNamespace(relation.Namespace())
		if relation.TableMethod != method.Address().ObjectID || relation.Owner != tableOwner.Ref ||
			namespace.Owner != schemaOwner.Ref || relation.Security.ACL.Explicit || namespace.ACL.Explicit {
			t.Fatal("method replay changed independent ownership or synthesized grants", name)
		}
		got := enginetest.Tuples(enginetest.RoleExecute(t, e, op, "SELECT id FROM "+name))
		if len(got) != 1 || len(got[0]) != 1 || got[0][0] != int64(7) { t.Fatal(name, got) }
	}
	var again bytes.Buffer
	if err := engine.Dump(t.Context(), e, &again); err != nil || !bytes.Equal(dump.Bytes(), again.Bytes()) {
		t.Fatal("mixed-owner custom method stream changed", err, dump.String(), again.String())
	}
	denied, closeDenied := enginetest.RoleOperation(t, e, "method_table_owner")
	defer closeDenied()
	if _, err := e.Execute(denied, enginetest.Statement(t, "SELECT id FROM method_owned.t"), 0, nil); err == nil {
		t.Fatal("replay widened schema usage")
	}
}
