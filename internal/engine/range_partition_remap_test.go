package engine

import (
	"bytes"
	"context"
	"database/sql/driver"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/engine/typedef"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func TestRangePartitionRemapSelfCatalogAndRetainedCursor(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	for _, query := range []string{
		"CREATE SEQUENCE seq",
		"CREATE TABLE root(k INT,payload INT DEFAULT nextval('seq'),CONSTRAINT positive CHECK(payload>0)) PARTITION BY RANGE((k+0))",
		"ALTER SEQUENCE seq OWNED BY root.payload",
		"CREATE TABLE leaf PARTITION OF root FOR VALUES FROM(0) TO(10)",
		"CREATE UNIQUE INDEX leaf_payload ON leaf(payload)",
		"CREATE TABLE coupled(k INT DEFAULT nextval('seq'),CONSTRAINT coupled_positive CHECK(k>0))",
		"CREATE VIEW rooted AS SELECT k,payload FROM root",
		"INSERT INTO root(k) VALUES(1),(2)",
	} {
		roleExecute(t, e, op, query)
	}
	roleExecute(t, e, op, "CREATE TEMP SEQUENCE retained_temp CACHE 10")
	tempAST := statement(t, "SELECT nextval('retained_temp')")
	tempDescription, err := e.Describe(op, tempAST)
	if err != nil {
		t.Fatal(err)
	}
	tempAddress := tempDescription.sequences.Constants[0].Ref.Address
	tempNamespace, tempToken := op.Session.TemporaryNamespace, op.Session.SessionToken
	tempDescriptor, tempNamespaceDescriptor := e.catalog.state.Get(tempAddress), e.catalog.state.Get(tempNamespace)
	tempDemand := func(want int64) {
		t.Helper()
		scope, err := e.StartStatement(op)
		if err != nil {
			t.Fatal(err)
		}
		scope.Expected = &tempDescription
		result, err := scope.Execute(tempAST, 0, nil)
		err = scope.Finish(err)
		if err != nil {
			t.Fatal(err)
		}
		got := tuples(result)
		if result.Rows != nil {
			result.Rows.Close()
		}
		if !reflect.DeepEqual(got, [][]driver.Value{{want}}) {
			t.Fatal("retained TEMP cached demand", got, want)
		}
	}
	tempDemand(1)
	tempGenerator, tempMemory := e.storage.Sequences[tempAddress], payloadOf(op.Session).sequences.Memory(tempAddress)
	roleExecute(t, e, op, "CREATE TEMP TABLE retained_table(k INT,payload INT DEFAULT nextval('retained_temp'),g INT GENERATED ALWAYS AS(payload+1) STORED,CONSTRAINT retained_positive CHECK(payload>0))")
	roleExecute(t, e, op, "CREATE UNIQUE INDEX retained_table_key ON retained_table(k) WHERE payload>0")
	roleExecute(t, e, op, "CREATE INDEX retained_table_expression ON retained_table((k+0)) WHERE payload>0")
	roleExecute(t, e, op, "INSERT INTO retained_table(k) VALUES(1)")
	tempGeneratorState, tempMemoryState := *tempGenerator, *tempMemory
	tempTableAddress := e.catalog.state.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: tempNamespace, Name: "retained_table"})
	tempIndexAddress := e.catalog.state.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: tempNamespace, Name: "retained_table_key"})
	tempTableDescriptor, tempIndexDescriptor := e.catalog.state.Get(tempTableAddress), e.catalog.state.Get(tempIndexAddress)
	tempTableStore, tempIndexRoot := e.storage.Relations[tempTableAddress], e.storage.IndexRoots[tempIndexAddress]
	tempExpressionAddress := e.catalog.state.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: tempNamespace, Name: "retained_table_expression"})
	tempExpressionDescriptor, tempExpressionRoot := e.catalog.state.Get(tempExpressionAddress), e.storage.IndexRoots[tempExpressionAddress]
	tempVersions := slices.Clone(tempTableStore.Versions)
	tempTableCursor := roleExecute(t, e, op, "SELECT k,payload FROM retained_table")
	retained := e.CatalogRead()
	root := rangePartitionRelation(t, e, "root")
	leaf := rangePartitionRelation(t, e, "leaf")
	oldKeys := slices.Clone(root.Partition.Keys)
	oldChildren := slices.Clone(root.Partition.Children)
	oldLower := slices.Clone(leaf.Membership.Lower)
	oldUpper := slices.Clone(leaf.Membership.Upper)
	oldChecks := slices.Clone(leaf.Constraints.Check)
	cursor := roleExecute(t, e, op, "SELECT k,payload FROM root")
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := e.ReplaceCatalog(canceled, e, retained); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if e.CatalogRead().State() != retained.State() {
		t.Fatal("canceled remap installed a candidate")
	}
	changes, err := e.ReplaceCatalog(context.Background(), e, retained)
	if err != nil || !changes.Replacement || e.CatalogRead().Epoch() != retained.Epoch()+1 {
		t.Fatal(changes, err)
	}
	if op.Session.SessionToken != tempToken || op.Session.TemporaryNamespace != tempNamespace || e.catalog.state.Get(tempAddress) != tempDescriptor || e.catalog.state.Get(tempNamespace) != tempNamespaceDescriptor || e.storage.Sequences[tempAddress] != tempGenerator || payloadOf(op.Session).sequences.Memory(tempAddress) != tempMemory || *tempGenerator != tempGeneratorState || *tempMemory != tempMemoryState {
		t.Fatal("hierarchy self remap changed exact live TEMP identity/cache")
	}
	tempDemand(3)
	if e.catalog.state.Get(tempTableAddress) != tempTableDescriptor || e.catalog.state.Get(tempIndexAddress) != tempIndexDescriptor || e.storage.Relations[tempTableAddress] != tempTableStore || e.storage.IndexRoots[tempIndexAddress] != tempIndexRoot || !reflect.DeepEqual(tempTableStore.Versions, tempVersions) {
		t.Fatal("hierarchy replacement reminted retained TEMP table definitions/storage/index")
	}
	if e.catalog.state.Get(tempExpressionAddress) != tempExpressionDescriptor || e.storage.IndexRoots[tempExpressionAddress] != tempExpressionRoot {
		t.Fatal("hierarchy replacement reminted retained TEMP expression/predicate index")
	}
	if !reflect.DeepEqual(tuples(tempTableCursor), [][]driver.Value{{int64(1), int64(2)}}) {
		t.Fatal("TEMP table retained cursor lost exact prefix")
	}
	roleExecute(t, e, op, "INSERT INTO retained_table(k) VALUES(2)")
	for _, tc := range []struct{ query, code, message, detail, constraint string }{
		{"INSERT INTO retained_table(k,payload) VALUES(2,9)", "23505", `duplicate key value violates unique constraint "retained_table_key"`, "Key (k)=(2) already exists.", "retained_table_key"},
		{"INSERT INTO retained_table(k,payload) VALUES(3,0)", "23514", `new row for relation "retained_table" violates check constraint "retained_positive"`, "Failing row contains (3, 0, 1).", "retained_positive"},
	} {
		_, err := e.Execute(op, statement(t, tc.query), 0, nil)
		var diagnostic *sqlstate.Error
		want := &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: tc.code, Message: tc.message, Detail: tc.detail, Schema: tempNamespaceDescriptor.Name(), Table: "retained_table", Constraint: tc.constraint}
		if !errors.As(err, &diagnostic) || !reflect.DeepEqual(diagnostic, want) {
			t.Fatal("retained TEMP enforcement diagnostic", diagnostic, want, err)
		}
	}
	if !reflect.DeepEqual(tuples(roleExecute(t, e, op, "SELECT k,payload,g FROM retained_table ORDER BY k")), [][]driver.Value{{int64(1), int64(2), int64(3)}, {int64(2), int64(4), int64(5)}}) {
		t.Fatal("retained TEMP default/generated/CHECK/predicate/index demand changed")
	}
	sequence := rangePartitionRelation(t, e, "seq")
	currentRoot := rangePartitionRelation(t, e, "root")
	expectedOwned := currentRoot.Address()
	expectedOwned.SubID = currentRoot.Attributes[currentRoot.Index["payload"]]
	if sequence.Sequence.Owned != expectedOwned || sequence.Owner != currentRoot.Owner {
		t.Fatal("self remap changed virtual column ownership identity")
	}
	if !reflect.DeepEqual(root.Partition.Keys, oldKeys) || !reflect.DeepEqual(root.Partition.Children, oldChildren) || !reflect.DeepEqual(leaf.Membership.Lower, oldLower) || !reflect.DeepEqual(leaf.Membership.Upper, oldUpper) || !reflect.DeepEqual(leaf.Constraints.Check, oldChecks) {
		t.Fatal("self remap mutated retained descriptors")
	}
	if !reflect.DeepEqual(tuples(cursor), [][]driver.Value{{int64(1), int64(1)}, {int64(2), int64(2)}}) {
		t.Fatal("retained cursor lost its captured physical prefixes")
	}
	roleExecute(t, e, op, "INSERT INTO root(k) VALUES(3)")
	roleExecute(t, e, op, "INSERT INTO coupled DEFAULT VALUES")
	result := roleExecute(t, e, op, "SELECT k,payload FROM rooted ORDER BY k")
	if !reflect.DeepEqual(tuples(result), [][]driver.Value{{int64(1), int64(1)}, {int64(2), int64(2)}, {int64(3), int64(3)}}) {
		t.Fatal("fresh key/default/CHECK/view demand failed")
	}
	result = roleExecute(t, e, op, "SELECT k FROM coupled")
	if !reflect.DeepEqual(tuples(result), [][]driver.Value{{int64(4)}}) {
		t.Fatal("nonpartition coupled default was not retagged")
	}
	_, err = e.Execute(op, statement(t, "INSERT INTO root(k,payload) VALUES(4,-1)"), 0, nil)
	if err == nil {
		t.Fatal("reowned inherited CHECK stopped enforcing")
	}
	result = roleExecute(t, e, op, "SELECT k,payload FROM leaf ORDER BY k")
	if !reflect.DeepEqual(tuples(result), [][]driver.Value{{int64(1), int64(1)}, {int64(2), int64(2)}, {int64(3), int64(3)}}) {
		t.Fatal("failed CHECK mutated remapped storage")
	}
	for _, name := range []string{"root", "leaf"} {
		if err := ValidatePartitionDescriptor(e.CatalogRead(), rangePartitionRelation(t, e, name)); err != nil {
			t.Fatal(err)
		}
	}
}

