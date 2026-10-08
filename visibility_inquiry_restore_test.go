package stuntgres_test

import (
	"bytes"
	"context"
	"strings"
	"stuntgres"
	"testing"
)

// A plain table dump still contains the bootstrapped no-principal metadata in
// its staged catalog. Neither visibility expressions nor owner remapping mocks
// are needed to reproduce replacement validation of those descriptors.
func TestVisibilityInquiryNoPrincipalRestore(t *testing.T) {
	ctx := context.Background()
	source := openLocal(t, stuntgres.NewDriver(), t.Name()+"-source")
	execSQL(t, source, "CREATE TABLE restored(id INT)")
	execSQL(t, source, "INSERT INTO restored VALUES(7)")
	var dump bytes.Buffer
	if err := stuntgres.Dump(ctx, source, &dump); err != nil {
		t.Fatal(err)
	}
	for _, target := range []struct {
		name string
		same bool
	}{{"same-engine", true}, {"independent-driver", false}} {
		t.Run(target.name, func(t *testing.T) {
			db := source
			if !target.same {
				db = openLocal(t, stuntgres.NewDriver(), t.Name())
			}
			if err := stuntgres.Reset(ctx, db); err != nil {
				t.Fatal(err)
			}
			// Empty scripts also replace a fully bootstrapped staged catalog.
			if err := stuntgres.Restore(ctx, db, strings.NewReader("")); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(ctx, db, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
			wantTuples(t, db, "SELECT id FROM restored", [][]any{{int64(7)}})
			wantTuples(t, db, "SELECT pg_type_is_visible(23),pg_collation_is_visible(950),pg_conversion_is_visible(4402)", [][]any{{true, true, true}})
			if err := stuntgres.Restore(ctx, db, strings.NewReader("CREATE TABLE broken(id INT); INSERT INTO broken VALUES('bad');")); err == nil {
				t.Fatal("invalid restore published")
			}
			wantTuples(t, db, "SELECT id FROM restored", [][]any{{int64(7)}})
		})
	}
}
