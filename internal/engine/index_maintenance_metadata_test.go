package engine

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
	"testing"
)

func indexMaintenanceMetadataFlags(index, parent ids.OID, n int16) indexMetadata {
	return indexMetadata{IndexRelID: index, IndRelID: parent, IndNatts: n, IndNKeyAtts: n, IndImmediate: true, IndIsValid: true, IndIsReady: true, IndIsLive: true}
}
func indexMaintenanceMetadata(t *testing.T, e *Engine, catalog CatalogRead, name string) (indexMetadata, indexesMetadata) {
	t.Helper()
	ref := roleTableRef(t, e, name)
	m, n, err := projectIndexMetadata(context.Background(), catalog, ref)
	if err != nil {
		t.Fatal(err)
	}
	return m, n
}

func TestIndexMaintenanceMetadataCompleteVectorsAndTrees(t *testing.T) {
	e := New()
	for _, q := range []string{"CREATE TABLE t(unused INT,n INT,s TEXT)", "CREATE INDEX ni ON t(n ASC NULLS LAST,n ASC NULLS FIRST,n DESC NULLS LAST,n DESC NULLS FIRST)", "CREATE INDEX ei ON t(lower(s),(n+(2-1)),n DESC NULLS LAST)", "CREATE TABLE q(n INT CONSTRAINT u UNIQUE NULLS NOT DISTINCT)", "CREATE TABLE d(n INT CONSTRAINT distinct_u UNIQUE)", "ALTER TABLE t DROP COLUMN unused"} {
		run(t, e, q)
	}
	capture := e.CatalogRead()
	ni, _ := btreeRoot(t, e, "ni")
	ei, _ := btreeRoot(t, e, "ei")
	m, n := indexMaintenanceMetadata(t, e, capture, "ni")
	want := indexMaintenanceMetadataFlags(ni.Address().ObjectID, ni.SQLIndex.Parent.ObjectID, 4)
	want.IndKey = []int16{2, 2, 2, 2}
	want.IndClass = []ids.OID{1978, 1978, 1978, 1978}
	want.IndCollation = []ids.OID{0, 0, 0, 0}
	want.IndOption = []int16{0, 2, 1, 3}
	if !reflect.DeepEqual(m, want) || n != (indexesMetadata{SchemaName: "public", TableName: "t", IndexName: "ni", IndexDef: "CREATE INDEX ni ON public.t USING btree (n, n NULLS FIRST, n DESC NULLS LAST, n DESC)"}) {
		t.Fatal("complete simple metadata", m, n, want)
	}
	expressions, names := indexMaintenanceMetadata(t, e, capture, "ei")
	expected := indexMaintenanceMetadataFlags(ei.Address().ObjectID, ei.SQLIndex.Parent.ObjectID, 3)
	expected.IndKey = []int16{0, 0, 2}
	expected.IndClass = []ids.OID{3126, 1978, 1978}
	expected.IndCollation = []ids.OID{100, 0, 0}
	expected.IndOption = []int16{0, 0, 1}
	expected.IndexExpressions = []expr.Expr{ei.SQLIndex.Keys[0].Derived.Definition, ei.SQLIndex.Keys[1].Derived.Definition}
	if !reflect.DeepEqual(expressions, expected) || names.IndexDef != "CREATE INDEX ei ON public.t USING btree (lower(s), ((n + (2 - 1))), n DESC NULLS LAST)" {
		t.Fatal("derived metadata lost original trees/providers", expressions, names, expected)
	}
	first := expressions.IndexExpressions[0].(functions.String)
	original := ei.SQLIndex.Keys[0].Derived.Definition.(functions.String)
	if &first.Args[0] != &original.Args[0] {
		t.Fatal("metadata copied an immutable analyzed function tree")
	}
	if reflect.DeepEqual(expressions.IndexExpressions[1], ei.SQLIndex.Keys[1].Derived.Execution) {
		t.Fatal("fixture or projection lost the original-vs-simplified distinction")
	}
	for _, name := range []string{"u", "distinct_u"} {
		index, _ := btreeRoot(t, e, name)
		got, n := indexMaintenanceMetadata(t, e, capture, name)
		want := indexMaintenanceMetadataFlags(index.Address().ObjectID, index.SQLIndex.Parent.ObjectID, 1)
		want.IndIsUnique = true
		want.IndNullsNotDistinct = name == "u"
		want.IndKey = []int16{1}
		want.IndClass = []ids.OID{1978}
		want.IndCollation = []ids.OID{0}
		want.IndOption = []int16{0}
		if !reflect.DeepEqual(got, want) || n.Tablespace != nil {
			t.Fatal("UNIQUE flags/vectors/tree nullability", got, n, want)
		}
	}
	m.IndKey[0] = 0
	m.IndClass[0] = 0
	m.IndCollation[0] = 999
	m.IndOption[0] = 7
	expressions.IndexExpressions[0] = nil
	again, _ := indexMaintenanceMetadata(t, e, capture, "ni")
	fresh, _ := indexMaintenanceMetadata(t, e, capture, "ei")
	if !reflect.DeepEqual(again, want) || fresh.IndexExpressions[0] == nil {
		t.Fatal("projection vectors/list alias descriptor storage")
	}
	run(t, e, "ALTER INDEX ni RENAME TO renamed_ni")
	run(t, e, "ALTER TABLE t RENAME COLUMN n TO renamed_n")
	run(t, e, "ALTER TABLE t RENAME TO renamed_t")
	old, oldNames, err := projectIndexMetadata(context.Background(), capture, e.catalog.Domain.Ref(capture.Epoch(), ni.Address()))
	if err != nil || !reflect.DeepEqual(old, want) || oldNames.IndexName != "ni" || oldNames.TableName != "t" {
		t.Fatal("historical capture consulted current catalog", old, oldNames, err)
	}
	current, currentNames := indexMaintenanceMetadata(t, e, e.CatalogRead(), "renamed_ni")
	if !reflect.DeepEqual(current, want) || currentNames.TableName != "renamed_t" || currentNames.IndexName != "renamed_ni" || currentNames.IndexDef != "CREATE INDEX renamed_ni ON public.renamed_t USING btree (renamed_n, renamed_n NULLS FIRST, renamed_n DESC NULLS LAST, renamed_n DESC)" {
		t.Fatal("current names or stable attributes lost", current, currentNames)
	}
	run(t, e, "DROP INDEX renamed_ni")
	records, _, err := indexCatalogMetadata(context.Background(), e.CatalogRead())
	if err != nil {
		t.Fatal(err)
	}
	for _, record := range records {
		if record.IndexRelID == ni.Address().ObjectID {
			t.Fatal("dropped metadata remained published")
		}
	}
	old, _, err = projectIndexMetadata(context.Background(), capture, e.catalog.Domain.Ref(capture.Epoch(), ni.Address()))
	if err != nil || !reflect.DeepEqual(old, want) {
		t.Fatal("drop mutated old capture", old, err)
	}
}

