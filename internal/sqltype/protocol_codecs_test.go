package sqltype

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"stuntgres/internal/sqlstate"
	"testing"
)

func TestProtocolValueCodecs(t *testing.T) {
	types := testTypes()
	query, err := QueryInt(60001, "query_int", "public.query_int")
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		descriptor                  *Descriptor
		code, message, inputMessage string
	}{
		{types[ACLItem], "42883", "no binary output function available for type aclitem", "no binary input function available for type aclitem"},
		{query, "42883", "no binary output function available for type query_int", "no binary input function available for type query_int"},
		{types[Tid], "0A000", "binary output is not supported for type tid", "binary input is not supported for type tid"},
	} {
		var initialization *sqlstate.Error
		if err := test.descriptor.CheckBinaryOutput(); !errors.As(err, &initialization) || initialization.Code != test.code || initialization.Message != test.message {
			t.Fatal("declared send initialization lost unavailable-provider diagnostic", err)
		}
		initialization = nil
		if err := test.descriptor.CheckBinaryInput(); !errors.As(err, &initialization) || initialization.Code != test.code || initialization.Message != test.inputMessage {
			t.Fatal("declared receive initialization lost unavailable-provider diagnostic", err)
		}
		initialization = nil
		if err := test.descriptor.CheckBinaryInputDeclaration(); !errors.As(err, &initialization) || initialization.Code != test.code || initialization.Message != test.inputMessage {
			t.Fatal("declared receive function lost unavailable-provider diagnostic", err)
		}
		_, err := test.descriptor.AppendBinaryWithEnvironment(context.Background(), nil, Value{typ: test.descriptor}, OutputEnvironment{})
		var diagnostic *sqlstate.Error
		if !errors.As(err, &diagnostic) || diagnostic.Code != test.code || diagnostic.Message != test.message {
			t.Fatal(test.descriptor.Name(), err)
		}
		_, err = test.descriptor.BinaryInputWithEnvironment(context.Background(), nil, -1, InputEnvironment{})
		diagnostic = nil
		if !errors.As(err, &diagnostic) || diagnostic.Code != test.code || diagnostic.Message != test.inputMessage {
			t.Fatal(test.descriptor.Name(), err)
		}
	}
	t.Run("domain-receive-initialization", func(t *testing.T) {
		domain, err := Domain(60003, "protocol_acl_domain", "public.protocol_acl_domain", types[ACLItem].Usage(), true)
		if err != nil {
			t.Fatal(err)
		}
		if err := domain.CheckBinaryInputDeclaration(); err != nil {
			t.Fatal("generic domain receiver unavailable before invocation", err)
		}
		var diagnostic *sqlstate.Error
		err = domain.CheckBinaryInput()
		if !errors.As(err, &diagnostic) || diagnostic.Code != "42883" || diagnostic.Message != "no binary input function available for type aclitem" {
			t.Fatal("NULL receiver invocation lost base lookup", err)
		}
		// A genuine receive invocation must likewise fail at base lookup,
		// before requesting the deliberately absent domain-check environment.
		_, err = domain.BinaryInputWithEnvironment(context.Background(), nil, -1, InputEnvironment{})
		diagnostic = nil
		if !errors.As(err, &diagnostic) || diagnostic.Code != "42883" || diagnostic.Message != "no binary input function available for type aclitem" {
			t.Fatal("base receiver absence lost precedence over domain constraints", err)
		}
	})
	t.Run("recursive-domain-codecs", func(t *testing.T) {
		ctx := context.Background()
		// The epoch instant has independently specified PostgreSQL microseconds 0.
		// This exercises the builtin domain's actual delegated provider, not its
		// membership in a descriptor list or a duplicated capability whitelist.
		domain := types[13254]
		value, err := domain.InputWithEnvironment(ctx, "2000-01-01 00:00:00+00", -1, InputEnvironment{})
		if err != nil {
			t.Fatal(err)
		}
		encoded, err := domain.AppendBinaryWithEnvironment(ctx, nil, value, OutputEnvironment{})
		if err != nil || !bytes.Equal(encoded, make([]byte, 8)) {
			t.Fatal("domain send did not delegate epoch value", encoded, err)
		}
		received, err := domain.BinaryInputWithEnvironment(ctx, make([]byte, 8), -1, InputEnvironment{})
		if err != nil || received.Type() != domain {
			t.Fatal("domain receive lost logical identity", err)
		}
		encoded, err = domain.AppendBinaryWithEnvironment(ctx, nil, received, OutputEnvironment{})
		if err != nil || !bytes.Equal(encoded, make([]byte, 8)) {
			t.Fatal(encoded, err)
		}
		array, err := Array(60002, "_protocol_epoch_domain", domain)
		if err != nil {
			t.Fatal(err)
		}
		values, err := array.InputWithEnvironment(ctx, `{"2000-01-01 00:00:00+00",NULL}`, -1, InputEnvironment{})
		if err != nil {
			t.Fatal(err)
		}
		// One dimension, NULL flag, domain OID13254, two members, lower bound1,
		// eight zero epoch bytes, and a separate SQL NULL (-1 length).
		want, err := hex.DecodeString("0000000100000001000033c60000000200000001000000080000000000000000ffffffff")
		if err != nil {
			t.Fatal(err)
		}
		encoded, err = array.AppendBinaryWithEnvironment(ctx, nil, values, OutputEnvironment{})
		if err != nil || !bytes.Equal(encoded, want) {
			t.Fatal("recursive domain array send", encoded, err)
		}
		values, err = array.BinaryInputWithEnvironment(ctx, want, -1, InputEnvironment{})
		if err != nil || values.ArrayLen() != 2 || values.ArrayElement(0).Type() != domain || values.ArrayElement(0).IsNull() || !values.ArrayElement(1).IsNull() {
			t.Fatal("recursive receive lost domain/NULL identities", err)
		}
	})
}
