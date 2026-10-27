package settings

const geqoCategory = "Query Tuning / Genetic Query Optimizer"

// GEQO configuration is stored state, not a native genetic planning algorithm.
var geqoBuiltins = []Definition{
	{Name: "geqo", Type: Boolean, BootDefault: "on", Category: geqoCategory, Description: "Enables genetic query optimization.", ExtraDescription: "This algorithm attempts to do planning without exhaustive searching."},
	{Name: "geqo_threshold", Type: Integer, BootDefault: "12", IntegerMin: 2, IntegerMax: 2147483647, Category: geqoCategory, Description: "Sets the threshold of FROM items beyond which GEQO is used."},
	{Name: "geqo_effort", Type: Integer, BootDefault: "5", IntegerMin: 1, IntegerMax: 10, Category: geqoCategory, Description: "GEQO: effort is used to set the default for other GEQO parameters."},
	{Name: "geqo_pool_size", Type: Integer, BootDefault: "0", IntegerMax: 2147483647, Category: geqoCategory, Description: "GEQO: number of individuals in the population.", ExtraDescription: "0 means use a suitable default value."},
	{Name: "geqo_generations", Type: Integer, BootDefault: "0", IntegerMax: 2147483647, Category: geqoCategory, Description: "GEQO: number of iterations of the algorithm.", ExtraDescription: "0 means use a suitable default value."},
	{Name: "geqo_selection_bias", Type: Floating, BootDefault: "2", FloatMin: 1.5, FloatMax: 2, Category: geqoCategory, Description: "GEQO: selective pressure within the population."},
	{Name: "geqo_seed", Type: Floating, BootDefault: "0", FloatMax: 1, Category: geqoCategory, Description: "GEQO: seed for random path selection."},
}
