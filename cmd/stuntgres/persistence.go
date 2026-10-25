package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"stuntgres/internal/engine"
)

const dumpTimestampLayout = "20060102150405"

// A database name may itself contain "--". Only the final timestamp suffix
// identifies a published dump; temporary files and unrelated SQL are not dumps.
func parseDumpFilename(filename string) (string, time.Time, bool) {
	if !strings.HasSuffix(filename, ".sql") {
		return "", time.Time{}, false
	}
	stem := strings.TrimSuffix(filename, ".sql")
	separator := strings.LastIndex(stem, "--")
	if separator < 0 {
		return "", time.Time{}, false
	}
	name, suffix := stem[:separator], stem[separator+2:]
	if len(suffix) != 14 || validName(name, true) != nil {
		return "", time.Time{}, false
	}
	for _, digit := range suffix {
		if digit < '0' || digit > '9' {
			return "", time.Time{}, false
		}
	}
	stamp, err := time.Parse(dumpTimestampLayout, suffix)
	if err != nil || stamp.Year() < 1 {
		return "", time.Time{}, false
	}
	return name, stamp, true
}

func latestDumps(dir string) (map[string]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	latest := make(map[string]string)
	timestamps := make(map[string]time.Time)
	for _, entry := range entries {
		name, stamp, ok := parseDumpFilename(entry.Name())
		if !ok || entry.Type()&os.ModeSymlink != 0 {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return nil, fmt.Errorf("inspect dump %q: %w", entry.Name(), err)
		}
		if !info.Mode().IsRegular() {
			continue
		}
		if previous, exists := timestamps[name]; !exists || stamp.After(previous) {
			timestamps[name] = stamp
			latest[name] = filepath.Join(dir, entry.Name())
		}
	}
	return latest, nil
}

func restoreDatabases(ctx context.Context, databases map[string]*database, latest map[string]string, logs *serverLogs) error {
	names := make([]string, 0, len(latest))
	for name := range latest {
		names = append(names, name)
	}
	sort.Strings(names)
	var result error
	for _, name := range names {
		path := latest[name]
		db := databases[name]
		var err error
		if db == nil {
			err = fmt.Errorf("restore database %q: database was not initialized", name)
		} else {
			err = restoreDatabase(ctx, db, path)
		}
		level, message := slog.LevelInfo, "database restored"
		args := []any{"database", name, "path", path}
		if err != nil {
			level, message = slog.LevelError, "database restore failed"
			args = append(args, "error", err.Error())
			result = errors.Join(result, fmt.Errorf("restore database %q: %w", name, err))
		}
		result = errors.Join(result, logs.operational(ctx, level, message, args...))
	}
	return result
}

func restoreDatabase(ctx context.Context, db *database, path string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	// Restore owns SQL and owner validation. In particular, a corrupt newest
	// dump is never retried with an older file or a different SQL principal.
	err = engine.Restore(ctx, db.engine, file)
	return errors.Join(err, file.Close())
}

func dumpDatabases(ctx context.Context, databases map[string]*database, dir string, logs *serverLogs) error {
	var result error
	for _, name := range databaseNames(databases) {
		db := databases[name]
		path, err := publishDump(dir, name, time.Now(), func(dst io.Writer) error {
			return engine.Dump(ctx, db.engine, dst)
		})
		level, message := slog.LevelInfo, "database dumped"
		args := []any{"database", name, "path", path}
		if err != nil {
			level, message = slog.LevelError, "database dump failed"
			args = append(args, "error", err.Error())
			result = errors.Join(result, fmt.Errorf("dump database %q: %w", name, err))
		}
		result = errors.Join(result, logs.operational(ctx, level, message, args...))
	}
	return result
}

// publishDump makes a complete, synced native dump visible in one operation.
// Link, rather than Rename, supplies atomic no-replace publication on every
// supported filesystem. Lifecycle callers serialize dumps; a concurrent name
// collision is still handled without replacing the competing file.
func publishDump(dir, name string, now time.Time, dump func(io.Writer) error) (path string, err error) {
	if err := validName(name, true); err != nil {
		return "", err
	}
	latest, err := latestDumps(dir)
	if err != nil {
		return "", err
	}
	stamp := now.UTC().Truncate(time.Second)
	if newest := latest[name]; newest != "" {
		_, previous, _ := parseDumpFilename(filepath.Base(newest))
		if !stamp.After(previous) {
			stamp = previous.Add(time.Second)
		}
	}
	file, err := os.CreateTemp(dir, ".stuntgres-dump-*")
	if err != nil {
		return "", err
	}
	temporary := file.Name()
	defer func() {
		if file != nil {
			err = errors.Join(err, file.Close())
		}
		if removeErr := os.Remove(temporary); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
			err = errors.Join(err, removeErr)
		}
	}()
	if err := dump(file); err != nil {
		return "", err
	}
	if err := file.Sync(); err != nil {
		return "", err
	}
	closeErr := file.Close()
	file = nil
	if closeErr != nil {
		return "", closeErr
	}
	for {
		if stamp.Year() < 1 || stamp.Year() > 9999 {
			return "", errors.New("dump timestamp exceeds the published filename range")
		}
		candidate := filepath.Join(dir, name+"--"+stamp.Format(dumpTimestampLayout)+".sql")
		if err := os.Link(temporary, candidate); errors.Is(err, os.ErrExist) {
			stamp = stamp.Add(time.Second)
			continue
		} else if err != nil {
			return "", err
		}
		path = candidate
		break
	}
	if err := os.Remove(temporary); err != nil {
		return path, err
	}
	directory, err := os.Open(dir)
	if err != nil {
		return path, err
	}
	return path, errors.Join(directory.Sync(), directory.Close())
}