func TestIndexMaintenanceMetadataDomainBackingClasses(t *testing.T) {
	e := New()
	for _, q := range []string{
		"CREATE DOMAIN domain_n AS INT",
		"CREATE DOMAIN nested_n AS domain_n",
		"CREATE DOMAIN domain_s AS TEXT",
		`CREATE TABLE q(n nested_n CONSTRAINT un UNIQUE NULLS NOT DISTINCT,s domain_s COLLATE "C" CONSTRAINT us UNIQUE NULLS NOT DISTINCT)`,
		"INSERT INTO q VALUES(1,'a'),(NULL,NULL)",
	} {
		run(t, e, q)
	}
	un, _ := btreeRoot(t, e, "un")
	constraint := *un.SQLIndex.Constraint
	run(t, e, "ALTER INDEX un RENAME TO renamed_un")
	renamed, _ := btreeRoot(t, e, "renamed_un")
	if renamed.Address() != un.Address() || renamed.SQLIndex.Constraint.OID != constraint.OID || renamed.SQLIndex.Constraint.Index != constraint.Index {
		t.Fatal("domain backing rename lost real constraint/index link")
	}
	check := func(name string, attribute int16, class, collation ids.OID) {
		t.Helper()
		index, _ := btreeRoot(t, e, name)
		got, _ := indexMaintenanceMetadata(t, e, e.CatalogRead(), name)
		want := indexMaintenanceMetadataFlags(index.Address().ObjectID, index.SQLIndex.Parent.ObjectID, 1)
		want.IndIsUnique, want.IndNullsNotDistinct = true, true
		want.IndKey = []int16{attribute}
		want.IndClass = []ids.OID{class}
		want.IndCollation = []ids.OID{collation}
		want.IndOption = []int16{0}
		if !reflect.DeepEqual(got, want) || index.SQLIndex.Keys[0].Class.OID() != uint32(class) || index.SQLIndex.Constraint.Index != index.Address() {
			t.Fatal("real domain backing descriptor/vector truth", name, got, want)
		}
	}
	for _, q := range []string{"REINDEX INDEX renamed_un", "REINDEX INDEX us", "REINDEX TABLE q"} {
		run(t, e, q)
		check("renamed_un", 1, 1978, 0)
		check("us", 2, 3126, 950)
	}
	beforeType, oldRoot := btreeRoot(t, e, "renamed_un")
	oldConstraint := *beforeType.SQLIndex.Constraint
	run(t, e, "ALTER TABLE q ALTER n TYPE BIGINT USING n::bigint")
	afterType, typedRoot := btreeRoot(t, e, "renamed_un")
	if afterType.Address() == beforeType.Address() || afterType.SQLIndex.Constraint.OID == oldConstraint.OID || afterType.SQLIndex.Constraint.Index != afterType.Address() || typedRoot == oldRoot || beforeType.SQLIndex.Keys[0].Class.OID() != 1978 || beforeType.SQLIndex.Constraint.Index != oldConstraint.Index {
		t.Fatal("TYPE lost candidate identities/backing link or mutated retained class/root")
	}
	if !reflect.DeepEqual(btreeKeys(t, oldRoot), []string{"1", "NULL"}) {
		t.Fatal("TYPE mutated retained domain backing tuples")
	}
	for _, q := range []string{"REINDEX INDEX renamed_un", "REINDEX TABLE q"} {
		run(t, e, q)
		check("renamed_un", 1, 3124, 0)
		check("us", 2, 3126, 950)
	}
	btreeWantKeys(t, e, "renamed_un", "1", "NULL")
	btreeWantKeys(t, e, "us", "a", "NULL")
	lockEmpty(t, e)
}

