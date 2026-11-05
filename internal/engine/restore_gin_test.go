package engine

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"maps"
	"strings"
	"testing"

	"stuntgres/internal/engine/control"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/parser"
)

func ginRestoreIdentityFixture(t *testing.T, incumbents int, routed bool) (*restoreRun, *session.Operation, *parser.Insert) {
	t.Helper()
	source := New()
	e, stageErr := source.RestoreStage()
	if stageErr != nil {
		t.Fatal(stageErr)
	}
	r := &restoreRun{stage: e}
	host := &restoreHost{target: source, run: r}
	op, done, err := host.Start(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(done)
	setup := func(sql string) {
		t.Helper()
		if _, err := e.Execute(op, statement(t, sql), 0, nil); err != nil {
			t.Fatal(sql, err)
		}
	}
	replayTable := "gin_restore_rows"
	if routed {
		setup("CREATE TABLE public.gin_restore_parent(id integer,a integer[]) PARTITION BY RANGE(id)")
		setup("CREATE TABLE public.gin_restore_rows PARTITION OF public.gin_restore_parent FOR VALUES FROM(0) TO(10)")
		replayTable = "gin_restore_parent"
	} else {
		setup("CREATE TABLE public.gin_restore_rows(id integer,a integer[])")
	}
	if incumbents != 0 {
		setup("INSERT INTO public." + replayTable + " VALUES " + strings.Repeat("(1,'{2}'),", incumbents-1) + "(1,'{2}')")
	}
	core.OperationReplay(op).InsertAuthority = true
	insertion := statement(t, "INSERT INTO public."+replayTable+" VALUES(1,'{2}')").(*parser.Insert)
	r.ginRowPairs = map[*parser.Insert]index.GinDumpRow{insertion: {Schema: "public", Table: "gin_restore_rows", Ordinal: 1, ReplaySchema: "public", ReplayTable: replayTable}}
	return r, op, insertion
}

func ginRestoreIdentityExecute(t *testing.T, r *restoreRun, op *session.Operation, insertion *parser.Insert) core.RestoreInsertCapture {
	t.Helper()
	r.cur = restoreStatement{}
	if err := prepareRestoreGinRow(r, op, insertion); err != nil {
		t.Fatal(err)
	}
	outcome, err := r.execute(op, insertion)
	if err != nil {
		t.Fatal(err)
	}
	if core.OperationReplayOrZero(op).GinInsert != nil {
		t.Fatal("execution retained the producer capture")
	}
	capture := r.cur.ginInsert
	row, version, accepted := capture.Identity()
	actual := capture.Store.Versions[len(capture.Store.Versions)-1]
	if !accepted || row != actual.Row || version != actual.Version {
		t.Fatal("receipt is not the actual accepted destination occurrence", row, version)
	}
	if err := afterRestoreGinRow(r, op, insertion, outcome); err != nil {
		t.Fatal(err)
	}
	return capture
}

func TestGINMaintenanceRestoreIdentityCapture(t *testing.T) {
	for _, routed := range []bool{false, true} {
		t.Run(fmt.Sprintf("duplicate-values/routed=%v", routed), func(t *testing.T) {
			r, op, insertion := ginRestoreIdentityFixture(t, 0, routed)
			seen := make(map[index.RowIdentity]bool)
			for ordinal := 1; ordinal <= 3; ordinal++ {
				record := r.ginRowPairs[insertion]
				record.Ordinal = ordinal
				r.ginRowPairs[insertion] = record
				capture := ginRestoreIdentityExecute(t, r, op, insertion)
				row, version, _ := capture.Identity()
				identity := index.RowIdentity{Row: row, Version: version}
				if seen[identity] || r.ginIdentities[[2]string{"public", "gin_restore_rows"}][ordinal] != identity {
					t.Fatal("equal SQL rows lost distinct produced occurrence identities")
				}
				seen[identity] = true
			}
			// Native Dump carries pending-empty indexes too. Replay the real
			// duplicate/routed row stream through the complete control protocol.
			core.OperationReplay(op).InsertAuthority = false
			if _, err := r.stage.Execute(op, statement(t, "CREATE INDEX gin_restore_rows_i ON public.gin_restore_rows USING gin(a)"), 0, nil); err != nil {
				t.Fatal(err)
			}
			var dump bytes.Buffer
			if err := Dump(context.Background(), r.stage, &dump); err != nil {
				t.Fatal(err)
			}
			copy := New()
			if err := Restore(context.Background(), copy, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
			target := ginRestoreRelation(copy.CatalogRead(), "public", "gin_restore_rows")
			rows := copy.Storage.Relations[target.Address()].Versions
			seen = make(map[index.RowIdentity]bool)
			for _, row := range rows {
				identity := index.RowIdentity{Row: row.Row, Version: row.Version}
				if seen[identity] {
					t.Fatal("restored duplicate occurrences share identity")
				}
				seen[identity] = true
			}
			if len(seen) != 3 {
				t.Fatal("restore lost equal occurrences", len(seen))
			}
		})
	}
	t.Run("domain-replay-mixed", func(t *testing.T) {
		source := New()
		for _, sql := range []string{
			"CREATE DOMAIN gin_restore_key AS integer[] CHECK(VALUE IS NULL OR cardinality(VALUE)>0)",
			"CREATE TABLE gin_restore_domains(id integer,a gin_restore_key)",
			"INSERT INTO gin_restore_domains VALUES(0,'{1}')",
			"CREATE INDEX gin_restore_domain_i ON gin_restore_domains USING gin(a)",
			"INSERT INTO gin_restore_domains VALUES(1,NULL),(1,NULL)",
		} {
			run(t, source, sql)
		}
		var dump bytes.Buffer
		if err := Dump(context.Background(), source, &dump); err != nil {
			t.Fatal(err)
		}
		if strings.Count(dump.String(), "-- stuntgres-domain-null-v1 ") != 2 {
			t.Fatal("fixture did not reach actual domain replay")
		}
		copy := New()
		if err := Restore(context.Background(), copy, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		idx := ginRestoreRelation(copy.CatalogRead(), "public", "gin_restore_domain_i")
		parent := ginRestoreRelation(copy.CatalogRead(), "public", "gin_restore_domains")
		root := copy.Storage.IndexRoots[idx.Address()]
		rows := copy.Storage.Relations[parent.Address()].Versions
		if len(rows) != 3 || len(root.Entries) != 3 || storage.GinPendingIncidences(root.Gin) != 2 || len(root.Gin.Columns[0].Tokens) != 1 {
			t.Fatal("domain replay lost actual occurrences or mixed placement")
		}
		seen := make(map[index.RowIdentity]bool)
		for _, row := range rows {
			identity := index.RowIdentity{Row: row.Row, Version: row.Version}
			if seen[identity] || row.Values[1].Type() != parent.Columns[1].Usage.Type {
				t.Fatal("domain replay retained foreign or duplicate row/type identity")
			}
			seen[identity] = true
		}
	})
	var baselineAllocations float64
	for _, incumbents := range []int{32, 4096} {
		t.Run(fmt.Sprintf("constant-hook-allocation/%d", incumbents), func(t *testing.T) {
			r, op, insertion := ginRestoreIdentityFixture(t, incumbents, false)
			capture := ginRestoreIdentityExecute(t, r, op, insertion)
			key := [2]string{"public", "gin_restore_rows"}
			var hookError error
			allocations := testing.AllocsPerRun(100, func() {
				delete(r.ginIdentities[key], 1)
				if err := prepareRestoreGinRow(r, op, insertion); err != nil {
					hookError = err
					return
				}
				// Reuse a genuine producer receipt while measuring only its two
				// consumers, excluding SQL binding/publication and output-map growth.
				r.cur.ginInsert = capture
				if err := afterRestoreGinRow(r, op, insertion, control.Outcome{}); err != nil {
					hookError = err
				}
			})
			if incumbents == 32 {
				baselineAllocations = allocations
			}
			// Allow fixed compiler/iterator overhead, not allocation growth
			// proportional to 128 times as many incumbent row versions.
			if hookError != nil || allocations > baselineAllocations+1 {
				t.Fatal("identity consumers allocate incumbent-sized scratch", incumbents, baselineAllocations, allocations, hookError)
			}
		})
	}
}

func TestGINMaintenanceRestoreIdentityAtomic(t *testing.T) {
	target := New()
	for _, source := range []string{
		"CREATE TABLE retained_gin(id integer,a integer[])",
		"CREATE INDEX retained_gin_i ON retained_gin USING gin(a)",
		"INSERT INTO retained_gin VALUES(77,'{7}')",
	} {
		run(t, target, source)
	}
	before := target.CatalogRead().State()
	stores, roots := maps.Clone(target.Storage.Relations), maps.Clone(target.Storage.IndexRoots)
	rowFrontier, versionFrontier := target.Storage.RowFrontier(), target.Storage.VersionFrontier()
	indexRecord, err := index.EncodeGinIndex(index.GinDumpIndex{Schema: "public", Name: "gin_restore_rows_i", ParentSchema: "public", Parent: "gin_restore_rows", Pending: []int{1}})
	if err != nil {
		t.Fatal(err)
	}
	for _, failure := range []string{"zero", "multiple", "wrong-target", "cancel"} {
		t.Run(failure, func(t *testing.T) {
			record := index.GinDumpRow{Schema: "public", Table: "gin_restore_rows", Ordinal: 1, ReplaySchema: "public", ReplayTable: "gin_restore_rows"}
			trigger := ""
			switch failure {
			case "zero":
				trigger = "CREATE FUNCTION public.gin_skip() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN RETURN NULL; END$$; CREATE TRIGGER gin_skip BEFORE INSERT ON public.gin_restore_rows FOR EACH ROW EXECUTE FUNCTION public.gin_skip();"
			case "multiple":
				trigger = "CREATE FUNCTION public.gin_expand() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN INSERT INTO public.gin_restore_other VALUES(NEW.id,NEW.a); RETURN NEW; END$$; CREATE TRIGGER gin_expand AFTER INSERT ON public.gin_restore_rows FOR EACH ROW EXECUTE FUNCTION public.gin_expand();"
			case "wrong-target":
				record.Table = "gin_restore_other"
			}
			rowRecord, err := index.EncodeGinRow(record)
			if err != nil {
				t.Fatal(err)
			}
			script := "CREATE TABLE public.gin_restore_rows(id integer,a integer[]); CREATE TABLE public.gin_restore_other(id integer,a integer[]);" + trigger + "\n" + rowRecord + "INSERT INTO public.gin_restore_rows VALUES(1,'{2}'); CREATE INDEX gin_restore_rows_i ON public.gin_restore_rows USING gin(a);\n" + indexRecord
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if failure == "cancel" {
				cancel()
			}
			err = Restore(ctx, target, strings.NewReader(script))
			if failure == "cancel" {
				if !errors.Is(err, context.Canceled) {
					t.Fatal("lost restore cancellation", err)
				}
			} else if err == nil || !strings.Contains(err.Error(), "native GIN row did not produce one destination identity") {
				t.Fatal("actual producer did not reject row identity mismatch", err)
			}
			if target.CatalogRead().State() != before || !maps.Equal(stores, target.Storage.Relations) || !maps.Equal(roots, target.Storage.IndexRoots) || target.Storage.RowFrontier() != rowFrontier || target.Storage.VersionFrontier() != versionFrontier {
				t.Fatal("failed identity capture changed destination state")
			}
		})
	}
	for _, failure := range []string{"wrong-store", "wrong-ref", "foreign-domain", "wrong-epoch", "update", "failed-insert", "stale-receipt", "wrong-statement"} {
		t.Run("producer-protocol/"+failure, func(t *testing.T) {
			r, op, insertion := ginRestoreIdentityFixture(t, 1, false)
			if err := prepareRestoreGinRow(r, op, insertion); err != nil {
				t.Fatal(err)
			}
			executed := parser.Statement(insertion)
			switch failure {
			case "wrong-store":
				r.cur.ginInsert.Store = &storage.Relation{}
			case "wrong-ref":
				r.cur.ginInsert.Target.Address.ObjectID++
			case "foreign-domain":
				r.cur.ginInsert.Target = New().Catalog.Domain.Ref(r.cur.ginInsert.Target.Epoch, r.cur.ginInsert.Target.Address)
			case "wrong-epoch":
				r.cur.ginInsert.Target.Epoch++
			case "update":
				executed = statement(t, "UPDATE public.gin_restore_rows SET a='{3}' WHERE id=1")
			case "failed-insert":
				executed = statement(t, "INSERT INTO public.gin_restore_rows VALUES(1,true)")
			}
			outcome, executeErr := r.execute(op, executed)
			if failure == "failed-insert" {
				if executeErr == nil {
					t.Fatal("invalid typed insertion succeeded")
				}
			} else if executeErr != nil {
				t.Fatal("real producer was not reached", executeErr)
			}
			if failure == "stale-receipt" {
				if err := afterRestoreGinRow(r, op, insertion, outcome); err != nil {
					t.Fatal(err)
				}
				record := r.ginRowPairs[insertion]
				record.Ordinal++
				r.ginRowPairs[insertion] = record
			}
			if failure == "wrong-statement" {
				r.cur.ginInsert.Statement = statement(t, "INSERT INTO public.gin_restore_rows VALUES(1,'{2}')").(*parser.Insert)
			}
			if err := afterRestoreGinRow(r, op, insertion, outcome); err == nil {
				t.Fatal("invalid/stale producer receipt acquired ordinal authority")
			}
			if _, _, accepted := r.cur.ginInsert.Identity(); accepted || core.OperationReplayOrZero(op).GinInsert != nil {
				t.Fatal("failed consumption retained a reusable receipt")
			}
			if target.CatalogRead().State() != before || !maps.Equal(stores, target.Storage.Relations) || !maps.Equal(roots, target.Storage.IndexRoots) {
				t.Fatal("failed isolated producer protocol changed public destination")
			}
		})
	}
}