func TestRangePartitionRemapFailedCandidatePreservesTarget(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	roleExecute(t, e, op, "CREATE TABLE root(k INT) PARTITION BY RANGE(k)")
	roleExecute(t, e, op, "CREATE TABLE leaf PARTITION OF root FOR VALUES FROM(0) TO(10)")
	roleExecute(t, e, op, "INSERT INTO root VALUES(1)")
	before := e.CatalogRead()
	root := rangePartitionRelation(t, e, "root")
	state := catalog.CloneState(before.State())
	candidate := *root
	partition := *root.Partition
	partition.Children = append(slices.Clone(partition.Children), partition.Children[0])
	candidate.Partition = &partition
	state.SetDescriptor(root.Address(), &candidate)
	if _, err := e.ReplaceCatalog(context.Background(), e, e.catalog.read(state)); err == nil {
		t.Fatal("malformed replacement accepted")
	}
	if e.CatalogRead().State() != before.State() {
		t.Fatal("malformed remap installed")
	}
	result := roleExecute(t, e, op, "SELECT k FROM root")
	if !reflect.DeepEqual(tuples(result), [][]driver.Value{{int64(1)}}) {
		t.Fatal("failed remap changed rows")
	}
}

func TestRangePartitionPublicationDeferredRestoreAndCancellation(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	roleExecute(t, e, op, "CREATE ROLE named_owner LOGIN")
	roleExecute(t, e, op, "SET SESSION AUTHORIZATION named_owner")
	withPublicSchemaUnitOwner(t, e, "named_owner", func() {
		for _, query := range []string{
			"CREATE TABLE target(k REGCLASS)",
			"CREATE TABLE alternate(k INT)",
			"CREATE TABLE root(k INT,payload REGCLASS DEFAULT 'target'::REGCLASS) PARTITION BY RANGE(k)",
			"CREATE TABLE middle PARTITION OF root DEFAULT PARTITION BY RANGE(k)",
			"CREATE TABLE leaf PARTITION OF middle DEFAULT",
			"ALTER TABLE target ALTER COLUMN k SET DEFAULT 'root'::REGCLASS",
			"INSERT INTO root(k) VALUES(1)",
		} {
			roleExecute(t, e, op, query)
		}
	})
	roleExecute(t, e, op, "SET SESSION AUTHORIZATION postgres")
	var stream bytes.Buffer
	if err := e.Dump(context.Background(), &stream); err != nil {
		t.Fatal(err)
	}
	target := New()
	targetOp, targetDone := roleOperation(t, target, "postgres")
	defer targetDone()
	roleExecute(t, target, targetOp, "CREATE ROLE named_owner LOGIN")
	roleExecute(t, target, targetOp, "CREATE TABLE sentinel(k INT)")
	roleExecute(t, target, targetOp, "INSERT INTO sentinel VALUES(9)")
	before := target.CatalogRead()
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := target.Restore(canceled, bytes.NewReader(stream.Bytes())); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if target.CatalogRead().State() != before.State() {
		t.Fatal("canceled hierarchy replay swapped target")
	}
	invalid := append(slices.Clone(stream.Bytes()), []byte("\nALTER TABLE public.root ALTER COLUMN payload SET DEFAULT NULL;\n")...)
	if err := target.Restore(context.Background(), bytes.NewReader(invalid)); err == nil {
		t.Fatal("nondeferrable hierarchy default accepted as importer replay")
	}
	if target.CatalogRead().State() != before.State() {
		t.Fatal("failed trusted replay swapped target")
	}
	if !reflect.DeepEqual(tuples(roleExecute(t, target, targetOp, "SELECT k FROM sentinel")), [][]driver.Value{{int64(9)}}) {
		t.Fatal("failed replay changed sentinel")
	}
	if err := target.Restore(context.Background(), bytes.NewReader(stream.Bytes())); err != nil {
		t.Fatal(err)
	}
	result := roleExecute(t, target, targetOp, "SELECT k,payload='target'::REGCLASS FROM root")
	if !reflect.DeepEqual(tuples(result), [][]driver.Value{{int64(1), true}}) {
		t.Fatal("deferred virtual default did not restore provider identity")
	}
	var again bytes.Buffer
	if err := target.Dump(context.Background(), &again); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(stream.Bytes(), again.Bytes()) {
		t.Fatal("semantic hierarchy replay changed deterministic schema stream")
	}
	roleExecute(t, target, targetOp, "SET SESSION AUTHORIZATION named_owner")
	roleExecute(t, target, targetOp, "INSERT INTO root(k) VALUES(2)")
	roleError(t, target, targetOp, "ALTER TABLE root ALTER COLUMN payload SET DEFAULT 'alternate'::REGCLASS", "0A000", "altering partition hierarchies is unsupported", "")
	roleExecute(t, target, targetOp, "SET SESSION AUTHORIZATION postgres")
	roleError(t, target, targetOp, "ALTER TABLE root ALTER COLUMN payload SET DEFAULT 'alternate'::REGCLASS", "0A000", "altering partition hierarchies is unsupported", "")
	if !reflect.DeepEqual(tuples(roleExecute(t, target, targetOp, "SELECT k,payload='target'::REGCLASS FROM root ORDER BY k")), [][]driver.Value{{int64(1), true}, {int64(2), true}}) {
		t.Fatal("ordinary hierarchy ALTER bypass changed defaults")
	}
	if err := target.Restore(context.Background(), strings.NewReader("nonsense")); err == nil {
		t.Fatal("malformed replay accepted")
	}
}

