// Package identfile reads an explicitly attached PostgreSQL identity mapping file.
// It parses configuration only; it never authenticates or matches identities.
package identfile

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"

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
type record struct {
	file       string
	line       int32
	fields     [][]string
	diagnostic string
}

// Read captures current bytes into independent rows. A nil Source is unattached.
func (s *Source) Read(ctx context.Context) ([]Row, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if s == nil {
		return nil, nil
	}
	records, err := read(ctx, s.path, 0)
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
		row := Row{File: r.file, Line: r.line, Error: r.diagnostic}
		if row.Error == "" {
			for i := range 3 {
				if i >= len(r.fields) {
					row.Error = "missing entry at end of line"
					break
				}
				if len(r.fields[i]) != 1 {
					row.Error = "multiple values in ident field"
					break
				}
			}
		}
		if row.Error == "" {
			for _, field := range r.fields[1:3] {
				if !strings.HasPrefix(field[0], "/") {
					continue
				}
				pattern := field[0][1:]
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
			row.Map = r.fields[0][0]
			row.System = r.fields[1][0]
			row.Username = r.fields[2][0]
		}
		rows = append(rows, row)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return rows, nil
}

func fileError(operation, path string, err error) error {
	code := "XX000"
	reason := ""
	switch {
	case errors.Is(err, os.ErrNotExist):
		code = "58P01"
		reason = "No such file or directory"
	case errors.Is(err, os.ErrPermission):
		code = "42501"
		reason = "Permission denied"
	case errors.Is(err, syscall.EISDIR):
		code = "42809"
		reason = "Is a directory"
	case errors.Is(err, syscall.ENOTDIR):
		code = "42809"
		reason = "Not a directory"
	case errors.Is(err, syscall.EMFILE), errors.Is(err, syscall.ENFILE):
		code = "53000"
	case errors.Is(err, syscall.EROFS):
		code = "42501"
	case errors.Is(err, syscall.EEXIST):
		code = "58P02"
	case errors.Is(err, syscall.ENOTEMPTY):
		code = "42809"
	case errors.Is(err, syscall.ENOSPC):
		code = "53100"
	case errors.Is(err, syscall.ENOMEM):
		code = "53200"
	case errors.Is(err, syscall.EIO):
		code = "58030"
	case errors.Is(err, syscall.ENAMETOOLONG):
		code = "58P03"
	}
	if reason == "" {
		var pe *os.PathError
		if errors.As(err, &pe) {
			err = pe.Err
		}
		reason = err.Error()
		if len(reason) > 0 {
			reason = strings.ToUpper(reason[:1]) + reason[1:]
		}
	}
	return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: code, Message: fmt.Sprintf("could not %s file \"%s\": %s", operation, path, reason)}
}
func resolve(parent, path string) string {
	if filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(filepath.Dir(parent), path)
}
func read(ctx context.Context, path string, depth int) ([]record, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if depth > 10 {
		return nil, &sqlstate.Error{Message: fmt.Sprintf("could not open file \"%s\": maximum nesting depth exceeded", path)}
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, fileError("open", path, err)
	}
	defer f.Close()
	reader := bufio.NewReader(f)
	var records []record
	var logical []byte
	line := int32(1)
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		start := line
		logical = logical[:0]
		lastBackslash := 0
		eof := false
		for {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			physical, err := reader.ReadString('\n')
			if err != nil && err != io.EOF {
				return nil, fileError("read", path, err)
			}
			if len(physical) == 0 && err == io.EOF {
				eof = true
				break
			}
			line++
			logical = append(logical, physical...)
			// PostgreSQL strips every trailing CR/LF from the accumulated
			// logical buffer, including a CR exposed by an earlier continuation.
			for len(logical) > 0 && (logical[len(logical)-1] == '\r' || logical[len(logical)-1] == '\n') {
				logical = logical[:len(logical)-1]
			}
			if len(logical) > lastBackslash && logical[len(logical)-1] == '\\' {
				logical = logical[:len(logical)-1]
				lastBackslash = len(logical)
				continue
			}
			if err == io.EOF {
				eof = true
			}
			break
		}
		fields, diagnostic, err := fields(ctx, path, string(logical), depth)
		if err != nil {
			return nil, err
		}
		if len(fields) > 0 || diagnostic != "" {
			r := record{file: path, line: start, fields: fields, diagnostic: diagnostic}
			directive := false
			if diagnostic == "" && len(fields) == 2 {
				name, arg := fields[0][0], fields[1][0]
				if name == "include" || name == "include_if_exists" {
					directive = true
					included, err := read(ctx, resolve(path, arg), depth+1)
					if err != nil {
						if ctx.Err() != nil {
							return nil, ctx.Err()
						}
						var se *sqlstate.Error
						if name == "include_if_exists" && errors.As(err, &se) && se.Code == "58P01" {
						} else {
							r.diagnostic = message(err)
						}
					} else {
						records = append(records, included...)
					}
				} else if name == "include_dir" {
					directive = true
					targets, diagnostic, err := directoryFiles(ctx, path, arg)
					if err != nil {
						return nil, err
					}
					r.diagnostic = diagnostic
					if diagnostic == "" {
						var failures []string
						// tokenize_include_file leaves its caller's err_msg
						// unchanged on success. The directory loop appends
						// that retained message after every selected file.
						lastFailure := ""
						for _, target := range targets {
							included, err := read(ctx, target, depth+1)
							if err != nil {
								if ctx.Err() != nil {
									return nil, ctx.Err()
								}
								lastFailure = message(err)
							} else {
								records = append(records, included...)
							}
							if lastFailure != "" {
								failures = append(failures, lastFailure)
							}
						}
						r.diagnostic = strings.Join(failures, "\n")
					}
				}
			}
			if !directive || r.diagnostic != "" {
				records = append(records, r)
			}
		}
		if eof {
			break
		}
	}
	return records, nil
}
func message(err error) string {
	var se *sqlstate.Error
	if errors.As(err, &se) {
		return se.Message
	}
	return err.Error()
}

