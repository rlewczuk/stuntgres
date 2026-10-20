package engine

import "stuntgres/internal/engine/pgcatalog"

// Temporary aliases for the ENGINE-SPLIT step 9b move; step 9c removes them.
type (
	catalogSourceInput          = pgcatalog.Input
	catalogSourceProducer       = pgcatalog.Producer
	catalogSourceRuntime        = pgcatalog.Runtime
	catalogCursorFact           = pgcatalog.CursorFact
	catalogSettingsFact         = pgcatalog.SettingsFact
	catalogPreparedFact         = pgcatalog.PreparedFact
	catalogRuleFact             = pgcatalog.RuleFact
	catalogForeignKeyComparison = pgcatalog.ForeignKeyComparison
	catalogSourceRegistration   = pgcatalog.Registration
	pgDependEdge                = pgcatalog.DependEdge
)

var _ pgcatalog.Runtime = engineCatalogRuntime{}
