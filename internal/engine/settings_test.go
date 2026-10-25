package engine

import (
	"context"

	"stuntgres/internal/engine/settings"
)

type settingsDefaultProviderForTest struct {
	values map[string]string
	calls  map[string]int
}

func (p *settingsDefaultProviderForTest) SessionDefault(_ context.Context, d settings.Definition) (string, bool, error) {
	p.calls[d.Name]++
	if len(d.EnumValues) > 0 {
		d.EnumValues[0] = "provider-mutated-copy"
	}
	value, present := p.values[d.Name]
	return value, present, nil
}
