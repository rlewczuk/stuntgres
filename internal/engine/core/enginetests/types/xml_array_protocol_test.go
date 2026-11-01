package types_test

import (
	"context"
	"encoding/binary"
	"errors"
	"testing"

	rootengine "stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/sqlstate"
)

// The XML input owner already attaches parameter provenance; the wire Bind
// relabels it with the portal name once, for scalar and array XML leaves alike.
func TestXMLArrayProtocolParameterContext(t *testing.T) {
	e := rootengine.New()
	s := enginetest.StartSession(t, e, nil)
	cycle := s.NewProtocolCycle()
	command := func(fn func(*core.StatementScope) error) error {
		t.Helper()
		op, _, err := s.AcquireCommand(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		defer op.Done()
		scope, err := e.StartProtocolCommand(op, cycle)
		if err != nil {
			t.Fatal(err)
		}
		return scope.Finish(fn(scope))
	}
	for _, tc := range []struct{ name, query, input string }{
		{"xml_scalar", "SELECT $1::xml", "<a>"},
		{"xml_array", "SELECT $1::xml[]", "{<a>}"},
	} {
		if err := command(func(scope *core.StatementScope) error {
			_, err := scope.ParseProtocol(tc.name, tc.query, nil)
			return err
		}); err != nil {
			t.Fatal(err)
		}
		// No parameter formats, one text parameter, no result formats.
		bind := binary.BigEndian.AppendUint32([]byte{0, 0, 0, 1}, uint32(len(tc.input)))
		bind = append(append(bind, tc.input...), 0, 0)
		err := command(func(scope *core.StatementScope) error {
			_, err := scope.BindProtocolPortal("p", tc.name, &enginetest.ProtocolReader{Data: bind})
			return err
		})
		var got *sqlstate.Error
		if !errors.As(err, &got) || got.Code != "2200N" || got.Context != `portal "p" parameter $1 = '...'` {
			t.Fatalf("%s: diagnostic=%#v error=%v", tc.name, got, err)
		}
	}
}
