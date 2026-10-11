// Command checkpackages selects real-reference test packages without running tests.
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

type testPackage struct {
	Dir          string
	ImportPath   string
	TestGoFiles  []string
	XTestGoFiles []string
}

func main() {
	if len(os.Args) != 2 {
		fail(fmt.Errorf("usage: checkpackages RUN"))
	}
	packages, err := selectPackages(os.Args[1])
	if err != nil {
		fail(err)
	}
	fmt.Println(strings.Join(packages, " "))
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "checkpackages:", err)
	os.Exit(1)
}

func selectPackages(pattern string) ([]string, error) {
	matcher, err := regexp.Compile(pattern)
	if err != nil {
		return nil, err
	}
	// Go splits subtest selectors on unbracketed slashes. Never approximate
	// that matching grammar or infer dynamically registered subtest names.
	all := strings.Contains(pattern, "/")
	cmd := exec.Command("go", "list", "-json", "-tags", "reference", "./reference/...")
	cmd.Stderr = os.Stderr
	data, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("discover reference packages: %w", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	var selected []string
	var fixture string
	for {
		var pkg testPackage
		if err := decoder.Decode(&pkg); err == io.EOF {
			break
		} else if err != nil {
			return nil, fmt.Errorf("decode reference package: %w", err)
		}
		if len(pkg.TestGoFiles)+len(pkg.XTestGoFiles) == 0 {
			continue
		}
		if strings.HasSuffix(pkg.ImportPath, "/reference/fixture") {
			fixture = pkg.ImportPath
		}
		if all {
			selected = append(selected, pkg.ImportPath)
			continue
		}
		matched := false
		for _, files := range [][]string{pkg.TestGoFiles, pkg.XTestGoFiles} {
			if matched {
				break
			}
			for _, name := range files {
				if matched {
					break
				}
				file, err := parser.ParseFile(token.NewFileSet(), filepath.Join(pkg.Dir, name), nil, parser.SkipObjectResolution)
				if err != nil {
					return nil, fmt.Errorf("parse %s: %w", name, err)
				}
				for _, declaration := range file.Decls {
					function, ok := declaration.(*ast.FuncDecl)
					if ok && function.Recv == nil && isTest(function.Name.Name) && matcher.MatchString(function.Name.Name) {
						matched = true
						break
					}
				}
			}
		}
		if matched {
			selected = append(selected, pkg.ImportPath)
		}
	}
	if len(selected) == 0 {
		if fixture == "" {
			return nil, fmt.Errorf("no reference fixture test package found for eager no-match startup")
		}
		selected = append(selected, fixture)
	}
	return selected, nil
}

func isTest(name string) bool {
	if name == "TestMain" || !strings.HasPrefix(name, "Test") {
		return false
	}
	r, _ := utf8.DecodeRuneInString(name[len("Test"):])
	return !unicode.IsLower(r)
}
