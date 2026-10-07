// Command generate packages the exact pinned PostgreSQL image timezone files.
// It deliberately never reads host timezone data or invokes host zic.
package main

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

func main() {
	source := flag.String("source", "", "directory containing copied zoneinfo, Default and tzdata-copyright")
	output := flag.String("output", "", "output directory")
	flag.Parse()
	if *source == "" || *output == "" {
		panic("-source and -output are required")
	}
	zi := mustRead(filepath.Join(*source, "zoneinfo", "tzdata.zi"))
	names := make(map[string]bool)
	for _, line := range strings.Split(string(zi), "\n") {
		f := strings.Fields(line)
		if len(f) > 1 && f[0] == "Z" {
			names[f[1]] = true
		}
		if len(f) > 2 && f[0] == "L" {
			names[f[2]] = true
		}
	}
	sorted := make([]string, 0, len(names))
	for name := range names {
		sorted = append(sorted, name)
	}
	sort.Strings(sorted)
	var archive bytes.Buffer
	zw := zip.NewWriter(&archive)
	var manifest strings.Builder
	for _, name := range sorted {
		p := filepath.Join(*source, "zoneinfo", filepath.FromSlash(name))
		resolved, err := filepath.EvalSymlinks(p)
		must(err)
		root, err := filepath.Abs(filepath.Join(*source, "zoneinfo"))
		must(err)
		rel, err := filepath.Rel(root, resolved)
		must(err)
		if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			panic("alias escapes image tree: " + name)
		}
		b := mustRead(resolved)
		if !bytes.HasPrefix(b, []byte("TZif")) {
			panic("not TZif: " + name)
		}
		h := &zip.FileHeader{Name: name, Method: zip.Store}
		h.SetModTime(time.Date(1980, 1, 1, 0, 0, 0, 0, time.UTC))
		h.SetMode(0444)
		w, err := zw.CreateHeader(h)
		must(err)
		_, err = w.Write(b)
		must(err)
		fmt.Fprintf(&manifest, "%x  %s\n", sha256.Sum256(b), name)
	}
	must(zw.Close())
	must(os.MkdirAll(*output, 0755))
	must(os.WriteFile(filepath.Join(*output, "zones.zip"), archive.Bytes(), 0644))
	fmt.Fprintf(&manifest, "%x  zones.zip\n", sha256.Sum256(archive.Bytes()))
	for _, name := range []string{"Default", "tzdata-copyright"} {
		b := mustRead(filepath.Join(*source, name))
		must(os.WriteFile(filepath.Join(*output, name), b, 0644))
		fmt.Fprintf(&manifest, "%x  %s\n", sha256.Sum256(b), name)
	}
	must(os.WriteFile(filepath.Join(*output, "SHA256SUMS"), []byte(manifest.String()), 0644))
}
func must(err error) {
	if err != nil {
		panic(err)
	}
}
func mustRead(p string) []byte { b, err := os.ReadFile(p); must(err); return b }