func TestIndexMaintenanceMetadataCollationAndRepeatedKeys(t *testing.T) {
	e := New()
	for _, q := range []string{"CREATE TABLE t(unused INT,n INT,s TEXT)", `CREATE INDEX ci ON t((s COLLATE "C"),(s COLLATE "POSIX"),s)`, "CREATE INDEX repeated ON t(" + strings.TrimSuffix(strings.Repeat("n,", 32), ",") + ")", "ALTER TABLE t DROP COLUMN unused"} {
		run(t, e, q)
	}
	index, _ := btreeRoot(t, e, "ci")
	got, names := indexMaintenanceMetadata(t, e, e.CatalogRead(), "ci")
	want := indexMaintenanceMetadataFlags(index.Address().ObjectID, index.SQLIndex.Parent.ObjectID, 3)
	want.IndKey = []int16{3, 3, 3}
	want.IndClass = []ids.OID{3126, 3126, 3126}
	want.IndCollation = []ids.OID{950, 951, 100}
	want.IndOption = []int16{0, 0, 0}
	if !reflect.DeepEqual(got, want) || names.IndexDef != `CREATE INDEX ci ON public.t USING btree (s COLLATE "C", s COLLATE "POSIX", s)` {
		t.Fatal("collation provider/simple attribute identity", got, names, want)
	}
	run(t, e, "ALTER TABLE t RENAME COLUMN s TO renamed_s")
	renamed, renamedNames := indexMaintenanceMetadata(t, e, e.CatalogRead(), "ci")
	if !reflect.DeepEqual(renamed, want) || renamedNames.IndexDef != `CREATE INDEX ci ON public.t USING btree (renamed_s COLLATE "C", renamed_s COLLATE "POSIX", renamed_s)` {
		t.Fatal("collated stable attribute changed across rename", renamed, renamedNames)
	}
	repeated, _ := btreeRoot(t, e, "repeated")
	m, _ := indexMaintenanceMetadata(t, e, e.CatalogRead(), "repeated")
	expected := indexMaintenanceMetadataFlags(repeated.Address().ObjectID, repeated.SQLIndex.Parent.ObjectID, 32)
	expected.IndKey = make([]int16, 32)
	expected.IndClass = make([]ids.OID, 32)
	expected.IndCollation = make([]ids.OID, 32)
	expected.IndOption = make([]int16, 32)
	for i := range expected.IndKey {
		expected.IndKey[i] = 2
		expected.IndClass[i] = 1978
	}
	if !reflect.DeepEqual(m, expected) {
		t.Fatal("32 repeated keys changed natts/order/provider vectors", m, expected)
	}
}

