// Package identfile reads an explicitly attached PostgreSQL identity mapping file.
// It parses configuration only; it never authenticates or matches identities.
package identfile

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"stuntgres/internal/engine/authfile"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// Source is immutable startup filesystem authority, including the include graph.
type Source struct{ path string }

func New(path string) *Source { return &Source{path: path} }

// Row owns its provenance and parsed strings. Number is zero for a diagnostic.
type Row struct {
	Number                int32
	File                  string
	Line                  int32
	Map, System, Username string
	Error                 string
}

// Read captures current bytes into independent rows. A nil Source is unattached.
func (s *Source) Read(ctx context.Context) ([]Row, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if s == nil {
		return nil, nil
	}
	records, err := authfile.Read(ctx, s.path)
	if err != nil {
		return nil, err
	}
	collation, err := sqltype.RegisteredCollation(950, 950)
	if err != nil {
		return nil, err
	}
	rows := make([]Row, 0, len(records))
	var number int32
	for _, r := range records {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		row := Row{File: r.File, Line: r.Line, Error: r.Diagnostic}
		if row.Error == "" {
			for i := range 3 {
				if i >= len(r.Fields) {
					row.Error = "missing entry at end of line"
					break
				}
				if len(r.Fields[i]) != 1 {
					row.Error = "multiple values in ident field"
					break
				}
			}
		}
		if row.Error == "" {
			for _, field := range r.Fields[1:3] {
				if !strings.HasPrefix(field[0].Text, "/") {
					continue
				}
				pattern := field[0].Text[1:]
				_, err = sqltype.CompilePGRegex(ctx, pattern, sqltype.PGRegexOptions{}, collation)
				if err != nil {
					var diagnostic *sqlstate.Error
					if !errors.As(err, &diagnostic) || diagnostic.Code != "2201B" {
						return nil, err
					}
					reason, ok := strings.CutPrefix(diagnostic.Message, "invalid regular expression: ")
					if !ok {
						return nil, err
					}
					row.Error = fmt.Sprintf("invalid regular expression \"%s\": %s", pattern, reason)
					break
				}
			}
		}
		if row.Error == "" {
			number++
			row.Number = number
			row.Map = r.Fields[0][0].Text
			row.System = r.Fields[1][0].Text
			row.Username = r.Fields[2][0].Text
		}
		rows = append(rows, row)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return rows, nil
}
