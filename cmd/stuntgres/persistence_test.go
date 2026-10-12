package main

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestServerPersistenceFiles(t *testing.T) {
	t.Run("filename", func(t *testing.T) {
		cases := []struct {
			filename string
			name     string
			valid    bool
		}{
			{"db--20260228123456.sql", "db", true},
			{"db--archive--20240229000000.sql", "db--archive", true},
			{"Unicode_世界--20260101000000.sql", "Unicode_世界", true},
			{"db--20260229000000.sql", "", false},
			{"db--20260101240000.sql", "", false},
			{"db--20260101006000.sql", "", false},
			{"db--20260101000060.sql", "", false},
			{"db--00000101000000.sql", "", false},
			{"db--2026010100000.sql", "", false},
			{"db--202601010000000.sql", "", false},
			{"db--2026010100000a.sql", "", false},
			{"db--20260101000000.SQL", "", false},
			{"db--20260101000000.sql.tmp", "", false},
			{"--20260101000000.sql", "", false},
			{"..--20260101000000.sql", "", false},
			{"a/b--20260101000000.sql", "", false},
			{"a\\b--20260101000000.sql", "", false},
			{"a:b--20260101000000.sql", "", false},
			{"a,b--20260101000000.sql", "", false},
			{strings.Repeat("d", 64) + "--20260101000000.sql", "", false},
			{"unrelated.sql", "", false},
		}
		for _, tc := range cases {
			t.Run(tc.filename, func(t *testing.T) {
				name, stamp, ok := parseDumpFilename(tc.filename)
				if ok != tc.valid || name != tc.name {
					t.Fatalf("parse = (%q, %v), want (%q, %v)", name, ok, tc.name, tc.valid)
				}
				if ok && (stamp.Location() != time.UTC || name+"--"+stamp.Format(dumpTimestampLayout)+".sql" != tc.filename) {
					t.Fatalf("timestamp did not roundtrip in UTC: %v", stamp)
				}
			})
		}
	})

	t.Run("latest regular only", func(t *testing.T) {
		dir := t.TempDir()
		for _, name := range []string{
			"db--20260101000000.sql", "db--20260102000000.sql",
			"db--archive--20260103000000.sql", "db--20260230000000.sql",
			"db--20260104000000.sql.tmp", ".stuntgres-dump-incomplete", "unrelated.sql",
		} {
			if err := os.WriteFile(filepath.Join(dir, name), []byte("not parsed as SQL by this mechanical test"), 0600); err != nil {
				t.Fatal(err)
			}
		}
		if err := os.Mkdir(filepath.Join(dir, "db--20260105000000.sql"), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(filepath.Join(dir, "db--20260101000000.sql"), filepath.Join(dir, "db--20260106000000.sql")); err != nil {
			t.Fatal(err)
		}
		latest, err := latestDumps(dir)
		if err != nil {
			t.Fatal(err)
		}
		if len(latest) != 2 || latest["db"] != filepath.Join(dir, "db--20260102000000.sql") || latest["db--archive"] != filepath.Join(dir, "db--archive--20260103000000.sql") {
			t.Fatalf("latest = %#v", latest)
		}
		// Selection is by filename, not content: corrupt newest files must reach
		// native Restore, never be prefiltered in favor of an older dump.
		if _, err := latestDumps(filepath.Join(dir, "missing")); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("missing directory error = %v", err)
		}
	})

	t.Run("publication and monotonic names", func(t *testing.T) {
		dir := t.TempDir()
		now := time.Date(2026, 1, 2, 3, 4, 5, 999, time.FixedZone("offset", 3600))
		contents := []string{"first complete file", "second complete file", "third complete file"}
		for i, content := range contents {
			clock := now
			if i == 2 {
				clock = now.Add(-24 * time.Hour)
			}
			path, err := publishDump(dir, "db--archive", clock, func(dst io.Writer) error {
				entries, err := os.ReadDir(dir)
				if err != nil {
					return err
				}
				published := 0
				for _, entry := range entries {
					if strings.HasSuffix(entry.Name(), ".sql") {
						published++
					}
				}
				if published != i {
					t.Errorf("partial publication visible: %d published, want %d", published, i)
				}
				_, err = io.WriteString(dst, content)
				return err
			})
			if err != nil {
				t.Fatal(err)
			}
			want := filepath.Join(dir, "db--archive--"+now.UTC().Add(time.Duration(i)*time.Second).Format(dumpTimestampLayout)+".sql")
			if path != want {
				t.Fatalf("published path = %q, want %q", path, want)
			}
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatal(err)
		}
		if len(entries) != len(contents) {
			t.Fatalf("temporary files remain: %v", entries)
		}
		for i, entry := range entries {
			path := filepath.Join(dir, entry.Name())
			data, err := os.ReadFile(path)
			if err != nil || string(data) != contents[i] {
				t.Fatalf("contents %q = %q, %v", path, data, err)
			}
			info, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			if info.Mode().Perm() != 0600 {
				t.Fatalf("mode = %o, want 600", info.Mode().Perm())
			}
		}
	})

	t.Run("no replace on collision", func(t *testing.T) {
		dir := t.TempDir()
		now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
		collision := filepath.Join(dir, "db--20260101000000.sql")
		path, err := publishDump(dir, "db", now, func(dst io.Writer) error {
			// Introduce a collision after selection, rather than merely seed the
			// directory with a file the selection pass would already see.
			if err := os.WriteFile(collision, []byte("competing file"), 0600); err != nil {
				return err
			}
			_, err := io.WriteString(dst, "new complete file")
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
		if path != filepath.Join(dir, "db--20260101000001.sql") {
			t.Fatalf("collision path = %q", path)
		}
		data, err := os.ReadFile(collision)
		if err != nil || string(data) != "competing file" {
			t.Fatalf("competing file overwritten: %q, %v", data, err)
		}
	})

	t.Run("failed output stays unpublished", func(t *testing.T) {
		dir := t.TempDir()
		failure := errors.New("writer failed")
		path, err := publishDump(dir, "db", time.Now(), func(dst io.Writer) error {
			if _, err := io.WriteString(dst, "partial file"); err != nil {
				return err
			}
			return failure
		})
		if path != "" || !errors.Is(err, failure) {
			t.Fatalf("failed publication = (%q, %v)", path, err)
		}
		entries, err := os.ReadDir(dir)
		if err != nil || len(entries) != 0 {
			t.Fatalf("partial files remain: %v, %v", entries, err)
		}
	})

	t.Run("timestamp exhaustion", func(t *testing.T) {
		dir := t.TempDir()
		previous := filepath.Join(dir, "db--99991231235959.sql")
		if err := os.WriteFile(previous, []byte("preserved"), 0600); err != nil {
			t.Fatal(err)
		}
		path, err := publishDump(dir, "db", time.Now(), func(dst io.Writer) error {
			_, err := io.WriteString(dst, "complete but unpublishable")
			return err
		})
		if err == nil || path != "" {
			t.Fatalf("exhausted timestamp = (%q, %v)", path, err)
		}
		entries, err := os.ReadDir(dir)
		if err != nil || len(entries) != 1 || entries[0].Name() != filepath.Base(previous) {
			t.Fatalf("unexpected exhausted timestamp files: %v, %v", entries, err)
		}
	})
}
