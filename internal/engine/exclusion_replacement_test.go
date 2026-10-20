package engine

import (
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
			// Match the destination's actual table/toast namespace allocation.
			sessionSQL(t, a, "CREATE SCHEMA collision_padding")
			sessionSQL(t, a, "CREATE SCHEMA portable CREATE TABLE t(k INT,CONSTRAINT ex EXCLUDE USING btree(k WITH =) DEFERRABLE INITIALLY DEFERRED)")
			sessionSQL(t, a, "INSERT INTO portable.t VALUES(7)")
			sessionSQL(t, a, "CREATE DOMAIN portable.d AS INT")
			sessionSQL(t, a, "CREATE TABLE portable.domain_t(k portable.d,CONSTRAINT domain_ex EXCLUDE USING hash(k WITH =))")
			sessionSQL(t, a, "INSERT INTO portable.domain_t VALUES(7)")
			before := source.CatalogRead()
			original := before.MustRelation(roleTableRef(t, source, "portable.t").Address)
			constraint := original.Constraints.Exclusion[0]
			sourceRoot := source.storage.IndexRoots[constraint.Index]
			target := New()
			b := sessionStart(t, target, nil)
			if self {
				target, b = source, a
			}
			sessionSQL(t, b, "CREATE TEMP SEQUENCE kept CACHE 10")
			sequenceScalar(t, b, "SELECT nextval('kept')", 1)
			_, description := sequencePreparedDescription(t, target, b, "SELECT nextval('kept')")
			tempRef := description.sequences.constants[0].ref
			temp, generator, memory := target.catalog.state.Get(tempRef.Address), target.storage.Sequences[tempRef.Address], b.sequences.memory(tempRef.Address)
			if !self && tempRef.Address.ObjectID != original.Address().ObjectID {
				t.Fatal("fixture must collide with a real retained TEMP address")
			}
			if _, err := target.ReplaceCatalog(ctx, source, before); err != nil {
				t.Fatal(err)
			}
			if target.catalog.state.Get(tempRef.Address) != temp || target.storage.Sequences[tempRef.Address] != generator || b.sequences.memory(tempRef.Address) != memory {
				t.Fatal("replacement changed retained TEMP identity")
			}
			parent := target.catalog.state.MustRelation(roleTableRef(t, target, "portable.t").Address)
			index, root := btreeRoot(t, target, "portable.ex")
			current := parent.Constraints.Exclusion[0]
			if current.Index != index.Address() || !backingConstraintPresent(parent, index) || !current.Deferrable || !current.InitiallyDeferred || len(root.Entries) != 1 {
				t.Fatal("replacement broke the actual exclusion closure")
			}
			if root == sourceRoot || self && root.Entries[0].Row == sourceRoot.Entries[0].Row {
				t.Fatal("replacement imported source root or row identities")
			}
			if !reflect.DeepEqual(original.Constraints.Exclusion[0], constraint) {
				t.Fatal("replacement mutated the source definition")
			}
			// Domain providers must bind the destination descriptor, not merely its OID.
			result := sessionSQL(t, b, "INSERT INTO portable.domain_t VALUES(7) ON CONFLICT ON CONSTRAINT domain_ex DO NOTHING")
			if result.Count != 0 {
				t.Fatal("destination domain equality did not recognize its imported key")
			}
			sequenceScalar(t, b, "SELECT nextval('kept')", 2)
			if len(target.exclusionPending) != 0 {
				t.Fatal("replacement retained transient exclusion state")
			}
			uniqueClaimsEmpty(t, target)
		})
	}
}