// Resolve/stat the entire directory before parsing any of its files, matching
// GetConfFilesInDir's all-or-nothing discovery and bytewise filename ordering.
func directoryFiles(ctx context.Context, parent, name string) ([]string, string, error) {
	if strings.Trim(name, " \t\r\n") == "" {
		return nil, "empty configuration directory name", nil
	}
	directory := resolve(parent, name)
	entries, err := os.ReadDir(directory)
	if err != nil {
		return nil, fmt.Sprintf("could not open directory \"%s\"", directory), nil
	}
	var targets []string
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return nil, "", err
		}
		name := entry.Name()
		if len(name) < 6 || name[0] == '.' || !strings.HasSuffix(name, ".conf") {
			continue
		}
		target := filepath.Join(directory, name)
		info, err := os.Stat(target)
		if err != nil {
			return nil, fmt.Sprintf("could not stat file \"%s\"", target), nil
		}
		if !info.IsDir() {
			targets = append(targets, target)
		}
	}
	return targets, "", nil
}

func fields(ctx context.Context, path, text string, depth int) ([][]string, string, error) {
	var result [][]string
	pos := 0
	for pos < len(text) {
		var field []string
		for {
			token, quoted, comma, ok := nextToken(text, &pos)
			if !ok {
				break
			}
			if !quoted && len(token) > 1 && token[0] == '@' {
				records, err := read(ctx, resolve(path, token[1:]), depth+1)
				if err != nil {
					if ctx.Err() != nil {
						return nil, "", ctx.Err()
					}
					return result, message(err), nil
				}
				for _, r := range records {
					if r.diagnostic != "" {
						return result, r.diagnostic, nil
					}
					for _, f := range r.fields {
						field = append(field, f...)
					}
				}
			} else {
				field = append(field, token)
			}
			if !comma {
				break
			}
		}
		if len(field) > 0 {
			result = append(result, field)
		}
		if err := ctx.Err(); err != nil {
			return nil, "", err
		}
	}
	return result, "", nil
}
func nextToken(text string, pos *int) (string, bool, bool, bool) {
	for *pos < len(text) && (authBlank(text[*pos]) || text[*pos] == ',') {
		(*pos)++
	}
	var token strings.Builder
	inQuote, wasQuote, sawQuote, initial := false, false, false, false
	for *pos < len(text) {
		c := text[*pos]
		if !inQuote && authBlank(c) {
			(*pos)++
			break
		}
		if !inQuote && c == '#' {
			*pos = len(text)
			break
		}
		if !inQuote && c == ',' {
			return token.String(), initial, true, sawQuote || token.Len() > 0
		}
		if c != '"' || wasQuote {
			token.WriteByte(c)
		}
		if inQuote && c == '"' {
			wasQuote = !wasQuote
		} else {
			wasQuote = false
		}
		if c == '"' {
			inQuote = !inQuote
			sawQuote = true
			if token.Len() == 0 {
				initial = true
			}
		}
		(*pos)++
	}
	return token.String(), initial, false, sawQuote || token.Len() > 0
}

// pg_isblank in the authentication tokenizer includes CR, but not LF/VT/FF.
func authBlank(c byte) bool { return c == ' ' || c == '\t' || c == '\r' }