func TestRangePartitionRemapExplicitCollation(t *testing.T) {
	e := New()
	view := e.CatalogRead()
	text, _ := view.LookupType(sqltype.Text)
	provider := collationUnitDefinition(e, 90000, "partition_provider")
	capability, err := provider.Usage()
	if err != nil {
		t.Fatal(err)
	}
	usage := text.Usage()
	usage.Collation = capability
	class, ok := sqltype.DefaultIndexClass(text)
	if !ok {
		t.Fatal("missing text ordering class")
	}
	comparison, err := sqltype.BindIndexOrdering(usage, class)
	if err != nil {
		t.Fatal(err)
	}
	source := &catalog.Relation{
		Partition: &catalog.PartitionDefinition{Keys: []catalog.PartitionKeyEntry{
			{Attribute: 1, Usage: usage, Class: class, Comparison: comparison, Collation: uint32(provider.OID)},
			{Attribute: 2, Usage: usage, Class: class, Comparison: comparison},
		}},
	}
	sourceKeys := slices.Clone(source.Partition.Keys)
	imported := *provider
	imported.OID = 90001
	// The old identity belongs to the retained live overlay, not the import.
	temporary := *provider
	temporary.ObjectName = "retained_temp_provider"
	temporary.ObjectNamespace = ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 90002}
	temporaryBefore := temporary
	next := catalog.CloneState(view.State())
	next.SetDescriptor(provider.Address(), &temporary)
	next.SetDescriptor(imported.Address(), &imported)
	mapping := map[ids.ObjectAddress]ids.ObjectAddress{provider.Address(): imported.Address()}
	collation := typedef.RemappedCollationCapabilities(next, mapping)
	for _, typesOnly := range []bool{false, true} {
		mapped, err := stored.RemapPartitionDescriptor(e.catalog.read(next), source,
			func(ref ids.ObjectRef) ids.ObjectRef { return ref },
			func(typ *sqltype.Descriptor) *sqltype.Descriptor { return typ },
			mapping, typesOnly, collation)
		if err != nil {
			t.Fatal(err)
		}
		if mapped == source || mapped.Partition == source.Partition {
			t.Fatal("remap reused mutable source descriptor")
		}
		if !reflect.DeepEqual(source.Partition.Keys, sourceKeys) {
			t.Fatal("remap mutated source keys")
		}
		if next.Get(provider.Address()) != &temporary || !reflect.DeepEqual(temporary, temporaryBefore) {
			t.Fatal("remap changed exact retained TEMP descriptor")
		}
		wantCapability, err := imported.Usage()
		if err != nil {
			t.Fatal(err)
		}
		for i, key := range mapped.Partition.Keys {
			if key.Usage.Collation != wantCapability {
				t.Fatal("remap lost imported comparison capability", typesOnly, i, key.Usage.Collation, wantCapability)
			}
			for _, witness := range []struct {
				left, right string
				want        int
			}{{"a", "m", -1}, {"m", "a", 1}, {"m", "m", 0}} {
				actual, err := key.Comparison.Compare(context.Background(),
					sqltype.String(text, witness.left), sqltype.String(text, witness.right), sqltype.InputEnvironment{})
				if err != nil || actual != witness.want {
					t.Fatal("remapped comparison changed independent text ordering", typesOnly, i, witness, actual, err)
				}
			}
			if i == 0 && key.Collation != key.Usage.Collation.ID {
				t.Fatal("explicit raw identity disagrees with comparison capability", typesOnly, key.Collation, key.Usage.Collation.ID)
			}
			if i == 1 && key.Collation != 0 {
				t.Fatal("implicit key gained an explicit collation clause", key.Collation)
			}
		}
	}
}
