package settings

import (
	"fmt"
	"sort"

	"stuntgres/internal/sqlstate"
)

// Registry owns shared metadata, immutable after construction. Value copies
// intentionally share its map and slice; readers must not mutate them.
type Registry struct {
	definitions map[string]Definition
	order       []string
}

// NewRegistry captures builtin boot values and adds validated registered
// definitions. track_commit_timestamp boots from trackCommitTimestamps.
func NewRegistry(registered []Definition, trackCommitTimestamps bool) Registry {
	groups := [...][]Definition{builtins, developerBuiltins, loggingBuiltins, presetBuiltins, replicationBuiltins, resourceBuiltins, shortBuiltins, vacuumBuiltins, walBuiltins, registered}
	r := Registry{definitions: make(map[string]Definition, len(builtins)+len(developerBuiltins)+len(loggingBuiltins)+len(presetBuiltins)+len(replicationBuiltins)+len(resourceBuiltins)+len(shortBuiltins)+len(vacuumBuiltins)+len(walBuiltins)+len(registered))}
	for i, list := range groups {
		for _, d := range list {
			if i < len(groups)-1 {
				if i == 0 {
					d = builtinMetadata(d)
				}
				if d.Name == "track_commit_timestamp" && trackCommitTimestamps {
					d.BootDefault = "on"
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
