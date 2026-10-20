package sequence

import (
	"slices"
	"testing"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/sqltype"
)

// builtin returns the built-in descriptor of id.
func builtin(t *testing.T, id sqltype.ID) *sqltype.Descriptor {
	t.Helper()
	for _, d := range sqltype.Builtins() {
		if d.ID() == id {
			return d
		}
	}
	t.Fatalf("builtin %v missing", id)
	return nil
}

// NewRelation builds the one sequence descriptor shape that CREATE SEQUENCE,
// identity and serial columns share; the options are the caller's copy.
func TestSequenceNewRelationShape(t *testing.T) {
	address := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 90010}
	namespace := ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 2200}
	owner := (&authorization.Domain{}).Principal(authorization.BootstrapPrincipalID)
	columns := []catalog.Column{{Name: "last_value"}, {Name: "log_cnt"}, {Name: "is_called"}}
	options := catalog.SequenceOptions{SQLType: sqltype.Int8, Increment: 1, Min: 1, Max: 9, Start: 1, Cache: 1}
	d := NewRelation(RelationSpec{Address: address, Namespace: namespace, Name: "s", Revision: 7, Owner: owner, Columns: columns, Options: options})
	if d.Address() != address || d.Namespace() != namespace || d.Name() != "s" || d.Owner != owner {
		t.Fatal("identity", d.Address(), d.Namespace(), d.Name(), d.Owner)
	}
	if d.ObjectRevision != 7 || d.SequencePlanStamp != 7 {
		t.Fatal("revision and plan stamp", d.ObjectRevision, d.SequencePlanStamp)
	}
	if d.RelationKind != catalog.SequenceRelation || d.ColumnMeta.HighWater != 3 || !slices.Equal(d.Attributes, []int32{1, 2, 3}) {
		t.Fatal("kind, high water or attributes", d.RelationKind, d.ColumnMeta.HighWater, d.Attributes)
	}
	if d.Index["last_value"] != 0 || d.Index["log_cnt"] != 1 || d.Index["is_called"] != 2 || len(d.Index) != 3 || len(d.Columns) != 3 {
		t.Fatal("index or columns", d.Index, d.Columns)
	}
	if d.Sequence == nil || *d.Sequence != options || d.Security.ACL.Explicit || len(d.Security.ACL.Entries) != 0 {
		t.Fatal("options or ACL", d.Sequence, d.Security.ACL)
	}
	options.Start = 5
	if d.Sequence.Start != 1 {
		t.Fatal("descriptor options alias the caller's value")
	}
}

func TestSequenceGenerators(t *testing.T) {
	options := catalog.SequenceOptions{Start: 4}
	if g := InitialGenerator(options, nil, 3); *g != (storage.SequenceGenerator{Last: 4, Generation: 3}) {
		t.Fatal("initial", *g)
	}
	restart := int64(9)
	if g := InitialGenerator(options, &restart, 3); *g != (storage.SequenceGenerator{Last: 9, Generation: 3}) {
		t.Fatal("initial restart", *g)
	}
	state := storage.SequenceGenerator{Last: 6, Called: true, Generation: 2}
	if g := ReplacementGenerator(state, 5, nil); *g != (storage.SequenceGenerator{Last: 6, Called: true, Generation: 5}) {
		t.Fatal("replacement", *g)
	}
	if g := ReplacementGenerator(state, 5, &restart); *g != (storage.SequenceGenerator{Last: 9, Generation: 5}) {
		t.Fatal("replacement restart", *g)
	}
}
