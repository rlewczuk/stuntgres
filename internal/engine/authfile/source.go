// Package authfile owns PostgreSQL authentication-file tokenization and includes.
package authfile

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"stuntgres/internal/sqlstate"
)

type Token struct {
	Text            string
	InitiallyQuoted bool
}
type Record struct {
	File       string
	Line       int32
	Fields     [][]Token
	Diagnostic string
}

// Read captures an include graph. Cancellation never publishes a prefix.
func Read(ctx context.Context, path string) ([]Record, error) {
	records, err := read(ctx, path, 0)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return records, nil
}

func resolve(parent, path string) string {
	if filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(filepath.Dir(parent), path)
}
func read(ctx context.Context, path string, depth int) ([]Record, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if depth > 10 {
		return nil, &sqlstate.Error{Message: fmt.Sprintf("could not open file \"%s\": maximum nesting depth exceeded", path)}
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, FileError("open", path, err)
	}
	defer f.Close()
	reader := bufio.NewReader(f)
	var records []Record
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
				return nil, FileError("read", path, err)
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
			r := Record{File: path, Line: start, Fields: fields, Diagnostic: diagnostic}
			directive := false
			if diagnostic == "" && len(fields) == 2 {
				name, arg := fields[0][0].Text, fields[1][0].Text
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
							r.Diagnostic = message(err)
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
					r.Diagnostic = diagnostic
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
						r.Diagnostic = strings.Join(failures, "\n")
					}
				}
			}
			if !directive || r.Diagnostic != "" {
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
