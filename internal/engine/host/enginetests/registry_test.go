package enginetests

import (
	"context"
	"errors"
	"strings"
	"stuntgres/internal/engine/host"
	"testing"
)

func registryDefinition() host.LanguageDefinition {
	return host.LanguageDefinition{Provider: "unit-provider", ABIVersion: "integer-v1", Name: "unit_host", HandlerName: host.Name{Schema: "public", Name: "unit_handler"}, Handler: func(ctx context.Context, p host.ProgramFunc, args []host.Object) (host.Object, error) {
		return p(ctx, args)
	}, Conversions: []host.ConversionDefinition{{ID: "integer", Type: host.Int8, Bootstrap: true, FromName: host.Name{Schema: "public", Name: "unit_from"}, ToName: host.Name{Schema: "public", Name: "unit_to"}, FromSQL: func(_ context.Context, v host.SQLValue) (host.Object, error) {
		if v.Null {
			return host.Object{Null: true}, nil
		}
		return host.Object{Value: v.Int64}, nil
	}, ToSQL: func(_ context.Context, v host.Object) (host.SQLValue, error) {
		if v.Null {
			return host.SQLValue{Type: host.Int8, Null: true}, nil
		}
		n, ok := v.Value.(int64)
		if !ok {
			return host.SQLValue{}, errors.New("not an integer object")
		}
		return host.SQLValue{Type: host.Int8, Int64: n}, nil
	}}}, Programs: []host.ProgramDefinition{{ID: "increment", Version: "1", Inputs: []host.Type{host.Int8}, Result: host.Int8, Execute: func(_ context.Context, args []host.Object) (host.Object, error) {
		n, ok := args[0].Value.(int64)
		if !ok {
			return host.Object{}, errors.New("wrong object")
		}
		return host.Object{Value: n + 1}, nil
	}}}}
}
func TestHostRegistryOwnership(t *testing.T) {
	original := registryDefinition()
	r, err := host.New(original)
	if err != nil {
		t.Fatal(err)
	}
	original.Programs[0].Inputs[0] = host.Text
	original.Conversions[0].ID = "mutated"
	p := r.Program("increment")
	if p == nil || p.Runtime() != r || r.Conversion("integer").Runtime() != r {
		t.Fatal("immutable token ownership lost")
	}
	copy := r.Definition()
	copy.Programs[0].Inputs[0] = host.Text
	copy.Conversions[0].Type = host.Text
	if p.Definition().Inputs[0] != host.Int8 || r.Conversion("integer").Definition().Type != host.Int8 || r.Conversion("integer").Definition().ID != "integer" {
		t.Fatal("registration backing escaped")
	}
	conversion := r.Conversion("integer")
	result, err := p.Invoke(context.Background(), []host.SQLValue{{Type: host.Int8, Int64: 7}}, []*host.Conversion{conversion}, conversion, nil)
	if err != nil || result.Type != host.Int8 || result.Int64 != 8 {
		t.Fatal(result, err)
	}
	fresh, err := host.New(registryDefinition())
	if err != nil {
		t.Fatal(err)
	}
	if fresh == r || fresh.Program("increment") == p || fresh.Conversion("integer") == r.Conversion("integer") || fresh.Fingerprint() != r.Fingerprint() {
		t.Fatal("fresh logical registration reused source tokens or changed stable identity")
	}
	for _, modify := range []func(*host.LanguageDefinition){func(d *host.LanguageDefinition) { d.Handler = nil }, func(d *host.LanguageDefinition) { d.Programs[0].Execute = nil }, func(d *host.LanguageDefinition) { d.Conversions = append(d.Conversions, d.Conversions[0]) }, func(d *host.LanguageDefinition) { d.Programs[0].Inputs[0] = host.Type("unsupported") }, func(d *host.LanguageDefinition) {
		d.Defaults = []host.DefaultDefinition{{Type: host.Int8, FromID: "absent"}}
	}} {
		d := registryDefinition()
		modify(&d)
		if _, err := host.New(d); err == nil {
			t.Fatal("invalid registration accepted")
		}
	}
	d := registryDefinition()
	d.ABIVersion = "integer-v2"
	incompatible, err := host.New(d)
	if err != nil {
		t.Fatal(err)
	}
	if incompatible.Fingerprint() == r.Fingerprint() {
		t.Fatal("ABI mismatch lost")
	}
	for _, modify := range []func(*host.LanguageDefinition){func(d *host.LanguageDefinition) { d.Name = strings.Repeat("x", 64) }, func(d *host.LanguageDefinition) { d.HandlerName.Schema = "bad\x00schema" }, func(d *host.LanguageDefinition) { d.Provider = string([]byte{0xff}) }, func(d *host.LanguageDefinition) { d.Programs[0].ID = "bad\x00program" }} {
		d := registryDefinition()
		modify(&d)
		if _, err := host.New(d); err == nil {
			t.Fatal("unrepresentable native registration accepted")
		}
	}
	// Exercise the real Invoke unwind: a handler retaining its lent capability
	// receives an ended-invocation error and its lent argument backing is empty.
	var retained host.ProgramFunc
	var retainedArgs []host.Object
	calls := 0
	lending := registryDefinition()
	originalProgram := lending.Programs[0].Execute
	lending.Programs[0].Execute = func(ctx context.Context, args []host.Object) (host.Object, error) {
		calls++
		return originalProgram(ctx, args)
	}
	lending.Handler = func(ctx context.Context, program host.ProgramFunc, args []host.Object) (host.Object, error) {
		retained = program
		retainedArgs = args
		return program(ctx, args)
	}
	lender, err := host.New(lending)
	if err != nil {
		t.Fatal(err)
	}
	token := lender.Conversion("integer")
	if _, err = lender.Program("increment").Invoke(context.Background(), []host.SQLValue{{Type: host.Int8, Int64: 7}}, []*host.Conversion{token}, token, func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	if len(retainedArgs) != 1 || retainedArgs[0].Value != nil || retainedArgs[0].Null {
		t.Fatal("handler retained live argument backing")
	}
	if _, err = retained(context.Background(), []host.Object{{Value: int64(99)}}); err == nil || calls != 1 {
		t.Fatal("retained handler capability executed outside invocation", calls, err)
	}
}
