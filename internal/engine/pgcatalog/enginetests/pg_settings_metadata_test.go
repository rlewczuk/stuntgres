package enginetests_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/engine/settings"
	"stuntgres/internal/sqltype"
)

// Override only the producer's fact seam; all unused runtime operations retain
// the composed engine adapter. These facts exercise conversion, not SQL capture.
type settingsProducerRuntime struct {
	core.EngineCatalogRuntime
	facts []pgcatalog.SettingsFact
	cancel context.CancelFunc
}

func (r settingsProducerRuntime) SessionSettings(ctx context.Context) ([]pgcatalog.SettingsFact, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if r.cancel != nil {
		r.cancel()
	}
	return r.facts, nil
}

func settingsProducerInput(t *testing.T) (pgcatalog.Input, pgcatalog.Producer) {
	t.Helper()
	e := engine.New()
	view := e.CatalogRead()
	source := view.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: pgcatalog.SettingsOID})
	produce, ok := pgcatalog.ProducerFor(pgcatalog.SettingsOID)
	if !ok {
		t.Fatal("missing settings producer")
	}
	return pgcatalog.Input{Context: t.Context(), Catalog: view, Source: source, Owner: view.Owner(), Runtime: settingsProducerRuntime{}}, produce
}

func TestPGSettingsProducerMetadata(t *testing.T) {
	input, produce := settingsProducerInput(t)
	names := []string{"name", "setting", "unit", "category", "short_desc", "extra_desc", "context", "vartype", "source", "min_val", "max_val", "enumvals", "boot_val", "reset_val", "sourcefile", "sourceline", "pending_restart"}
	if len(input.Source.Columns) != len(names) {
		t.Fatal("settings carrier width", len(input.Source.Columns))
	}
	for i, name := range names {
		column := input.Source.Columns[i]
		want := sqltype.Text
		switch i {
		case 11:
			want = 1009
		case 15:
			want = sqltype.Int4
		case 16:
			want = sqltype.Bool
		}
		owned, ok := input.Catalog.LookupType(want)
		if !ok || column.Usage.Type != owned || column.Name != name || !column.Nullable || !column.NullabilityKnown || column.OriginTable != uint32(pgcatalog.SettingsOID) || column.OriginAttribute != int16(i+1) {
			t.Fatal("settings carrier", i, column)
		}
	}
	if rows, err := produce(input); err != nil || rows == nil || len(rows) != 0 {
		t.Fatal("empty typed capture", rows, err)
	}
	definition := settings.NewRegistry(nil, false).Definitions()["statement_timeout"]
	input.Runtime = settingsProducerRuntime{facts: []pgcatalog.SettingsFact{{Definition: definition, Value: definition.DefaultValue(), Reset: definition.DefaultValue(), Source: "default"}}}
	rows, err := produce(input)
	if err != nil || len(rows) != 1 || len(rows[0]) != 17 {
		t.Fatal(rows, err)
	}
	for i, value := range rows[0] {
		if value.Type() != input.Source.Columns[i].Usage.Type {
			t.Fatal("datum carrier", i, value.Type())
		}
	}
	for _, i := range []int{11, 14, 15} {
		if !rows[0][i].IsNull() {
			t.Fatal("missing metadata must be typed NULL", i)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	input.Context = ctx
	if rows, err := produce(input); rows != nil || !errors.Is(err, context.Canceled) {
		t.Fatal("pre-canceled producer published rows", rows, err)
	}
	ctx, cancel = context.WithCancel(t.Context())
	defer cancel()
	input.Context = ctx
	runtime := input.Runtime.(settingsProducerRuntime)
	runtime.cancel = cancel
	input.Runtime = runtime
	if rows, err := produce(input); rows != nil || !errors.Is(err, context.Canceled) {
		t.Fatal("canceled fact capture published a prefix", rows, err)
	}
}

func TestPGSettingsProducerOwnership(t *testing.T) {
	input, produce := settingsProducerInput(t)
	definition, err := settings.Validate(settings.Definition{Name: "metadata.mode", Type: settings.Enum, BootDefault: "safe", EnumValues: []string{"safe", "fast"}})
	if err != nil {
		t.Fatal(err)
	}
	input.Runtime = settingsProducerRuntime{facts: []pgcatalog.SettingsFact{{Definition: definition, Value: settings.Value{Text: "fast"}, Reset: definition.DefaultValue(), Source: "session"}}}
	first, err := produce(input)
	if err != nil || len(first) != 1 {
		t.Fatal(first, err)
	}
	array := first[0][11]
	check := func(value sqltype.Value) {
		t.Helper()
		if value.IsNull() || value.Type() != input.Source.Columns[11].Usage.Type || value.Type().ID() != 1009 || value.ArrayRank() != 1 || value.ArrayLen() != 2 || value.ArrayDimension(0) != (sqltype.ArrayDimension{Lower: 1, Length: 2}) {
			t.Fatal("enum array shape/type", value)
		}
		for i, want := range []string{"safe", "fast"} {
			element := value.ArrayElement(i)
			got, err := element.Text()
			if err != nil || got != want || element.Type() != value.Type().Element() || element.Type().ID() != sqltype.Text {
				t.Fatal("enum element", i, got, err)
			}
		}
	}
	check(array)
	// Mutating the input fact's test-owned slice must not mutate retained rows.
	definition.EnumValues[0] = "changed"
	check(array)
	second, err := produce(input)
	if err != nil || len(second) != 1 {
		t.Fatal(second, err)
	}
	if got, err := second[0][11].ArrayElement(0).Text(); err != nil || got != "changed" {
		t.Fatal("fresh capture did not observe changed input", got, err)
	}
	if reflect.ValueOf(array).FieldByName("array").Pointer() == reflect.ValueOf(second[0][11]).FieldByName("array").Pointer() {
		t.Fatal("captures share mutable array backing")
	}
	check(array)
}
