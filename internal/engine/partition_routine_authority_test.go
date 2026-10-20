package engine

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// SQL only constructs authentic bound owners. Assertions exercise the private
// recipe/cache/consumed-graph boundary, not public SQL results.
func TestPartitionRoutineOriginalAuthority(t *testing.T) {
	e := New()
	admin, done := roleOperation(t, e, "postgres")
	defer done()
	roleExecute(t, e, admin, "CREATE TABLE partition_original(v text) PARTITION BY HASH ((bit_length(v)))")
	roleExecute(t, e, admin, "CREATE TABLE partition_plain(v text) PARTITION BY HASH (v)")
	roleExecute(t, e, admin, "CREATE ROLE partition_original_reader LOGIN")
	reader, readerDone := roleOperation(t, e, "partition_original_reader")
	defer readerDone()
	capture := func(op *Operation) AuthorizationView {
		t.Helper()
		e.mu.Lock()
		auth, err := e.authorizationLocked(op)
		e.mu.Unlock()
		if err != nil {
			t.Fatal(err)
		}
		return auth
	}
	auth := capture(reader)
	var owner *catalog.Relation
	for _, descriptor := range auth.Catalog.Descriptors() {
		if relation, ok := descriptor.(*catalog.Relation); ok && relation.Name() == "partition_original" {
			owner = relation
		}
	}
	if owner == nil || owner.Partition.Keys[0].Derived == nil {
		t.Fatal("missing genuinely bound partition owner")
	}
	original := owner.Partition.Keys[0].Derived
	refs := slices.Clone(original.Dependencies)
	for _, id := range []ids.OID{1811, 1374, 141} {
		if !slices.ContainsFunc(refs, func(ref ids.ObjectRef) bool {
			return ref.Address == (ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: id})
		}) {
			t.Fatal("original outer/body reference missing", id)
		}
	}
	text := func(raw string) sqltype.Value {
		t.Helper()
		value, err := owner.Columns[0].Usage.Type.Input(context.Background(), raw, -1)
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
	row := []sqltype.Value{text("abc")}
	sentinel := []sqltype.Value{text("outer")}
	newEvaluation := func(auth AuthorizationView) EvalContext {
		scope := &StatementScope{routineAuth: auth}
		return EvalContext{evalStatement: evalStatement{Context: context.Background(), statementScope: scope, catalog: auth.Catalog}, evalFrame: evalFrame{Row: sentinel, Scope: 7, Frame: 9}, evalAdmission: evalAdmission{routineAuthority: &auth}}
	}
	for _, descriptor := range auth.Catalog.Descriptors() {
		if plain, ok := descriptor.(*catalog.Relation); ok && plain.Name() == "partition_plain" {
			local := newEvaluation(auth)
			var buffer [32]catalog.PartitionDatum
			if _, err := partitionRowKey(plain, row, &local, buffer[:]); err != nil {
				t.Fatal("plain key demand", err)
			}
			if local.statementScope.partitionPrograms != nil || local.statementScope.routinePrograms != nil {
				t.Fatal("plain key initialized derived preparation")
			}
		}
	}
	evaluation := newEvaluation(auth)
	value, err := evaluatePartitionRecipe(&evaluation, owner, 0, row)
	if err != nil || value.IsNull() {
		t.Fatal("intact original recipe", value, err)
	}
	if !reflect.DeepEqual(evaluation.Row, sentinel) || evaluation.Scope != 7 || evaluation.Frame != 9 || evaluation.routineConsumer != nil {
		t.Fatal("partition evaluation changed enclosing row or recipe context")
	}
	if len(evaluation.statementScope.partitionPrograms) != 1 || len(evaluation.statementScope.routinePrograms) != 1 {
		t.Fatal("missing statement-owned successful preparation")
	}
	if _, err := evaluatePartitionRecipe(&evaluation, owner, 0, row); err != nil || len(evaluation.statementScope.routinePrograms) != 1 {
		t.Fatal("repeated tuple reinitialized consumed program", err)
	}
	probe := func(name string, dependencies []ids.ObjectRef) {
		t.Helper()
		t.Run(name, func(t *testing.T) {
			copyOwner, partition, key := *owner, *owner.Partition, *original
			partition.Keys = slices.Clone(partition.Keys)
			copyOwner.Partition = &partition
			partition.Keys[0].Derived = &key
			key.Dependencies = dependencies
			local := newEvaluation(auth)
			if _, err := evaluatePartitionRecipe(&local, &copyOwner, 0, row); err == nil {
				t.Fatal("invalid original lifetime was rescued")
			}
			if len(local.statementScope.partitionPrograms) != 0 || len(local.statementScope.routinePrograms) != 0 {
				t.Fatal("failed initialization published a partial program")
			}
			if !reflect.DeepEqual(local.Row, sentinel) || local.Scope != 7 || local.Frame != 9 {
				t.Fatal("failed evaluation changed enclosing context")
			}
		})
	}
	probe("missing", nil)
	probe("empty", []ids.ObjectRef{})
	for _, id := range []ids.OID{1811, 1374, 141} {
		missing := slices.DeleteFunc(slices.Clone(refs), func(ref ids.ObjectRef) bool {
			return ref.Address.ObjectID == id && ref.Address.ClassID == ids.ProcedureClassID
		})
		probe(fmt.Sprintf("omitted_%d", id), missing)
	}
	stale := slices.Clone(refs)
	for i := range stale {
		stale[i].Epoch++
	}
	probe("stale_epoch", stale)
	foreign := slices.Clone(refs)
	other := New()
	for i := range foreign {
		foreign[i] = other.catalog.Domain.Ref(foreign[i].Epoch, foreign[i].Address)
	}
	probe("foreign_equal_address", foreign)
	otherAdmin, otherDone := roleOperation(t, other, "postgres")
	defer otherDone()
	other.mu.Lock()
	otherAuth, err := other.authorizationLocked(otherAdmin)
	other.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	retagged, _, err := retagBoundTypes(otherAuth.Catalog, original.Definition, func(old *sqltype.Descriptor) *sqltype.Descriptor {
		if old == nil {
			return nil
		}
		current, _ := otherAuth.Catalog.LookupType(old.ID())
		return current
	})
	if err != nil {
		t.Fatal(err)
	}
	var retagAdmission routineAdmissionProgram
	retagEvaluation := newEvaluation(otherAuth)
	if _, err := prepareIndexRecipe(&retagEvaluation, retagged, refs, owner, &retagAdmission, true); err == nil {
		t.Fatal("retag manufactured destination original lifetime")
	}
	// A genuinely bound old expression must reject a newer declaration even
	// when its address, engine and epoch all still match.
	staleAuth := auth
	state := *auth.Catalog.State()
	state.CloneDescriptors()
	address := ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 1811}
	declaration := *state.MustProcedure(address)
	declaration.ObjectRevision++
	state.SetDescriptor(address, &declaration)
	staleAuth.Catalog = staleAuth.Catalog.WithState(&state)
	staleEvaluation := newEvaluation(staleAuth)
	if _, err := evaluatePartitionRecipe(&staleEvaluation, owner, 0, row); err == nil {
		t.Fatal("stale declaration revision was rescued by address")
	}
	// A sibling's freshly selected manifest cannot borrow the already admitted
	// owner's cache entry, even within the same actor/statement phase.
	sibling, siblingPartition, siblingKey := *owner, *owner.Partition, *original
	siblingPartition.Keys = slices.Clone(siblingPartition.Keys)
	sibling.Partition = &siblingPartition
	siblingPartition.Keys[0].Derived = &siblingKey
	siblingKey.Dependencies = []ids.ObjectRef{}
	if _, err := evaluatePartitionRecipe(&evaluation, &sibling, 0, row); err == nil || len(evaluation.statementScope.partitionPrograms) != 1 {
		t.Fatal("sibling borrowed an admitted owner's manifest")
	}
	multiOwner, multiPartition := *owner, *owner.Partition
	multiPartition.Keys = append(slices.Clone(multiPartition.Keys), siblingPartition.Keys[0])
	multiOwner.Partition = &multiPartition
	multiEvaluation := newEvaluation(auth)
	if _, err := evaluatePartitionRecipe(&multiEvaluation, &multiOwner, 0, row); err == nil {
		t.Fatal("later key's missing original manifest was ignored")
	}
	if len(multiEvaluation.statementScope.partitionPrograms) != 0 || len(multiEvaluation.statementScope.routinePrograms) != 0 {
		t.Fatal("failed multi-key preparation published an earlier partial graph")
	}
	roleExecute(t, e, admin, "REVOKE EXECUTE ON FUNCTION pg_catalog.octet_length(text) FROM PUBLIC")
	denied := capture(reader)
	requireDenied := func(err error) {
		t.Helper()
		var state interface{ SQLState() string }
		if !errors.As(err, &state) || state.SQLState() != "42501" {
			t.Fatal("expected actual actor EXECUTE denial", err)
		}
	}
	nullEvaluation := newEvaluation(denied)
	_, nullErr := evaluatePartitionRecipe(&nullEvaluation, owner, 0, []sqltype.Value{sqltype.Null(owner.Columns[0].Usage.Type)})
	requireDenied(nullErr)
	// No Eval here: a previously consumed body must be fenced after recapture.
	if err := evaluation.statementScope.readmitPreparedPrograms(denied); err == nil {
		t.Fatal("consumed body survived revoked EXECUTE without readmission")
	}
	fresh := newEvaluation(denied)
	if _, err := evaluatePartitionRecipe(&fresh, owner, 0, row); err == nil || len(fresh.statementScope.partitionPrograms) != 0 {
		t.Fatal("fresh statement borrowed an earlier actor's admission", err)
	}
	// The separately privileged owner cannot license the actual reader phase.
	ownerAuth := capture(admin)
	phase := newEvaluation(ownerAuth)
	if _, err := evaluatePartitionRecipe(&phase, owner, 0, row); err != nil {
		t.Fatal("privileged owner phase", err)
	}
	phase.routineAuthority = &denied
	if _, err := evaluatePartitionRecipe(&phase, owner, 0, row); err == nil || len(phase.statementScope.partitionPrograms) != 1 {
		t.Fatal("reader borrowed owner phase or failed phase was published", err)
	}
	roleExecute(t, e, admin, "GRANT EXECUTE ON FUNCTION pg_catalog.octet_length(text) TO partition_original_reader")
	granted := capture(reader)
	if err := evaluation.statementScope.readmitPreparedPrograms(granted); err != nil {
		t.Fatal("original consumed graph did not readmit after grant", err)
	}
	evaluation.statementScope.command++
	evaluation.routineAuthority = &granted
	if _, err := evaluatePartitionRecipe(&evaluation, owner, 0, row); err != nil || len(evaluation.statementScope.partitionPrograms) != 2 {
		t.Fatal("new command phase reused prior preparation", err)
	}
	if !reflect.DeepEqual(original.Dependencies, refs) {
		t.Fatal("preparation mutated published original graph")
	}
}
