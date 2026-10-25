package engine

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/settings"
)

func New() *core.Engine {
	e, err := newEngine(nil, nil, "", false)
	if err != nil {
		panic(err)
	}
	return e
}

// NewWithCatalogProviders registers feature-owned catalog providers before
// installing the builtin namespace descriptors.
func NewWithCatalogProviders(providers ...catalog.Provider) (*core.Engine, error) {
	return newEngine(providers, nil, "", false)
}

// NewWithSettings consumes definitions validated before driver publication.
func NewWithSettings(definitions []settings.Definition, database string, trackCommitTimestamp bool) *core.Engine {
	e, err := newEngine(nil, definitions, database, trackCommitTimestamp)
	if err != nil {
		panic(err)
	}
	return e
}

// newEngine is the composition: it seeds the state with NewEngine and then
// registers what the root composes.
func newEngine(providers []catalog.Provider, definitions []settings.Definition, database string, trackCommitTimestamp bool) (*core.Engine, error) {
	e, err := core.NewEngine(providers, definitions, database, trackCommitTimestamp)
	if err != nil {
		return nil, err
	}
	e.Statements, e.Factory = familyStatements{}, newSettingsEngine
	return e, nil
}

// newSettingsEngine is the composition's EngineFactory.
func newSettingsEngine(definitions []settings.Definition, database string, trackCommitTimestamp bool) (*core.Engine, error) {
	return newEngine(nil, definitions, database, trackCommitTimestamp)
}
