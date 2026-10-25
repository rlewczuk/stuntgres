package main

import (
	"bufio"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// referencePassword returns the URL password, or else the first matching
// libpq password-file entry (PGPASSFILE, else the platform default file).
// A missing file is not an error; the server then rejects the login.
func referencePassword(c *referenceConfig) (string, bool, error) {
	if c.passwordSet {
		return c.password, true, nil
	}
	path := os.Getenv("PGPASSFILE")
	if path == "" {
		if runtime.GOOS == "windows" {
			path = filepath.Join(os.Getenv("APPDATA"), "postgresql", "pgpass.conf")
		} else {
			home, err := os.UserHomeDir()
			if err != nil {
				return "", false, nil
			}
			path = filepath.Join(home, ".pgpass")
		}
	}
	return lookupPassfile(path, c.host, c.port, c.dbname, c.user)
}

func lookupPassfile(path, host, port, dbname, user string) (string, bool, error) {
	file, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return "", false, err
	}
	if !info.Mode().IsRegular() {
		return "", false, fmt.Errorf("password file %q is not a plain file", path)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
		return "", false, fmt.Errorf("password file %q has group or world access; permissions should be u=rw (0600) or less", path)
	}
	scanner := bufio.NewScanner(file)
	want := [4]string{host, port, dbname, user}
	for scanner.Scan() {
		line := strings.TrimRight(scanner.Text(), "\r")
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields, ok := passfileFields(line)
		if !ok {
			continue
		}
		matched := true
		for i, value := range want {
			if fields[i] != "*" && fields[i] != value {
				matched = false
				break
			}
		}
		if matched {
			return fields[4], true, nil
		}
	}
	return "", false, scanner.Err()
}

// passfileFields splits host:port:database:user:password, honoring "\:"
// and "\\" escapes. The password field takes the remainder of the line.
func passfileFields(line string) ([5]string, bool) {
	var fields [5]string
	var current strings.Builder
	index := 0
	for i := 0; i < len(line); i++ {
		b := line[i]
		switch {
		case b == '\\' && i+1 < len(line):
			i++
			current.WriteByte(line[i])
		case b == ':' && index < 4:
			fields[index] = current.String()
			current.Reset()
			index++
		default:
			current.WriteByte(b)
		}
	}
	if index != 4 {
		return fields, false
	}
	fields[4] = current.String()
	return fields, true
}
