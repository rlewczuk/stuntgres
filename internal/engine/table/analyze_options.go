package table

import (
	"fmt"
	"stuntgres/internal/parser"
)

// AnalyzeOptions retains the last occurrence of each supported control.
// Buffer records a validated physical buffer-strategy control, which has no
// physical implementation in this in-memory engine; collection still runs.
type AnalyzeOptions struct {
	Verbose, SkipLocked, Buffer bool
}

// NormalizeAnalyze validates controls in encounter order using the same Boolean
// and buffer-size authority as VACUUM. Standalone ANALYZE has no VACUUM entry
// restrictions and is allowed inside an explicit transaction.
func NormalizeAnalyze(s *parser.Analyze) (AnalyzeOptions, error) {
	var o AnalyzeOptions
	for _, v := range s.Options {
		var destination *bool
		switch v.Name.Text {
		case "verbose":
			destination = &o.Verbose
		case "skip_locked":
			destination = &o.SkipLocked
		case "buffer_usage_limit":
			if err := vacuumBuffer(v); err != nil {
				return o, err
			}
			o.Buffer = true
		default:
			return o, vacuumOptionError("42601", fmt.Sprintf(`unrecognized ANALYZE option "%s"`, v.Name.Text), v.Name.Pos)
		}
		if destination != nil {
			value, ok := vacuumBoolean(v.Value)
			if !ok {
				return o, sqlError("42601", v.Name.Text+" requires a Boolean value")
			}
			*destination = value
		}
	}
	return o, nil
}
