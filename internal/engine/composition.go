package engine

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/settings"
)

func New() *core.Engine {
	e, err := newEngine(nil, nil, "", core.Startup{})
	if err != nil {
		panic(err)
	}
	return e
}

// NewWithCatalogProviders registers feature-owned catalog providers before
// installing the builtin namespace descriptors.
func NewWithCatalogProviders(providers ...catalog.Provider) (*core.Engine, error) {
	return newEngine(providers, nil, "", core.Startup{})
}

// NewWithSettings consumes definitions validated before driver publication.
func NewWithSettings(definitions []settings.Definition, database string, trackCommitTimestamp bool) *core.Engine {
	return NewWithStartup(definitions, database, core.Startup{TrackCommitTimestamp: trackCommitTimestamp})
}

// NewWithStartup is NewWithSettings with the complete immutable startup
// configuration, including logical WAL and the output plugins it serves.
func NewWithStartup(definitions []settings.Definition, database string, startup core.Startup) *core.Engine {
	e, err := newEngine(nil, definitions, database, startup)
	if err != nil {
		panic(err)
	}
	return e
}

// newEngine is the composition: it seeds the state with NewEngine and then
// registers what the root composes. Its EngineFactory keeps the startup
// configuration for template seeds and Restore stages.
func newEngine(providers []catalog.Provider, definitions []settings.Definition, database string, startup core.Startup) (*core.Engine, error) {
	e, err := core.NewEngine(providers, definitions, database, startup)
	if err != nil {
		return nil, err
	}
	e.Statements = familyStatements{}
	e.Factory = func(definitions []settings.Definition, database string, trackCommitTimestamp bool) (*core.Engine, error) {
		next := startup
		next.TrackCommitTimestamp = trackCommitTimestamp
		return newEngine(nil, definitions, database, next)
	}
	return e, nil
}
