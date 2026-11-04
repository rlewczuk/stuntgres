package core_test

import (
	"database/sql/driver"
	"errors"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/engine/view"
	"stuntgres/internal/sqltype"
)

func TestSepgsqlSimulationLabels(t *testing.T) {
	e := sepgsqlPhaseEngine(t)
	enginetest.Run(t, e, "CREATE TABLE sepgsql_label_owner(a integer,b text)")
	original := e.CatalogRead()
	address := enginetest.CatalogRef(t, e, original, "sepgsql_label_owner").Address
	key := catalog.SecurityLabelKey{Object: address, Provider: "selinux"}
	before := original.State().SecurityLabel(key)
	if before == nil || before.Label != "P" {
		t.Fatal("missing actual create label", before)
	}
	enginetest.Run(t, e, "SECURITY LABEL ON TABLE sepgsql_label_owner IS 'S'")
	if original.State().SecurityLabel(key) != before || before.Label != "P" {
		t.Fatal("label publication mutated an old catalog snapshot")
	}
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	sepgsqlPhaseRows(t, e, op, "SELECT objsubid,label FROM pg_seclabel WHERE objoid='sepgsql_label_owner'::regclass ORDER BY objsubid", [][]driver.Value{
		{int64(-6), "P"}, {int64(-5), "P"}, {int64(-4), "P"}, {int64(-3), "P"}, {int64(-2), "P"}, {int64(-1), "P"}, {int64(0), "S"}, {int64(1), "P"}, {int64(2), "P"},
	})
	enginetest.RoleExecute(t, e, op, "BEGIN")
	enginetest.RoleExecute(t, e, op, "SECURITY LABEL ON COLUMN sepgsql_label_owner.a IS 'S'")
	enginetest.RoleExecute(t, e, op, "SAVEPOINT label_phase")
	enginetest.RoleExecute(t, e, op, "SECURITY LABEL ON COLUMN sepgsql_label_owner.a IS 'U'")
	enginetest.RoleExecute(t, e, op, "ROLLBACK TO label_phase")
	sepgsqlPhaseRows(t, e, op, "SELECT label FROM pg_seclabel WHERE objoid='sepgsql_label_owner'::regclass AND objsubid=1", [][]driver.Value{{"S"}})
	enginetest.RoleExecute(t, e, op, "ROLLBACK")
	sepgsqlPhaseRows(t, e, op, "SELECT label FROM pg_seclabel WHERE objoid='sepgsql_label_owner'::regclass AND objsubid=1", [][]driver.Value{{"P"}})
	enginetest.RoleExecute(t, e, op, "SECURITY LABEL ON COLUMN sepgsql_label_owner.ctid IS 'S'")
	sepgsqlPhaseRows(t, e, op, "SELECT objtype,objname,label FROM pg_seclabels WHERE objoid='sepgsql_label_owner'::regclass AND objsubid=-1", [][]driver.Value{{"column", "sepgsql_label_owner.ctid", "S"}})
	enginetest.RoleExecute(t, e, op, "SECURITY LABEL ON TABLE sepgsql_label_owner IS NULL")
	sepgsqlPhaseRows(t, e, op, "SELECT count(*) FROM pg_seclabel WHERE objoid='sepgsql_label_owner'::regclass AND objsubid=0", [][]driver.Value{{int64(0)}})
	if original.State().SecurityLabel(key) != before {
		t.Fatal("association removal changed old immutable state")
	}
	enginetest.RoleExecute(t, e, op, "SELECT sepgsql_setcon('R')")
	enginetest.RoleError(t, e, op, "SECURITY LABEL ON COLUMN sepgsql_label_owner.b IS 'S'", "42501", "SELinux: security policy violation", "")
	enginetest.RoleExecute(t, e, op, "SELECT sepgsql_setcon(NULL)")
	sepgsqlPhaseRows(t, e, op, "SELECT label FROM pg_seclabel WHERE objoid='sepgsql_label_owner'::regclass AND objsubid=2", [][]driver.Value{{"P"}})
}

