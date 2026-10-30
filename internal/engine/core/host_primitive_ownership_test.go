package core_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/host"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/proc"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/sqltype"
)

// These are borrowed observations of the actual invocation buffers, deliberately
// not copies. The public reference cohort owns scalar SQL outcomes and replay.
// This supplement checks the private descriptor/token/frame cleanup boundaries.
type primitiveOwnerObservation struct {
	arguments    []host.Object
	continuation host.ProgramFunc
}

func primitiveOwnerDefinition(typ host.Type, arity int, fault string, observed *primitiveOwnerObservation) host.LanguageDefinition {
	inputs := make([]host.Type, arity)
	for i := range inputs {
		inputs[i] = typ
	}
	conversion := host.ConversionDefinition{
		ID: string(typ), Type: typ, Bootstrap: true,
		FromName: host.Name{Schema: "public", Name: "primitive_owner_from"},
		ToName:   host.Name{Schema: "public", Name: "primitive_owner_to"},
		FromSQL: func(_ context.Context, v host.SQLValue) (host.Object, error) {
			if v.Type != typ {
				return host.Object{}, errors.New("wrong primitive carrier input")
			}
			if v.Null {
				return host.Object{Null: true}, nil
			}
			switch typ {
			case host.Bool:
				return host.Object{Value: v.Bool}, nil
			case host.Int4, host.Int8:
				return host.Object{Value: v.Int64}, nil
			case host.Text:
				return host.Object{Value: v.Text}, nil
			}
			return host.Object{}, errors.New("unsupported primitive")
		},
		ToSQL: func(_ context.Context, v host.Object) (host.SQLValue, error) {
			out := host.SQLValue{Type: typ, Null: v.Null}
			if fault == "to-error" {
				return out, errors.New("primitive owner TO failure")
			}
			if !v.Null {
				switch typ {
				case host.Bool:
					x, ok := v.Value.(bool)
					if !ok {
						return out, errors.New("wrong bool object")
					}
					out.Bool = x
				case host.Int4, host.Int8:
					x, ok := v.Value.(int64)
					if !ok {
						return out, errors.New("wrong integer object")
					}
					out.Int64 = x
				case host.Text:
					x, ok := v.Value.(string)
					if !ok {
						return out, errors.New("wrong text object")
					}
					out.Text = x
				}
			}
			switch fault {
			case "wrong-tag":
				out.Type = host.Hstore
			case "extra-payload":
				out.Hstore = []host.HstoreEntry{}
			case "null-payload":
				out.Null, out.Bool = true, true
			case "range":
				out.Int64 = 2147483648
			case "nul":
				out.Text = "a\x00b"
			case "utf8":
				out.Text = string([]byte{0xff})
			}
			return out, nil
		},
	}
	program := host.ProgramDefinition{
		ID: "compute", Version: "1", Inputs: inputs, Result: typ,
		Execute: func(_ context.Context, args []host.Object) (host.Object, error) {
			if fault == "program-error" {
				return host.Object{}, errors.New("primitive owner program failure")
			}
			if fault == "wrong-object" {
				return host.Object{Value: struct{}{}}, nil
			}
			if fault == "null-object" {
				return host.Object{Null: true, Value: int64(7)}, nil
			}
			for _, arg := range args {
				if arg.Null {
					return host.Object{Null: true}, nil
				}
			}
			switch typ {
			case host.Bool:
				return host.Object{Value: !args[0].Value.(bool)}, nil
			case host.Int4, host.Int8:
				sum := int64(5)
				for _, arg := range args {
					sum += arg.Value.(int64)
				}
				return host.Object{Value: sum}, nil
			case host.Text:
				return host.Object{Value: args[0].Value.(string) + "!"}, nil
			}
			return host.Object{}, errors.New("unsupported primitive")
		},
	}
	return host.LanguageDefinition{
		Provider: "primitive-owner", ABIVersion: "1", Name: "primitive_owner",
		HandlerName: host.Name{Schema: "public", Name: "primitive_owner_handler"},
		Handler: func(ctx context.Context, program host.ProgramFunc, args []host.Object) (host.Object, error) {
			observed.arguments, observed.continuation = args, program
			return program(ctx, args)
		},
		Programs: []host.ProgramDefinition{program}, Conversions: []host.ConversionDefinition{conversion},
		Defaults: []host.DefaultDefinition{{Type:typ,FromID:string(typ),ToID:string(typ)}},
	}
}

