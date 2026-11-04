package settings

import (
	"fmt"
	"sort"
	"sync"

	"stuntgres/internal/sqlstate"
)

// Registry owns shared metadata, immutable after construction. Value copies
// intentionally share its map and slice; readers must not mutate them.
type Registry struct {
	definitions map[string]Definition
	order       []string
	defaults    *sessionDefaults
}

// Startup is the immutable startup configuration a registry boots from.
type Startup struct {
	TrackCommitTimestamps bool // track_commit_timestamp boots on.
	LogicalWAL            bool // wal_level starts as logical, as with -c wal_level=logical.
}

// builtinRegistries holds the registry without registered definitions, built
// once per startup configuration. Registries are immutable, so every engine
// shares it.
var builtinRegistries [4]struct {
	once     sync.Once
	registry Registry
}

// NewRegistry captures builtin boot values and adds validated registered
// definitions. track_commit_timestamp boots from trackCommitTimestamps.
func NewRegistry(registered []Definition, trackCommitTimestamps bool) Registry {
	return NewStartupRegistry(registered, Startup{TrackCommitTimestamps: trackCommitTimestamps})
}

// NewStartupRegistry is NewRegistry for a complete startup configuration.
func NewStartupRegistry(registered []Definition, startup Startup) Registry {
	if len(registered) != 0 {
		return newRegistry(registered, startup)
	}
	index := 0
	if startup.TrackCommitTimestamps {
		index |= 1
	}
	if startup.LogicalWAL {
		index |= 2
	}
	shared := &builtinRegistries[index]
	shared.once.Do(func() { shared.registry = newRegistry(nil, startup) })
	return shared.registry
}

func newRegistry(registered []Definition, startup Startup) Registry {
	groups := [...][]Definition{builtins, developerBuiltins, loggingBuiltins, plannerBuiltins, presetBuiltins, replicationBuiltins, resourceBuiltins, plannerCostBuiltins, plannerOtherBuiltins, geqoBuiltins, shortBuiltins, vacuumBuiltins, walBuiltins, pgStatStatementsDefinitions, registered}
	r := Registry{defaults: &sessionDefaults{}, definitions: make(map[string]Definition, len(builtins)+len(developerBuiltins)+len(loggingBuiltins)+len(plannerBuiltins)+len(presetBuiltins)+len(replicationBuiltins)+len(resourceBuiltins)+len(plannerCostBuiltins)+len(plannerOtherBuiltins)+len(geqoBuiltins)+len(shortBuiltins)+len(vacuumBuiltins)+len(walBuiltins)+len(pgStatStatementsDefinitions)+len(registered))}
	for i, list := range groups {
		for _, d := range list {
			if i < len(groups)-1 {
				d.Flags = builtinFlags(d.Name)
				if i == 0 {
					d = builtinMetadata(d)
				}
				if d.Name == "track_commit_timestamp" && startup.TrackCommitTimestamps {
					d.BootDefault = "on"
				}
				if d.Name == "wal_level" && startup.LogicalWAL {
					d.NativeStartup = &StartupDefault{Value: "logical", Source: "command line"}
				}
				// Boot metadata can predate database initialization and need not
				// satisfy the live setter's capability checks.
				var v Value
				var err error
				if d.NativeStartup != nil && d.Type == String {
					v.Text = d.BootDefault
				} else {
					v, err = d.Parse(d.BootDefault)
				}
				if err != nil {
					panic(err)
				} // Builtin definitions are source-owned constants.
				d.bootValue = v
				d.BootDefault = d.Format(v)
				if d.NativeStartup != nil {
					d.startupValue, err = d.Parse(d.NativeStartup.Value)
					if err != nil {
						panic(err)
					}
				}
			}
			r.definitions[d.Name] = d
			r.order = append(r.order, d.Name)
		}
	}
	sort.Strings(r.order)
	return r
}

// Lookup finds a definition by case-folded name, or fails with 42704.
func (r Registry) Lookup(name string) (Definition, error) {
	if d, ok := r.definitions[FoldName(name)]; ok {
		return d, nil
	}
	return Definition{}, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42704", Message: fmt.Sprintf(`unrecognized configuration parameter "%s"`, name)}
}

// Definitions borrows immutable definitions, including their enum and boot paths.
// Neither the map nor any backing slices may be changed by the caller.
func (r Registry) Definitions() map[string]Definition { return r.definitions }

// Order borrows the immutable sorted names; callers must not change the slice.
func (r Registry) Order() []string { return r.order }
