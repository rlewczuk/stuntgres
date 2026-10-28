package enginetests_test

import (
	"testing"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/sqlstate"
)

func TestInheritanceColumnStorageMerge(t *testing.T) {
	for _, storage := range []string{"p", "e", "x", "m"} {
		for _, first := range []string{"", "p", "l"} {
			for _, second := range []string{"", "p", "l"} {
				old := catalog.ColumnStorage{Storage: storage, Compression: first}
				next := catalog.ColumnStorage{Storage: storage, Compression: second}
				got, err := analyze.MergeInheritedColumnStorage("t", old, next)
				if first != "" && second != "" && first != second {
					d, ok := err.(*sqlstate.Error)
					if !ok || d.Code != "42804" || d.Message != `column "t" has a compression method conflict` {
						t.Fatalf("explicit conflict %v %v: %v", old, next, err)
					}
					want := "pglz versus lz4"
					if first == "l" { want = "lz4 versus pglz" }
					if d.Detail != want { t.Fatalf("detail %q want %q", d.Detail, want) }
					continue
				}
				want := old
				if first == "" { want.Compression = second }
				if err != nil || got != want { t.Fatalf("merge %v %v: %v %v want %v", old, next, got, err, want) }
			}
		}
	}
	for _, reverse := range []bool{false, true} {
		old, next := catalog.ColumnStorage{Storage: "e", Compression: "p"}, catalog.ColumnStorage{Storage: "x", Compression: "l"}
		want := "EXTERNAL versus EXTENDED"
		if reverse { old, next = next, old; want = "EXTENDED versus EXTERNAL" }
		_, err := analyze.MergeInheritedColumnStorage("t", old, next)
		d, ok := err.(*sqlstate.Error)
		if !ok || d.Code != "42804" || d.Message != `inherited column "t" has a storage parameter conflict` || d.Detail != want {
			t.Fatalf("storage before compression: %v", err)
		}
	}
}

func TestInheritanceColumnStorageLocalMerge(t *testing.T) {
	for _, storage := range []string{"p", "e", "x", "m"} {
		for _, selected := range []string{"", "p", "e", "x", "m"} {
			for _, inherited := range []string{"", "p", "l"} {
				for _, explicit := range []string{"", "p", "l"} {
					old := catalog.ColumnStorage{Storage: storage, Compression: inherited}
					local := catalog.ColumnStorage{Storage: selected, Compression: explicit}
					before := local
					got, err := analyze.MergeLocalColumnStorage("t", old, local)
					if local != before {
						t.Fatal("merge mutated the local declaration")
					}
					conflict := selected != "" && selected != storage
					if conflict || inherited != "" && explicit != "" && inherited != explicit {
						d, ok := err.(*sqlstate.Error)
						if !ok {
							t.Fatalf("missing conflict for %v %v: %v", old, local, err)
						}
						want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42804"}
						if conflict {
							want.Message = `column "t" has a storage parameter conflict`
							names := map[string]string{"p": "PLAIN", "e": "EXTERNAL", "x": "EXTENDED", "m": "MAIN"}
							want.Detail = names[storage] + " versus " + names[selected]
						} else {
							want.Message = `column "t" has a compression method conflict`
							names := map[string]string{"p": "pglz", "l": "lz4"}
							want.Detail = names[inherited] + " versus " + names[explicit]
						}
						if *d != want {
							t.Fatalf("diagnostic %+v want %+v", *d, want)
						}
						continue
					}
					want := old
					if inherited == "" {
						want.Compression = explicit
					}
					if err != nil || got != want {
						t.Fatalf("merge %v %v: %v %v want %v", old, local, got, err, want)
					}
				}
			}
		}
	}
}