func TestHostPrimitiveValueValidation(t *testing.T) {
	for _, typ := range []host.Type{host.Bool, host.Int4, host.Int8, host.Text} {
		faults := []string{"ok", "wrong-tag", "extra-payload", "null-payload", "wrong-object", "null-object", "program-error", "to-error"}
		if typ == host.Int4 {
			faults = append(faults, "range")
		}
		if typ == host.Text {
			faults = append(faults, "nul", "utf8")
		}
		for _, fault := range faults {
			t.Run(string(typ)+"/"+fault, func(t *testing.T) {
				primitiveOwnerInvocation(t, typ, 1, fault)
			})
		}
	}
	// Both proc.Frame and host invocation have four inline argument slots.
	// Retain the actual five-slot backing on success and both failure exits.
	for _, fault := range []string{"ok", "program-error", "wrong-tag"} {
		t.Run("five/"+fault, func(t *testing.T) {
			primitiveOwnerInvocation(t, host.Int8, 5, fault)
		})
	}
}

func primitiveOwnerInvocation(t *testing.T, typ host.Type, arity int, fault string) {
	t.Helper()
	observed := new(primitiveOwnerObservation)
	definition := primitiveOwnerDefinition(typ, arity, fault, observed)
	runtime, err := host.New(definition)
	if err != nil {
		t.Fatal(err)
	}
	e := engine.New()
	if err := e.InstallHostDefinitions([]*host.Runtime{runtime}); err != nil {
		t.Fatal(err)
	}
	// Installation mints destination-owned capabilities; the supplied registry
	// is a definition source, never executable authority in this engine.
	source := runtime
	if len(e.Catalog.HostRuntimes) != 1 {
		t.Fatal("primitive inventory did not publish exactly one runtime")
	}
	runtime = e.Catalog.HostRuntimes[0]
	if runtime == source || runtime.Program("compute") == source.Program("compute") || runtime.Conversion(string(typ)) == source.Conversion(string(typ)) || runtime.Fingerprint() != source.Fingerprint() {
		t.Fatal("installed primitive inventory lost definition identity or borrowed source capability tokens")
	}
	signature := ""
	for i := range arity {
		if i != 0 {
			signature += ","
		}
		signature += fmt.Sprintf("v%d %s", i, typ)
	}
	enginetest.Run(t, e, "CREATE FUNCTION primitive_owner_fn("+signature+") RETURNS "+string(typ)+" LANGUAGE primitive_owner AS 'compute'")
	read := e.CatalogRead()
	var routine *catalog.Procedure
	for _, descriptor := range read.Descriptors() {
		if p, ok := descriptor.(*catalog.Procedure); ok && p.Name() == "primitive_owner_fn" {
			routine = p
			break
		}
	}
	if routine == nil {
		t.Fatal("missing actual primitive declaration")
	}
	layout := routine.RoutineDefinition()
	if routine.SQL != nil || routine.Host == nil || layout != routine.Host.Layout || layout.Kind != proc.HostBody || routine.Host.Program != runtime.Program("compute") {
		t.Fatal("primitive layout/executable has a foreign or SQL owner")
	}
	if err := (stored.ProcedureProvider{}).Validate(read, routine); err != nil {
		t.Fatal(err)
	}
	currentType, ok := read.Type(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: routine.Result})
	if !ok || currentType.SQLType != layout.Result.Type || catalog.HostLogicalType(currentType.SQLType) != typ {
		t.Fatal("result does not own the exact current primitive type")
	}
	if len(routine.Host.Tokens) != arity+1 {
		t.Fatal("missing directional tokens")
	}
	for i, address := range routine.Host.Converters {
		converter, ok := read.Procedure(address)
		if routine.Host.Pairs[i] != (ids.ObjectAddress{}) || routine.Transforms != nil || routine.Display.TransformTypes != nil {
			t.Fatal("default primitive conversion fabricated SQL opt-in")
		}
		if !ok || converter.HostConversion == nil || converter.HostConversion.Type != currentType.SQLType || converter.HostConversion.Token != runtime.DefaultConversion(typ, i < arity) || routine.Host.Tokens[i] != converter.HostConversion.Token || converter.HostConversion.From != (i < arity) {
			t.Fatal("primitive default converter borrowed another type or runtime owner")
		}
	}
	foreign, err := host.New(primitiveOwnerDefinition(typ, arity, fault, new(primitiveOwnerObservation)))
	if err != nil {
		t.Fatal(err)
	}
	for _, corrupt := range []func(*catalog.HostRoutine){
		func(h *catalog.HostRoutine) { h.Program = foreign.Program("compute") },
		func(h *catalog.HostRoutine) { h.Program = source.Program("compute") },
		func(h *catalog.HostRoutine) {
			h.Tokens = append([]*host.Conversion(nil), h.Tokens...)
			h.Tokens[0] = foreign.Conversion(string(typ))
		},
		func(h *catalog.HostRoutine) {
			h.Tokens = append([]*host.Conversion(nil), h.Tokens...)
			h.Tokens[arity] = foreign.Conversion(string(typ))
		},
		func(h *catalog.HostRoutine) {
			h.Tokens = append([]*host.Conversion(nil), h.Tokens...)
			h.Tokens[arity] = source.Conversion(string(typ))
		},
	} {
		candidate := *routine
		payload := *routine.Host
		corrupt(&payload)
		candidate.Host = &payload
		state := catalog.CloneState(read.State())
		state.SetDescriptor(candidate.Address(), &candidate)
		if err := (stored.ProcedureProvider{}).Validate(e.Catalog.Read(state), &candidate); err == nil {
			t.Fatal("equal logical IDs accepted foreign executable/converter tokens")
		}
	}
	frame := proc.NewFrame(layout.Inputs)
	defer frame.Close()
	for i, usage := range layout.Inputs {
		if usage.Type != currentType.SQLType {
			t.Fatal("argument uses another primitive descriptor")
		}
		var value sqltype.Value
		switch typ {
		case host.Bool:
			value = sqltype.Boolean(usage.Type, false)
		case host.Int4, host.Int8:
			value, err = sqltype.Integer(usage.Type, int64(i+1))
		case host.Text:
			value = sqltype.String(usage.Type, "λ")
		}
		if err != nil {
			t.Fatal(err)
		}
		if err := frame.Bind(i, value, usage); err != nil {
			t.Fatal(err)
		}
	}
	// Keep the actual backing, not a copied observation of its values.
	slots := frame.Parameters
	op, done, err := enginetest.Operation(e, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer done()
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	result, invokeErr := core.ScopeInvokeSQLRoutine(scope, routine, frame, layout.Result, nil, sqltype.CollationRef{}, false)
	finishErr := scope.Finish(invokeErr)
	if fault == "ok" {
		if invokeErr != nil || finishErr != nil || result.Type() != currentType.SQLType {
			t.Fatalf("primitive result owner=%p error=%v finish=%v", result.Type(), invokeErr, finishErr)
		}
	} else if invokeErr == nil {
		t.Fatal("invalid primitive carrier/program was published")
	}
	if len(observed.arguments) != arity || observed.continuation == nil {
		t.Fatal("actual invocation buffers were not observed")
	}
	for _, argument := range observed.arguments {
		if argument.Value != nil || argument.Null {
			t.Fatal("host invocation retained primitive object backing")
		}
	}
	if _, err := observed.continuation(context.Background(), nil); err == nil {
		t.Fatal("ended callback retained callable frame authority")
	}
	frame.Close()
	for _, slot := range slots {
		if slot.Type() != nil {
			t.Fatal("closed frame retained primitive SQL slot backing")
		}
	}
	if frame.Parameters != nil || frame.Bind(0, sqltype.Null(currentType.SQLType), layout.Inputs[0]) == nil {
		t.Fatal("closed frame remains callable")
	}
	if _, err := frame.Result(result, layout.Result); err == nil {
		t.Fatal("closed frame accepted a result")
	}
	if fault == "ok" && result.Type() != currentType.SQLType {
		t.Fatal("frame closure invalidated detached primitive output")
	}
	if routine.RoutineDefinition() != layout || routine.Host.Program != runtime.Program("compute") || core.ScopeActiveSQLRoutineDepth(scope) != 0 || core.ActiveScopeOfLease(op.Lease) != nil || scope.ReplacementRead {
		t.Fatal("invocation retained scope ownership or rewrote immutable layout/program")
	}
}
