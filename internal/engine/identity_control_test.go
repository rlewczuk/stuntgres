package engine

import (
	"bytes"
	"context"
	"testing"
)

func TestIdentityControlsTemporaryCollision(t *testing.T) {
	for _, mode := range []string{"ReplaceCatalog", "Restore"} {
		t.Run(mode, func(t *testing.T) {
			source, dest := New(), New()
			a, b := sessionStart(t, source, nil), sessionStart(t, dest, nil)
			sessionSQL(t, a, `CREATE SCHEMA collision_padding`)
			sessionSQL(t, a, `CREATE SCHEMA portable`)
			sessionSQL(t, a, `CREATE TABLE portable.i(id INT GENERATED ALWAYS AS IDENTITY(START 7 CACHE 2))`)
			sessionSQL(t, a, `INSERT INTO portable.i DEFAULT VALUES`)
			// Consume one persistent relation address before TEMP namespace/sequence
			// creation so kept collides with the source's linked identity sequence.
			sessionSQL(t, b, `CREATE TABLE displaced(v INT)`)
			sessionSQL(t, b, `CREATE TEMP SEQUENCE kept START 30 CACHE 10`)
			sequenceScalar(t, b, `SELECT nextval('kept')`, 30)
			_, tempDescription := sequencePreparedDescription(t, dest, b, `SELECT nextval('kept')`)
			tempRef := tempDescription.sequences.constants[0].ref.Address
			sourceRef := roleTableRef(t, source, "portable.i").Address
			original := source.catalog.state.descriptors[sourceRef].(*relationDescriptor)
			originalIdentity := original.identities[original.attributes[0]]
			if originalIdentity.sequence != tempRef {
				t.Fatal("fixture lacks actual sequence/TEMP collision", originalIdentity.sequence, tempRef)
			}
			originalSequence := source.catalog.state.descriptors[originalIdentity.sequence].(*relationDescriptor)
			originalOwned := sourceRef
			originalOwned.SubID = original.attributes[0]
			if !originalSequence.sequence.identity || originalSequence.sequence.owned != originalOwned {
				t.Fatal("source identity lacks reciprocal ownership")
			}
			tempDefinition, tempGenerator, tempMemory := dest.catalog.state.descriptors[tempRef], dest.sequences[tempRef], b.sequences[tempRef]
			generatorState, memoryState := *tempGenerator, *tempMemory
			if mode == "Restore" {
				var dump bytes.Buffer
				if err := source.Dump(context.Background(), &dump); err != nil {
					t.Fatal(err)
				}
				if err := dest.Restore(context.Background(), bytes.NewReader(dump.Bytes())); err != nil {
					t.Fatal(err)
				}
			} else {
				if _, err := dest.ReplaceCatalog(context.Background(), source.CatalogRead()); err != nil {
					t.Fatal(err)
				}
			}
			if dest.catalog.state.descriptors[tempRef] != tempDefinition || dest.sequences[tempRef] != tempGenerator || b.sequences[tempRef] != tempMemory || *tempGenerator != generatorState || *tempMemory != memoryState {
				t.Fatal("retained TEMP authority changed")
			}
			ref := roleTableRef(t, dest, "portable.i").Address
			table := dest.catalog.state.descriptors[ref].(*relationDescriptor)
			identity := table.identities[table.attributes[0]]
			if identity.sequence == tempRef {
				t.Fatal("identity forward address not remapped")
			}
			sequence := dest.catalog.state.descriptors[identity.sequence].(*relationDescriptor)
			owned := ref
			owned.SubID = table.attributes[0]
			if !sequence.sequence.identity || sequence.sequence.owned != owned {
				t.Fatal("identity reciprocal address not remapped")
			}
			sessionSQL(t, b, `INSERT INTO portable.i DEFAULT VALUES`)
			rows := sessionSQL(t, b, `SELECT id FROM portable.i ORDER BY id`)
			got := tuples(rows)
			rows.Rows.Close()
			if len(got) != 2 || got[0][0] != int64(7) || got[1][0] != int64(9) {
				t.Fatal("restored state/rows", got)
			}
			sequenceScalar(t, b, `SELECT nextval('kept')`, 31)
			sessionSQL(t, b, `DROP TABLE portable.i`)
			if dest.catalog.state.descriptors[identity.sequence] != nil || dest.sequences[identity.sequence] != nil {
				t.Fatal("remapped dependency closure failed")
			}
			if source.catalog.state.descriptors[sourceRef] != original || original.identities[original.attributes[0]] != originalIdentity {
				t.Fatal("source immutable definition changed")
			}
		})
	}
}