func TestIndexMaintenanceMetadataDomainsAndEnumeration(t *testing.T) {
	e := New()
	for _, q := range []string{"CREATE DOMAIN di AS INT", "CREATE DOMAIN ditwo AS di", "CREATE DOMAIN dt AS VARCHAR(4)", "CREATE TYPE mood AS ENUM('z','a')", "CREATE DOMAIN dm AS mood", "CREATE TABLE t(i ditwo,s dt,m dm)", "CREATE INDEX ix ON t(i,s,m)"} {
		run(t, e, q)
	}
	index, _ := btreeRoot(t, e, "ix")
	got, _ := indexMaintenanceMetadata(t, e, e.CatalogRead(), "ix")
	want := indexMaintenanceMetadataFlags(index.Address().ObjectID, index.SQLIndex.Parent.ObjectID, 3)
	want.IndKey = []int16{1, 2, 3}
	want.IndClass = []ids.OID{1978, 3126, 10069}
	want.IndCollation = []ids.OID{0, 100, 0}
	want.IndOption = []int16{0, 0, 0}
	if !reflect.DeepEqual(got, want) {
		t.Fatal("domain/enum metadata substituted declared type OIDs", got, want)
	}
	// An explicit empty table ACL cannot hide a structural metadata record.
	parent := e.catalog.Domain.Ref(e.CatalogRead().Epoch(), index.SQLIndex.Parent)
	if err := e.SetObjectACL(context.Background(), parent, authorization.ACLValue{Explicit: true}); err != nil {
		t.Fatal(err)
	}
	records, names, err := indexCatalogMetadata(context.Background(), e.CatalogRead())
	if err != nil || len(records) != 1 || len(names) != 1 || !reflect.DeepEqual(records[0], want) {
		t.Fatal("metadata enumeration filtered by ACL or mixed captures", records, names, err)
	}
	old := e.CatalogRead()
	if err := e.Reset(context.Background()); err != nil {
		t.Fatal(err)
	}
	records, _, err = indexCatalogMetadata(context.Background(), old)
	if err != nil || len(records) != 1 || !reflect.DeepEqual(records[0], want) {
		t.Fatal("old metadata capture became mutable after replacement", records, err)
	}
	empty, _, err := indexCatalogMetadata(context.Background(), e.CatalogRead())
	if err != nil || len(empty) != 0 {
		t.Fatal("replacement retained old published metadata", empty, err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := indexCatalogMetadata(canceled, old); !errors.Is(err, context.Canceled) {
		t.Fatal("metadata ignored cancellation", err)
	}
	foreign := New()
	if _, _, err := projectIndexMetadata(context.Background(), old, foreign.catalog.Domain.Ref(old.Epoch(), index.Address())); err == nil {
		t.Fatal("foreign ref accepted by capture")
	}
	if _, _, err := projectIndexMetadata(context.Background(), old, e.catalog.Domain.Ref(old.Epoch(), ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 123456})); err == nil {
		t.Fatal("missing ref accepted by capture")
	}
	if _, _, err := projectIndexMetadata(context.Background(), old, e.catalog.Domain.Ref(old.Epoch(), parent.Address)); err == nil {
		t.Fatal("non-index ref accepted by capture")
	}
	if p, err := sqltype.BindOrdering(index.Columns[2].Usage); err != nil || p.DefaultBtreeOpclass() != 10069 {
		t.Fatal("enum central provider was not retained", p, err)
	}
}