func TestSepgsqlSimulationSharedLabels(t *testing.T) {
	e := sepgsqlPhaseEngine(t)
	publisher := enginetest.StartSession(t, e, nil)
	const database = "sepgsql_shared_owner"
	enginetest.SessionSQL(t, publisher, "CREATE DATABASE "+database)
	peer, err := e.SelectDatabase(t.Context(), database)
	if err != nil {
		t.Fatal(err)
	}
	if peer == e || peer.Catalog.Domain == e.Catalog.Domain {
		t.Fatal("shared-label witness requires distinct genuine database roots")
	}

	address := peer.Catalog.DatabaseAddress()
	key := catalog.SecurityLabelKey{Object: address, Provider: "selinux"}
	oldRead, oldPublisherRead := peer.CatalogRead(), e.CatalogRead()
	databaseObject := oldRead.MustDatabase(address)
	before := oldRead.State().SecurityLabel(key)
	if before == nil || before.Label != "P" || oldPublisherRead.Get(address) != databaseObject || oldPublisherRead.State().SecurityLabel(key) != before {
		t.Fatal("new database lacks its canonical shared descriptor and initial P label")
	}
	beforeValue, oldLabels := *before, oldRead.State().SecurityLabelRoot()
	oldPublisherLabels := oldPublisherRead.State().SecurityLabelRoot()

	stale := enginetest.StartSession(t, peer, nil)
	enginetest.SessionSQL(t, stale, "BEGIN")
	// Creating a real schema observes this database's P label and leaves a
	// genuine private catalog journal without locking the database for relabel.
	const schema = "sepgsql_shared_private"
	enginetest.SessionSQL(t, stale, "CREATE SCHEMA "+schema)
	journal := stale.Transactions.Catalog
	if journal == nil {
		t.Fatal("schema creation did not retain a real private catalog journal")
	}
	observed, present := journal.LabelReads[key]
	if !present || observed != before || journal.Originals[address] != databaseObject {
		t.Fatal("private transaction did not capture the genuine database-label predecessor")
	}
	peer.Mu.Lock()
	privateRead := peer.PrivateCatalogLocked(journal)
	peer.Mu.Unlock()
	privateSchema := privateRead.NameAddress(ids.NameKey{ClassID: ids.NamespaceClassID, Name: schema})
	privateKey := catalog.SecurityLabelKey{Object: privateSchema, Provider: "selinux"}
	privateLabel := privateRead.State().SecurityLabel(privateKey)
	if privateSchema.ObjectID == 0 || privateRead.Get(privateSchema) == nil || privateLabel == nil || privateLabel.Label != "P" || peer.CatalogRead().Get(privateSchema) != nil {
		t.Fatal("schema and its label must exist only in the actual private read")
	}

	enginetest.SessionSQL(t, publisher, "SECURITY LABEL ON DATABASE "+database+" IS 'S'")
	currentPublisher, currentPeer := e.CatalogRead(), peer.CatalogRead()
	winner := currentPublisher.State().SecurityLabel(key)
	if winner == nil || winner == before || winner.Label != "S" || currentPeer.State().SecurityLabel(key) != winner {
		t.Fatal("label-only publication did not expose the same new S association across roots")
	}
	if currentPublisher.State() == oldPublisherRead.State() || currentPeer.State() == oldRead.State() || currentPublisher.Get(address) != databaseObject || currentPeer.Get(address) != databaseObject {
		t.Fatal("shared label-only publication must replace both roots, not the database descriptor")
	}
	checkOldReads := func() {
		t.Helper()
		if oldRead.State().SecurityLabelRoot() != oldLabels || oldPublisherRead.State().SecurityLabelRoot() != oldPublisherLabels ||
			oldRead.State().SecurityLabel(key) != before || oldPublisherRead.State().SecurityLabel(key) != before || *before != beforeValue ||
			oldRead.Get(address) != databaseObject || oldPublisherRead.Get(address) != databaseObject {
			t.Fatal("shared publication mutated a retained genuine old CatalogRead")
		}
		if privateRead.State().SecurityLabel(key) != before || privateRead.State().SecurityLabel(privateKey) != privateLabel || privateRead.Get(privateSchema) == nil {
			t.Fatal("peer publication or rollback mutated the retained private read")
		}
	}
	checkOldReads()

	// This is an actual relabel through the stale physical transaction, not a
	// hand-built candidate or a direct call to a predecessor validator.
	op, err := stale.Acquire(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	result, err := peer.Execute(op, enginetest.Statement(t, "SECURITY LABEL ON DATABASE "+database+" IS 'U'"), 0, nil)
	if result.Rows != nil {
		if closeErr := result.Rows.Close(); closeErr != nil {
			t.Error(closeErr)
		}
	}
	op.Done()
	var conflict catalog.Conflict
	if !errors.As(err, &conflict) {
		t.Fatalf("stale private database relabel must reject with a catalog conflict: %T %v", err, err)
	}
	checkCommitted := func() {
		t.Helper()
		for _, root := range []catalog.Read{e.CatalogRead(), peer.CatalogRead()} {
			if root.Get(address) != databaseObject || root.State().SecurityLabel(key) != winner || winner.Label != "S" {
				t.Fatal("stale relabel lost the previously published shared S label")
			}
			if root.Get(privateSchema) != nil || root.State().SecurityLabel(privateKey) != nil ||
				root.NameAddress(ids.NameKey{ClassID: ids.NamespaceClassID, Name: schema}) != (ids.ObjectAddress{}) {
				t.Fatal("rejected private relabel partially published its schema or label")
			}
		}
	}
	checkCommitted()
	enginetest.SessionSQL(t, stale, "ROLLBACK")
	if stale.TransactionState() != session.TxIdle || stale.Transactions.Catalog != nil {
		t.Fatal("rollback retained the rejected transaction's private catalog")
	}
	checkCommitted()
	checkOldReads()
	enginetest.LockEmpty(t, e)
}

func TestSepgsqlSimulationDemands(t *testing.T) {
	t.Run("frozen_whole_row_is_semantic_not_expanded_star", func(t *testing.T) {
		e := sepgsqlPhaseEngine(t)
		enginetest.Run(t, e, "CREATE TABLE sepgsql_manifest_parent(id integer)")
		enginetest.Run(t, e, "CREATE TABLE sepgsql_manifest_child(lead text,id integer)")
		enginetest.Run(t, e, "ALTER TABLE sepgsql_manifest_child INHERIT sepgsql_manifest_parent")
		enginetest.Run(t, e, "CREATE VIEW sepgsql_manifest_whole AS SELECT sepgsql_manifest_parent AS value FROM sepgsql_manifest_parent")
		enginetest.Run(t, e, "CREATE VIEW sepgsql_manifest_columns AS SELECT sepgsql_manifest_parent.* FROM sepgsql_manifest_parent")
		read := e.CatalogRead()
		whole := read.MustRelation(enginetest.CatalogRef(t, e, read, "sepgsql_manifest_whole").Address)
		columns := read.MustRelation(enginetest.CatalogRef(t, e, read, "sepgsql_manifest_columns").Address)
		wholeSources, columnSources := stored.ViewOf(whole).Sources(), stored.ViewOf(columns).Sources()
		if len(wholeSources) != 1 || len(columnSources) != 1 || !wholeSources[0].WholeRowRead || columnSources[0].WholeRowRead || len(wholeSources[0].Reads) != 1 || wholeSources[0].Reads[0] != 1 {
			t.Fatal("frozen whole-row source lost its distinct semantic demand", wholeSources, columnSources)
		}
		op, done := enginetest.RoleOperation(t, e, "postgres")
		defer done()
		e.Mu.Lock()
		auth, err := e.AuthorizationLocked(op)
		e.Mu.Unlock()
		if err != nil {
			t.Fatal(err)
		}
		source, err := view.SourceRequirementLeaf(auth.View, whole, 0, plan.RowMark{})
		if err != nil || !source.WholeRowRead || len(source.Uses) != 1 || source.Uses[0] != 0 || source.Ref.Address != wholeSources[0].Ref.Address {
			t.Fatal("current source reconstruction lost immutable whole-row lineage", source, err)
		}
	})
	e := sepgsqlPhaseEngine(t)
	enginetest.Run(t, e, "CREATE TABLE sepgsql_acl_owner(v integer)")
	enginetest.Run(t, e, "INSERT INTO sepgsql_acl_owner VALUES(7)")
	enginetest.Run(t, e, "CREATE ROLE sepgsql_sql_reader LOGIN")
	enginetest.Run(t, e, "SECURITY LABEL ON TABLE sepgsql_acl_owner IS 'S'")
	reader, finishReader := enginetest.RoleOperation(t, e, "sepgsql_sql_reader")
	defer finishReader()
	// This physical subject is A, with every MAC permission. It still has no
	// ordinary SQL table grant; MAC must not become an ACL shortcut.
	sepgsqlPhaseRows(t, e, reader, "SELECT sepgsql_getcon()", [][]driver.Value{{"A"}})
	enginetest.RoleError(t, e, reader, "SELECT v FROM sepgsql_acl_owner", "42501", "permission denied for table sepgsql_acl_owner", "")
	enginetest.Run(t, e, "GRANT SELECT ON sepgsql_acl_owner TO sepgsql_sql_reader")
	sepgsqlPhaseRows(t, e, reader, "SELECT v FROM sepgsql_acl_owner", [][]driver.Value{{int64(7)}})
	admin, finishAdmin := enginetest.RoleOperation(t, e, "postgres")
	defer finishAdmin()
	enginetest.RoleExecute(t, e, admin, "SELECT sepgsql_setcon('R')")
	// Conversely this genuine SQL superuser still cannot read the S table as R.
	enginetest.RoleError(t, e, admin, "SELECT v FROM sepgsql_acl_owner", "42501", "SELinux: security policy violation", "")
	e.Mu.Lock()
	readerView, readerErr := e.AuthorizationLocked(reader)
	adminView, adminErr := e.AuthorizationLocked(admin)
	e.Mu.Unlock()
	if readerErr != nil || adminErr != nil {
		t.Fatal(readerErr, adminErr)
	}
	if readerView.State.CurrentUser == adminView.State.CurrentUser || readerView.Sepgsql.Subject != "A" || adminView.Sepgsql.Subject != "R" || readerView.Sepgsql.Policy != adminView.Sepgsql.Policy {
		t.Fatal("captured ACL principals and physical MAC subjects were conflated")
	}
	sepgsqlPhaseRows(t, e, admin, "SELECT current_user,sepgsql_getcon()", [][]driver.Value{{"postgres", "R"}})
	sepgsqlPhaseRows(t, e, reader, "SELECT current_user,sepgsql_getcon()", [][]driver.Value{{"sepgsql_sql_reader", "A"}})
}

func TestSepgsqlCatalogIdentityComposition(t *testing.T) {
	e := engine.New()
	read := e.CatalogRead()
	state := read.State()
	labels := read.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 12099})
	database := read.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 12212})
	indexes := read.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 12359})
	row := read.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 12101})
	array := read.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 12100})
	databaseRow := read.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 12272})
	databaseArray := read.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 12271})
	if labels.Name() != "pg_seclabels" || labels.Address().ObjectID != pgcatalog.SeclabelsOID || pgcatalog.SeclabelsRuleOID != 12361 || labels.ReturnRuleOID() != 12361 ||
		database.Name() != "pg_stat_database" || database.ReturnRuleOID() != 12360 || indexes.Name() != "pg_statio_user_indexes" {
		t.Fatal("composed native view/rule identities", labels, database, indexes)
	}
	if labels.RowType != 12101 || row.Array != 12100 || row.OwningRelation != labels.Address() || array.OwningRelation != labels.Address() ||
		array.SQLType.Element() != row.SQLType || row.Owner != labels.Owner || array.Owner != labels.Owner || len(labels.Columns) != 8 {
		t.Fatal("label view reciprocal row/array ownership", labels, row, array)
	}
	// The builtin SQL row/array descriptors carry nominal identities. The
	// source relation owns its eight-column shape; catalog composite metadata
	// comes from reciprocal ownership, not an executable Composite recipe.
	if row.SQLType.ID() != 12101 || row.SQLType.AssociatedArray() != 12100 || array.SQLType.ID() != 12100 ||
		!row.SQLType.IsBuiltin() || !array.SQLType.IsBuiltin() {
		t.Fatal("label nominal builtin row/array identities", row.SQLType, array.SQLType)
	}
	if facts, err := pgcatalog.TypeFacts(t.Context(), read, row, sqltype.OutputEnvironment{}); err != nil || facts.Kind != "c" {
		t.Fatal("label catalog composite ownership", facts, err)
	}
	if database.RowType != 12272 || databaseRow.Array != 12271 || databaseRow.OwningRelation != database.Address() ||
		databaseArray.OwningRelation != database.Address() || databaseArray.SQLType.Element() != databaseRow.SQLType {
		t.Fatal("database view reciprocal row/array ownership", database, databaseRow, databaseArray)
	}
	for i, column := range labels.Columns {
		if column.OriginTable != 12099 || column.OriginAttribute != int16(i+1) {
			t.Fatal("label column source identity", i, column)
		}
	}
	for _, obsolete := range []ids.OID{12360, 12361} {
		if read.Get(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: obsolete}) != nil {
			t.Fatal("obsolete label relation retained at a reserved rule identity", obsolete)
		}
	}
	for _, fixed := range []ids.OID{3596, 3592} {
		if read.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: fixed}).ReturnRuleOID() != 0 {
			t.Fatal("fixed label catalog gained a view rule", fixed)
		}
	}
	counts := make(map[string]int)
	for address, descriptor := range read.Descriptors() {
		if descriptor.Namespace() == labels.Namespace() && (descriptor.Name() == labels.Name() || descriptor.Name() == database.Name()) && address.ClassID == ids.RelationClassID {
			counts[descriptor.Name()]++
		}
		if relation, ok := descriptor.(*catalog.Relation); ok {
			switch relation.ReturnRuleOID() {
			case 12360:
				if relation != database {
					t.Fatal("database rule owned by another source", relation)
				}
			case 12361:
				if relation != labels {
					t.Fatal("label rule owned by another source", relation)
				}
			case 12362:
				t.Fatal("obsolete label rule retained", relation)
			}
		}
	}
	if counts["pg_seclabels"] != 1 || counts["pg_stat_database"] != 1 {
		t.Fatal("duplicate/missing installed view sources", counts)
	}
	reserved := make(map[ids.OID]bool)
	for _, descriptor := range read.Descriptors() {
		catalog.ReserveDefinitionOIDs(reserved, descriptor)
	}
	if !reserved[12360] || !reserved[12361] || e.Catalog.NextOID() <= 12361 {
		t.Fatal("composed rules not reserved below allocation frontier", e.Catalog.NextOID())
	}
	if err := catalog.ValidateDefinitionOIDs(read); err != nil {
		t.Fatal(err)
	}
	for _, source := range []*catalog.Relation{database, labels} {
		if _, ok := pgcatalog.ProducerFor(source.Address().ObjectID); !ok || !pgcatalog.RegisteredSource(read, source) {
			t.Fatal("missing independent source dispatch", source.Name())
		}
	}
	for _, obsolete := range []ids.OID{12360, 12361} {
		if _, ok := pgcatalog.ProducerFor(obsolete); ok {
			t.Fatal("obsolete label producer retained", obsolete)
		}
	}
	// Exercise both genuine dispatch paths, not merely symbolic registration.
	s := enginetest.StartSession(t, e, nil)
	enginetest.TransactionRows(t, s, "SELECT count(*) FROM pg_catalog.pg_stat_database WHERE datid=0", 1)
	enginetest.TransactionRows(t, s, "SELECT count(*) FROM pg_catalog.pg_seclabels", 0)
	enginetest.TransactionRows(t, s, "SELECT count(*) FROM pg_rewrite WHERE oid=12360 AND ev_class=12212 AND rulename='_RETURN'", 1)
	enginetest.TransactionRows(t, s, "SELECT count(*) FROM pg_rewrite WHERE oid=12361 AND ev_class=12099 AND rulename='_RETURN'", 1)
	enginetest.TransactionRows(t, s, "SELECT count(*) FROM pg_rewrite WHERE oid=12362", 0)
	enginetest.TransactionRows(t, s, "SELECT count(*) FROM pg_depend WHERE classid=2618 AND objid=12360 AND refclassid=1259 AND refobjid=12212 AND deptype='i'", 1)
	enginetest.TransactionRows(t, s, "SELECT count(*) FROM pg_depend WHERE classid=2618 AND objid=12361 AND refclassid=1259 AND refobjid=12099 AND deptype='i'", 1)
	// Reject copied, foreign and obsolete source identities at the existing
	// capture boundary, including a cross-source attempt at either producer.
	foreign := engine.New()
	for _, source := range []*catalog.Relation{database, labels} {
		produce, _ := pgcatalog.ProducerFor(source.Address().ObjectID)
		in := pgcatalog.Input{Context: t.Context(), Catalog: read, Roles: e.Roles(), Owner: read.Owner(), Ref: e.Catalog.Domain.Ref(read.Epoch(), source.Address()), Source: source, Runtime: core.NewCatalogRuntime(core.CatalogRuntimeFields{Engine: e})}
		for _, replacement := range []*catalog.Relation{foreign.CatalogRead().MustRelation(source.Address()), database, labels} {
			if replacement == source {
				continue
			}
			bad := in
			bad.Source = replacement
			if rows, err := produce(bad); err == nil || rows != nil {
				t.Fatal("foreign/cross-source capture admitted", source.Name(), err)
			}
		}
		copy := *source
		in.Source = &copy
		if rows, err := produce(in); err == nil || rows != nil {
			t.Fatal("copied source admitted", source.Name(), err)
		}
		copy.ObjectAddress.ObjectID = 12360
		in.Ref = e.Catalog.Domain.Ref(read.Epoch(), copy.Address())
		if rows, err := produce(in); err == nil || rows != nil {
			t.Fatal("obsolete source identity admitted", source.Name(), err)
		}
	}
	if e.CatalogRead().State() != state {
		t.Fatal("source observations published catalog state")
	}
	enginetest.SessionSQL(t, s, "CREATE TABLE sepgsql_identity_fresh(id integer)")
	fresh := enginetest.RoleTableRef(t, e, "sepgsql_identity_fresh")
	if fresh.Address.ObjectID <= 12361 {
		t.Fatal("fresh allocation reused builtin reservation", fresh)
	}
	if read.State() != state || read.Get(labels.Address()) != labels || read.Get(database.Address()) != database ||
		read.Get(row.Address()) != row || read.Get(array.Address()) != array || labels.ReturnRuleOID() != 12361 || database.ReturnRuleOID() != 12360 {
		t.Fatal("observations/allocation mutated retained catalog ownership")
	}
	if err := catalog.ValidateDefinitionOIDs(e.CatalogRead()); err != nil {
		t.Fatal(err)
	}
}
