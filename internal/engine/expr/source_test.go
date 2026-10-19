package expr

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/sqltype"
)

func TestSourceCursorScalarPresence(t *testing.T) {
	typ := sourceCursorType(t, sqltype.Int4)
	cursor := ScalarSource(sqltype.Null(typ))
	value, present, err := cursor.Next(context.Background())
	if err != nil || !present || !value.IsNull() || value.Type() != typ {
		t.Fatal("scalar NULL is a present row", value, present, err)
	}
	for range 2 {
		value, present, err = cursor.Next(context.Background())
		if err != nil || present || !value.IsNull() || value.Type() != typ {
			t.Fatal("typed EOF", value, present, err)
		}
	}
	cursor.Close()
	cursor.Close()
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, present, err := cursor.Next(canceled); present || !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation must win after EOF/close", present, err)
	}
	var unopened SourceCursor
	if _, present, err := unopened.Next(context.Background()); present || err == nil {
		t.Fatal("unopened cursor accepted")
	}
}

func TestSourceCursorOwnedLifetime(t *testing.T) {
	typ := sourceCursorType(t, sqltype.Text)
	first := OwnedSource(typ, []sqltype.Value{sqltype.String(typ, "retained"), sqltype.Null(typ)})
	value, present, err := first.Next(context.Background())
	if err != nil || !present {
		t.Fatal(value, present, err)
	}
	first.Close()
	first.Close()
	if first.Values != nil || first.State != nil || first.Value.Type() != nil {
		t.Fatal("close retains payload")
	}
	text, err := value.Text()
	if err != nil || text != "retained" || value.Type() != typ {
		t.Fatal("close invalidates emitted value", text, err)
	}
}

func sourceCursorType(t *testing.T, id sqltype.ID) *sqltype.Descriptor {
	t.Helper()
	for _, typ := range sqltype.Builtins() {
		if typ.ID() == id {
			return typ
		}
	}
	t.Fatalf("missing builtin descriptor %d", id)
	return nil
}
