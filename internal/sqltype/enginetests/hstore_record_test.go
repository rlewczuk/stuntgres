package enginetests

import (
	"context"
	"errors"
	"stuntgres/internal/sqltype"
	"testing"
)

func hstoreRecordTypes(t *testing.T) (*sqltype.Descriptor, *sqltype.Descriptor, map[sqltype.ID]*sqltype.Descriptor) {
	t.Helper()
	builtins := make(map[sqltype.ID]*sqltype.Descriptor)
	for _, d := range sqltype.Builtins() {
		builtins[d.ID()] = d
	}
	pair, err := sqltype.Composite(65001, "pair", "pair", 65002, []sqltype.CompositeMember{{Attribute: 1, Name: "i", Usage: builtins[23].Usage()}, {Attribute: 2, Name: "n", Usage: builtins[25].Usage()}})
	if err != nil {
		t.Fatal(err)
	}
	return hstoreUnitType(t, 65000), pair, builtins
}
func hstoreRecordInput(t *testing.T, d *sqltype.Descriptor, text string) sqltype.Value {
	t.Helper()
	value, err := d.Input(context.Background(), text, -1)
	if err != nil {
		t.Fatal(err)
	}
	return value
}
func hstoreRecordOutput(t *testing.T, value sqltype.Value, want string) {
	t.Helper()
	got, err := value.Type().Output(context.Background(), value)
	if err != nil || got != want {
		t.Fatalf("output %q want %q: %v", got, want, err)
	}
}
func hstoreRecordInteger(t *testing.T, d *sqltype.Descriptor, n int64) sqltype.Value {
	t.Helper()
	value, err := sqltype.Integer(d, n)
	if err != nil {
		t.Fatal(err)
	}
	return value
}
func TestHstoreRecordTypedBoundaries(t *testing.T) {
	ctx := context.Background()
	h, pair, _ := hstoreRecordTypes(t)
	base := hstoreRecordInput(t, pair, "(7,keep)")
	for _, tc := range []struct{ source, want string }{{"i=>0042", "(42,keep)"}, {"unknown=>oops", "(7,keep)"}, {"N=>changed", "(7,keep)"}, {"n=>NULL", "(7,)"}, {`n=>"NULL"`, "(7,NULL)"}, {`n=>""`, `(7,"")`}, {"", "(7,keep)"}} {
		got, err := sqltype.HstorePopulateRecord(ctx, base, h, hstoreRecordInput(t, h, tc.source), sqltype.InputEnvironment{})
		if err != nil {
			t.Fatal(err)
		}
		hstoreRecordOutput(t, got, tc.want)
	}
	for _, input := range []sqltype.Value{sqltype.Null(pair), hstoreRecordInput(t, pair, "(,)")} {
		got, err := h.HstoreFromRecord(ctx, input, sqltype.OutputEnvironment{})
		if err != nil || got.IsNull() {
			t.Fatal("whole NULL constructor", err)
		}
		hstoreRecordOutput(t, got, `"i"=>NULL, "n"=>NULL`)
	}
	empty := hstoreRecordInput(t, h, "")
	got, err := sqltype.HstorePopulateRecord(ctx, sqltype.Null(pair), h, empty, sqltype.InputEnvironment{})
	if err != nil || got.IsNull() {
		t.Fatal("whole NULL versus all NULL", err)
	}
	hstoreRecordOutput(t, got, "(,)")
	got, err = sqltype.HstorePopulateRecord(ctx, sqltype.Null(pair), h, sqltype.Null(h), sqltype.InputEnvironment{})
	if err != nil || !got.IsNull() {
		t.Fatal("NULL source", err)
	}
	zero, err := sqltype.Composite(65003, "zero", "zero", 65004, nil)
	if err != nil {
		t.Fatal(err)
	}
	got, err = h.HstoreFromRecord(ctx, sqltype.Null(zero), sqltype.OutputEnvironment{})
	if err != nil || got.IsNull() {
		t.Fatal(err)
	}
	hstoreRecordOutput(t, got, "")
	got, err = sqltype.HstorePopulateRecord(ctx, sqltype.Null(zero), h, empty, sqltype.InputEnvironment{})
	if err != nil || got.IsNull() {
		t.Fatal(err)
	}
	hstoreRecordOutput(t, got, "()")
	deferred := sqltype.DeferredRecord()
	for _, f := range []func() (sqltype.Value, error){func() (sqltype.Value, error) {
		return h.HstoreFromRecord(ctx, sqltype.Null(deferred), sqltype.OutputEnvironment{})
	}, func() (sqltype.Value, error) {
		return sqltype.HstorePopulateRecord(ctx, sqltype.Null(deferred), h, empty, sqltype.InputEnvironment{})
	}} {
		_, err := f()
		if state, ok := err.(interface{ SQLState() string }); !ok || state.SQLState() != "42809" {
			t.Fatal("unregistered record", err)
		}
	}
	got, err = sqltype.HstorePopulateRecord(ctx, sqltype.Null(deferred), h, sqltype.Null(h), sqltype.InputEnvironment{})
	if err != nil || !got.IsNull() {
		t.Fatal("anonymous both NULL", err)
	}
}
func TestHstoreRecordOwnershipAndGeneration(t *testing.T) {
	ctx := context.Background()
	h, _, b := hstoreRecordTypes(t)
	shape, err := sqltype.Composite(65001, "tuple", "tuple", 65002, []sqltype.CompositeMember{{Attribute: 1, Name: "i", Usage: b[23].Usage()}, {Attribute: 2, Name: "a", Usage: b[1009].Usage()}})
	if err != nil {
		t.Fatal(err)
	}
	array := hstoreRecordInput(t, b[1009], "{keep,shared}")
	fields := []sqltype.Value{hstoreRecordInteger(t, b[23], 7), array}
	base, err := sqltype.CompositeValue(shape, fields)
	if err != nil {
		t.Fatal(err)
	}
	fields[0] = hstoreRecordInteger(t, b[23], 99)
	fields[1] = sqltype.Null(b[1009])
	replacement := hstoreRecordInput(t, h, "i=>42")
	got, err := sqltype.HstorePopulateRecord(ctx, base, h, replacement, sqltype.InputEnvironment{})
	if err != nil {
		t.Fatal(err)
	}
	if got.CompositeField(1) != array || base.CompositeField(1) != array || got == base {
		t.Fatal("unchanged field backing lost or changed tuple shared")
	}
	n, _ := base.CompositeField(0).Integer()
	if n != 7 {
		t.Fatal("borrowed external fields")
	}
	for _, source := range []sqltype.Value{hstoreRecordInput(t, h, ""), hstoreRecordInput(t, h, "unknown=>oops"), sqltype.Null(h)} {
		unchanged, err := sqltype.HstorePopulateRecord(ctx, base, h, source, sqltype.InputEnvironment{})
		if err != nil || unchanged != base {
			t.Fatal("unchanged tuple allocated/rebuilt", err)
		}
	}
	foreign := hstoreUnitType(t, h.ID())
	for _, source := range []sqltype.Value{replacement, sqltype.Null(h)} {
		if v, err := sqltype.HstorePopulateRecord(ctx, base, foreign, source, sqltype.InputEnvironment{}); err == nil || v.Type() != nil {
			t.Fatal("foreign source accepted")
		}
	}
	other := *b[23]
	if _, err := sqltype.CompositeValue(shape, []sqltype.Value{hstoreRecordInteger(t, &other, 7), array}); err == nil {
		t.Fatal("foreign child accepted")
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	for _, f := range []func() (sqltype.Value, error){func() (sqltype.Value, error) { return h.HstoreFromRecord(cancelled, base, sqltype.OutputEnvironment{}) }, func() (sqltype.Value, error) {
		return sqltype.HstorePopulateRecord(cancelled, base, h, replacement, sqltype.InputEnvironment{})
	}} {
		v, err := f()
		if !errors.Is(err, context.Canceled) || v.Type() != nil {
			t.Fatal("cancellation", err)
		}
	}
	hstoreRecordOutput(t, base, `(7,"{keep,shared}")`)
}

type hstoreRecordDomainCheck struct {
	reject   *sqltype.Descriptor
	nullOnly bool
}

var hstoreRecordDomainFailure = errors.New("domain rejected")

func (d hstoreRecordDomainCheck) CheckDomain(_ context.Context, typ *sqltype.Descriptor, v sqltype.Value) error {
	if typ == d.reject && (!d.nullOnly || v.IsNull()) {
		return hstoreRecordDomainFailure
	}
	return nil
}
func TestHstoreRecordDestinationInput(t *testing.T) {
	ctx := context.Background()
	h, _, b := hstoreRecordTypes(t)
	varchar := b[1043].Usage()
	varchar.Typmod = 6 // varchar(2): header + character count.
	shape, err := sqltype.Composite(65001, "typed", "typed", 65002, []sqltype.CompositeMember{{Attribute: 1, Name: "z", Usage: b[23].Usage()}, {Attribute: 2, Name: "a", Usage: varchar}})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ input, code, message string }{{"a=>abc,z=>oops", "22P02", `invalid input syntax for type integer: "oops"`}, {"a=>abc,z=>7", "22001", "value too long for type character varying(2)"}} {
		_, err := sqltype.HstorePopulateRecord(ctx, sqltype.Null(shape), h, hstoreRecordInput(t, h, tc.input), sqltype.InputEnvironment{})
		s, ok := err.(interface{ SQLState() string })
		if !ok || s.SQLState() != tc.code || err.Error() == "" {
			t.Fatal("descriptor-order or typmod", err)
		}
		if err.Error() != "stuntgres: "+tc.message {
			t.Fatalf("diagnostic %q want %q", err.Error(), tc.message)
		}
	}
	domain, err := sqltype.Domain(65005, "nonnull", "nonnull", b[25].Usage(), false, true)
	if err != nil {
		t.Fatal(err)
	}
	withDomain, err := sqltype.Composite(65006, "domain_tuple", "domain_tuple", 65007, []sqltype.CompositeMember{{Attribute: 1, Name: "n", Usage: domain.Usage()}})
	if err != nil {
		t.Fatal(err)
	}
	env := sqltype.InputEnvironment{DomainChecks: hstoreRecordDomainCheck{reject: domain, nullOnly: true}}
	for _, input := range []string{"", "n=>NULL"} {
		if _, err := sqltype.HstorePopulateRecord(ctx, sqltype.Null(withDomain), h, hstoreRecordInput(t, h, input), env); err == nil || err.Error() != "domain rejected" {
			t.Fatal("NULL domain check missing", err)
		}
	}
	outer, err := sqltype.Domain(65008, "outer_tuple", "outer_tuple", withDomain.Usage(), true)
	if err != nil {
		t.Fatal(err)
	}
	env.DomainChecks = hstoreRecordDomainCheck{reject: outer}
	if _, err := sqltype.HstorePopulateRecord(ctx, sqltype.Null(outer), h, hstoreRecordInput(t, h, "n=>ok"), env); err == nil || err.Error() != "domain rejected" {
		t.Fatal("outer domain check missing", err)
	}
	field, err := domain.InputWithEnvironment(ctx, "keep", -1, env)
	if err != nil {
		t.Fatal(err)
	}
	base, err := sqltype.OwnedCompositeValue(outer, []sqltype.Value{field})
	if err != nil {
		t.Fatal(err)
	}
	originalField := base.CompositeField(0)
	unmatched := hstoreRecordInput(t, h, "unknown=>oops")
	if result, err := sqltype.HstorePopulateRecord(ctx, base, h, unmatched, env); err != hstoreRecordDomainFailure || result.Type() != nil {
		t.Fatal("unmatched nonempty replacement bypassed outer domain check", err)
	}
	if base.CompositeField(0) != originalField {
		t.Fatal("rejected result check changed original field backing")
	}
	for _, source := range []sqltype.Value{sqltype.Null(h), hstoreRecordInput(t, h, "")} {
		result, err := sqltype.HstorePopulateRecord(ctx, base, h, source, env)
		if err != nil || result != base {
			t.Fatal("NULL/empty hstore lost its unchecked no-copy return", err)
		}
	}
	env.DomainChecks = hstoreRecordDomainCheck{}
	result, err := sqltype.HstorePopulateRecord(ctx, base, h, unmatched, env)
	if err != nil || result != base || result.CompositeField(0) != originalField {
		t.Fatal("successful result-domain validation copied unchanged backing", err)
	}
}
