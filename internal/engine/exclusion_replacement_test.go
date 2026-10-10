package engine

import (
	"bytes"
	"context"
	"reflect"
	"testing"
)

func TestExclusionReplacement(t *testing.T) {
	for _, self := range []bool{false, true} {
		t.Run(map[bool]string{false: "foreign", true: "same_owner"}[self], func(t *testing.T) {
			ctx := context.Background()
			source := New()
			a := sessionStart(t, source, nil)
			sessionSQL(t, a, "CREATE SCHEMA portable CREATE TABLE t(k INT,CONSTRAINT ex EXCLUDE USING btree(k WITH =) DEFERRABLE INITIALLY DEFERRED)")
			sessionSQL(t, a, "INSERT INTO portable.t VALUES(7)")
			sessionSQL(t, a, "CREATE DOMAIN portable.d AS INT")
			sessionSQL(t, a, "CREATE TABLE portable.domain_t(k portable.d,CONSTRAINT domain_ex EXCLUDE USING hash(k WITH =))")
			sessionSQL(t, a, "INSERT INTO portable.domain_t VALUES(7)")
			before := source.CatalogRead()
			original := before.state.descriptors[roleTableRef(t, source, "portable.t").Address].(*relationDescriptor)
			constraint := original.exclusionConstraints[0]
			sourceRoot := source.indexRoots[constraint.Index]
			var dump bytes.Buffer
			if err := source.Dump(ctx, &dump); err != nil {
				t.Fatal(err)
			}
			target := New()
			b := sessionStart(t, target, nil)
			if self {
				target, b = source, a
			}
			sessionSQL(t, b, "CREATE TEMP SEQUENCE kept CACHE 10")
			sequenceScalar(t, b, "SELECT nextval('kept')", 1)
			_, description := sequencePreparedDescription(t, target, b, "SELECT nextval('kept')")
			tempRef := description.sequences.constants[0].ref
			temp, generator, memory := target.state.descriptors[tempRef.Address], target.sequences[tempRef.Address], b.sequences[tempRef.Address]
			if !self && tempRef.Address.ObjectID != original.address.ObjectID {
				t.Fatal("fixture must collide with a real retained TEMP address")
			}
			if _, err := target.ReplaceCatalog(ctx, before); err != nil {
				t.Fatal(err)
			}
			if target.state.descriptors[tempRef.Address] != temp || target.sequences[tempRef.Address] != generator || b.sequences[tempRef.Address] != memory {
				t.Fatal("replacement changed retained TEMP identity")
			}
			parent := target.state.descriptors[roleTableRef(t, target, "portable.t").Address].(*relationDescriptor)
			index, root := btreeRoot(t, target, "portable.ex")
			current := parent.exclusionConstraints[0]
			if current.Index != index.address || !backingConstraintPresent(parent, index) || !current.Deferrable || !current.InitiallyDeferred || len(root.entries) != 1 {
				t.Fatal("replacement broke the actual exclusion closure")
			}
			if root == sourceRoot || self && root.entries[0].row == sourceRoot.entries[0].row {
				t.Fatal("replacement imported source root or row identities")
			}
			if !reflect.DeepEqual(original.exclusionConstraints[0], constraint) {
				t.Fatal("replacement mutated the source definition")
			}
			// Domain providers must bind the destination descriptor, not merely its OID.
			result := sessionSQL(t, b, "INSERT INTO portable.domain_t VALUES(7) ON CONFLICT ON CONSTRAINT domain_ex DO NOTHING")
			if result.Count != 0 {
				t.Fatal("destination domain equality did not recognize its imported key")
			}
			sequenceScalar(t, b, "SELECT nextval('kept')", 2)
			var again bytes.Buffer
			if err := target.Dump(ctx, &again); err != nil || !bytes.Equal(dump.Bytes(), again.Bytes()) {
				t.Fatal("noncanonical exclusion replacement replay", err, again.String())
			}
			if len(target.exclusionPending) != 0 {
				t.Fatal("replacement retained transient exclusion state")
			}
			uniqueClaimsEmpty(t, target)
		})
	}
}
