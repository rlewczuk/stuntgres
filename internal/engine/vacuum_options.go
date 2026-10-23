package engine

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"stuntgres/internal/engine/settings"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

// VacuumOptions are one VACUUM statement's normalized options; the last
// occurrence of an option wins.
type VacuumOptions struct {
	Freeze, Verbose, Analyze, Full, SkipLocked bool
	ProcessMain, ProcessToast, DisableSkipping bool
	SkipDatabase, OnlyDatabase, Buffer         bool
	Parallel                                   int
}

func vacuumBoolean(v parser.Value) (bool, bool) {
	switch v.Kind {
	case parser.BooleanValue:
		return v.Boolean, true
	case parser.IntegerValue:
		if v.Integer == 0 || v.Integer == 1 {
			return v.Integer == 1, true
		}
	case parser.StringValue:
		switch strings.ToLower(v.Text) {
		case "true", "on":
			return true, true
		case "false", "off":
			return false, true
		}
	}
	return false, false
}
func vacuumOptionError(code, message string, pos parser.Position) error {
	d := sqlError(code, message).(*sqlstate.Error)
	if pos.Character > 0 {
		d.Position = strconv.Itoa(pos.Character)
	}
	return d
}

// NormalizeVacuum validates s's options in order, then their combinations, and
// returns the first error PostgreSQL reports.
func NormalizeVacuum(s *parser.Vacuum) (VacuumOptions, error) {
	o := VacuumOptions{ProcessMain: true, ProcessToast: true}
	for _, v := range s.Options {
		name := v.Name.Text
		var destination *bool
		switch name {
		case "freeze":
			destination = &o.Freeze
		case "verbose":
			destination = &o.Verbose
		case "analyze", "analyse":
			destination = &o.Analyze
		case "full":
			destination = &o.Full
		case "skip_locked":
			destination = &o.SkipLocked
		case "process_main":
			destination = &o.ProcessMain
		case "process_toast":
			destination = &o.ProcessToast
		case "disable_page_skipping":
			destination = &o.DisableSkipping
		case "skip_database_stats":
			destination = &o.SkipDatabase
		case "only_database_stats":
			destination = &o.OnlyDatabase
		case "truncate":
			if _, ok := vacuumBoolean(v.Value); !ok {
				return o, sqlError("42601", name+" requires a Boolean value")
			}
		case "index_cleanup":
			if !v.HasValue || strings.EqualFold(v.Value.Text, "auto") {
				continue
			}
			if _, ok := vacuumBoolean(v.Value); !ok {
				return o, sqlError("42601", name+" requires a Boolean value")
			}
		case "parallel":
			if !v.HasValue {
				return o, vacuumOptionError("42601", "parallel option requires a value between 0 and 1024", v.Name.Pos)
			}
			n, err := strconv.ParseInt(v.Value.Text, 10, 32)
			if err != nil {
				return o, sqlError("42601", "parallel requires an integer value")
			}
			if n < 0 || n > 1024 {
				return o, vacuumOptionError("42601", "parallel workers for vacuum must be between 0 and 1024", v.Name.Pos)
			}
			o.Parallel = int(n)
		case "buffer_usage_limit":
			o.Buffer = true
			if err := vacuumBuffer(v); err != nil {
				return o, err
			}
		default:
			return o, vacuumOptionError("42601", fmt.Sprintf(`unrecognized VACUUM option "%s"`, name), v.Name.Pos)
		}
		if destination != nil {
			value, ok := vacuumBoolean(v.Value)
			if !ok {
				return o, sqlError("42601", name+" requires a Boolean value")
			}
			*destination = value
		}
	}
	incompatible := func(message string) (VacuumOptions, error) { return o, sqlError("0A000", message) }
	if o.Full && o.Parallel > 0 {
		return incompatible("VACUUM FULL cannot be performed in parallel")
	}
	if o.Full && o.Buffer && !o.Analyze {
		return incompatible("BUFFER_USAGE_LIMIT cannot be specified for VACUUM FULL")
	}
	if !o.Analyze {
		for _, t := range s.Targets {
			if len(t.Columns) > 0 {
				return incompatible("ANALYZE option must be specified when a column list is provided")
			}
		}
	}
	if o.Full && o.DisableSkipping {
		return incompatible("VACUUM option DISABLE_PAGE_SKIPPING cannot be used with FULL")
	}
	if o.Full && !o.ProcessToast {
		return incompatible("PROCESS_TOAST required with VACUUM FULL")
	}
	if o.OnlyDatabase {
		if len(s.Targets) > 0 {
			return incompatible("ONLY_DATABASE_STATS cannot be specified with a list of tables")
		}
		if o.Full || o.Freeze || o.Analyze || o.SkipDatabase || o.DisableSkipping || o.SkipLocked {
			return incompatible("ONLY_DATABASE_STATS cannot be specified with other VACUUM options")
		}
	}
	return o, nil
}
func vacuumBuffer(v parser.VacuumOption) error {
	d := sqlError("22023", "BUFFER_USAGE_LIMIT option must be 0 or between 128 kB and 16777216 kB").(*sqlstate.Error)
	if !v.HasValue {
		return sqlError("42601", "buffer_usage_limit requires a parameter")
	}
	n, unit, ok := settings.ParseNumber(v.Value.Text, true)
	if !ok {
		return d
	}
	if n < math.MinInt32 || n > math.MaxInt32 {
		d.Hint = "Value exceeds integer range."
		return d
	}
	switch unit {
	case "", "kB":
	case "B":
		n /= 1024
	case "MB":
		n *= 1024
	case "GB":
		n *= 1024 * 1024
	case "TB":
		n *= 1024 * 1024 * 1024
	default:
		d.Hint = `Valid units for this parameter are "B", "kB", "MB", "GB", and "TB".`
		return d
	}
	n = math.RoundToEven(n)
	if n < math.MinInt32 || n > math.MaxInt32 {
		d.Hint = "Value exceeds integer range."
		return d
	}
	if n != 0 && (n < 128 || n > 16777216) {
		return d
	}
	return nil
}

// CheckVacuumEntry rejects VACUUM inside a transaction block, then FULL, then
// ANALYZE, which the engine does not perform.
func CheckVacuumEntry(o VacuumOptions, inBlock bool) error {
	if inBlock {
		return sqlError("25001", "VACUUM cannot run inside a transaction block")
	}
	if o.Full {
		return sqlError("0A000", "VACUUM FULL is not supported")
	}
	if o.Analyze {
		return sqlError("0A000", "VACUUM ANALYZE is not supported")
	}
	return nil
}
