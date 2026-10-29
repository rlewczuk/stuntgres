package reference_test

import (
	"bytes"
	"context"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

// These expectations are specified independently of native replay. The initial
// pinned-oracle run must resolve the complete-invalid DROP and nested ONLY flags;
// no assertion here is learned from a native dump or substituted for exact bytes.
func TestReferencePartitionExclusionIncompleteControls(t *testing.T) {
	for _, state := range []string{"only_gap", "partial_covered", "partial_default_gap", "partial_attached_subtree", "complete_invalid_drop_gap", "empty_invalid_drop_gap", "nested_local_scope"} {
		partitionExclusionCase(t, state, func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
			run := partitionConstraintRun(t, p, b, prepared)
			nested := state == "nested_local_scope"
			attached := state == "partial_attached_subtree"
			complete := state == "complete_invalid_drop_gap"
			defaultGap := state == "partial_default_gap"
			covered := state == "partial_covered" || complete || nested || attached || defaultGap
			gap := state != "complete_invalid_drop_gap" && state != "empty_invalid_drop_gap"
			target, key, keys := "pie_root", "tenant", partitionExclusionKeys
			run(fixture.Exec("CREATE TABLE pie_root(id INT,tenant INET,site INET,during INT4RANGE) PARTITION BY RANGE(tenant)", 0))
			if complete {
				run(fixture.Exec("ALTER TABLE pie_root ADD CONSTRAINT pie_complete EXCLUDE USING btree(tenant WITH =,id WITH =)", 0))
			}
			if nested {
				run(fixture.Exec("CREATE TABLE pie_mid PARTITION OF pie_root DEFAULT PARTITION BY RANGE(site)", 0))
				target, key, keys = "pie_mid", "site", "site inet_ops WITH =,during WITH &&"
			}
			gapBound, coveredBound := "FOR VALUES FROM('10.0.0.0') TO('10.0.0.128')", "DEFAULT"
			if defaultGap {
				gapBound, coveredBound = "DEFAULT", "FOR VALUES FROM('10.0.0.128') TO('10.0.1.0')"
			}
			run(fixture.Exec("CREATE TABLE pie_gap PARTITION OF "+target+" "+gapBound, 0))
			// This independent provider is compatible with the admitted overlapping
			// ranges: it excludes repeated IDs, not overlapping booking ranges.
			run(fixture.Exec("ALTER TABLE pie_gap ADD CONSTRAINT pie_local EXCLUDE USING btree(id WITH =)", 0))
			run(fixture.Exec("INSERT INTO pie_root VALUES(1,'10.0.0.1','10.0.0.1','[1,5)'),(2,'10.0.0.1','10.0.0.1','[3,7)')", 2))
			var localOID, localIndexOID int64
			if err := b.DB.QueryRowContext(p.Ctx, "SELECT oid,conindid FROM pg_constraint WHERE conrelid='pie_gap'::regclass::OID AND conname='pie_local'").Scan(&localOID, &localIndexOID); err != nil {
				t.Fatal(err)
			}
			run(fixture.Exec("ALTER TABLE ONLY "+target+" ADD CONSTRAINT pie_ex EXCLUDE USING gist("+keys+")", 0))
			run(partitionIncompleteGraph(target, key, true, false, complete, false))
			if covered {
				// A covered partitioned member and its leaf are installed AFTER ONLY.
				// Replay must clone this captured subtree without filling pie_gap.
				if attached {
					run(fixture.Exec("CREATE TABLE pie_cov(id INT,tenant INET,site INET,during INT4RANGE,CONSTRAINT pie_cov_local EXCLUDE USING gist("+keys+")) PARTITION BY RANGE("+key+")", 0))
					run(fixture.Exec("CREATE TABLE pie_leaf PARTITION OF pie_cov DEFAULT", 0))
					middleIDs := partitionAdoptionIdentity(t, p, b.DB, "pie_cov")
					leafIDs := partitionAdoptionIdentity(t, p, b.DB, "pie_leaf")
					run(fixture.Exec("INSERT INTO pie_cov VALUES(3,'10.0.0.129','10.0.0.129','[1,5)')", 1))
					run(fixture.Exec("ALTER TABLE "+target+" ATTACH PARTITION pie_cov DEFAULT", 0))
					run(partitionAdoptionSame("pie_cov", middleIDs))
					run(partitionAdoptionSame("pie_leaf", leafIDs))
					run(fixture.Exec("ALTER INDEX pie_leaf_tenant_during_excl RENAME TO pie_saved", 0))
				} else {
					run(fixture.Exec("CREATE TABLE pie_cov PARTITION OF "+target+" "+coveredBound+" PARTITION BY RANGE("+key+")", 0))
					run(fixture.Exec("CREATE TABLE pie_leaf PARTITION OF pie_cov DEFAULT", 0))
					run(fixture.Exec("INSERT INTO pie_root VALUES(3,'10.0.0.129','10.0.0.129','[1,5)')", 1))
				}
			}
			if !gap {
				// DROP does not validate an already-invalid parent. This is the
				// source-derived complete-invalid/empty-invalid oracle probe.
				run(fixture.Exec("DROP TABLE pie_gap", 0))
			} else {
				run(fixture.Select("SELECT oid=$1::OID AS same_constraint,conindid=$2::OID AS same_index FROM pg_constraint WHERE conrelid='pie_gap'::regclass::OID AND conname='pie_local'", []string{"same_constraint", "same_index"}, []string{"BOOL", "BOOL"}, [][]any{{true, true}}, localOID, localIndexOID))
			}
			snapshot := func(x fixture.Executor, recorder *fixture.Recorder, pg bool) {
				t.Helper()
				apply := func(s fixture.Step) {
					t.Helper()
					s.Prepared = prepared
					fixture.RunStep(t, p.Ctx, x, recorder, s)
				}
				apply(partitionIncompleteGraph(target, key, gap, covered, complete, attached))
				apply(fixture.Select("SELECT relname FROM pg_class WHERE relname LIKE '__stuntgres_partition_replay_%' ORDER BY relname", []string{"relname"}, []string{"NAME"}, nil))
				rows := [][]any{}
				if gap {
					rows = append(rows, []any{int64(1), "10.0.0.1", "10.0.0.1", "[1,5)"}, []any{int64(2), "10.0.0.1", "10.0.0.1", "[3,7)"})
				}
				if covered {
					rows = append(rows, []any{int64(3), "10.0.0.129", "10.0.0.129", "[1,5)"})
				}
				s := fixture.Select("SELECT id,tenant,site,during FROM pie_root ORDER BY id", []string{"id", "tenant", "site", "during"}, []string{"INT4", "INET", "INET", "INT4RANGE"}, rows)
				s.CheckMetadata = referenceRangeMetadata(pg, []string{"id", "tenant", "site", "during"}, []string{"INT4", "INET", "INET", "INT4RANGE"}, []bool{true, true, true, true})
				apply(s)
			}
			future := func(x fixture.Executor, recorder *fixture.Recorder) {
				apply := func(s fixture.Step) { s.Prepared = prepared; fixture.RunStep(t, p.Ctx, x, recorder, s) }
				if gap {
					apply(fixture.Exec("INSERT INTO pie_root VALUES(10,'10.0.0.1','10.0.0.1','[2,6)')", 1))
					apply(fixture.ErrorDiagnostic("INSERT INTO pie_root VALUES(1,'10.0.0.1','10.0.0.1','[20,25)')", fixture.ExclusionReferenceConflict("pie_gap", "pie_local", "id", "1", "1")))
					apply(fixture.Select("SELECT id FROM pie_gap ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}, {int64(10)}}))
				}
				if covered {
					name := "pie_leaf_" + key + "_during_excl"
					if attached {
						// The original leaf is now the sole adopted provider.
						name = "pie_saved"
					}
					apply(fixture.ErrorDiagnostic("INSERT INTO pie_root VALUES(11,'10.0.0.129','10.0.0.129','[3,7)')", fixture.ExclusionReferenceConflict("pie_leaf", name, key+", during", "10.0.0.129, [3,7)", "10.0.0.129, [1,5)")))
					apply(fixture.Exec("INSERT INTO pie_root VALUES(12,'10.0.0.130','10.0.0.130','[3,7)')", 1))
					apply(fixture.Select("SELECT id FROM pie_leaf ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(3)}, {int64(12)}}))
				} else {
					// A newly added child of an invalid ONLY root still clones the
					// provider, including after restoring an empty-invalid root.
					apply(fixture.Exec("CREATE TABLE pie_future PARTITION OF "+target+" DEFAULT", 0))
					apply(fixture.Exec("INSERT INTO pie_root VALUES(20,'10.0.0.129','10.0.0.129','[1,5)')", 1))
					apply(fixture.ErrorDiagnostic("INSERT INTO pie_root VALUES(21,'10.0.0.129','10.0.0.129','[3,7)')", fixture.ExclusionReferenceConflict("pie_future", "pie_future_"+key+"_during_excl", key+", during", "10.0.0.129, [3,7)", "10.0.0.129, [1,5)")))
				}
			}
			snapshot(b.DB, b.Recorder, b.PG)
			if b.PG {
				future(b.DB, b.Recorder)
				return
			}
			var dump bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal("restore admitted incomplete exclusion topology", err, dump.String())
			}
			snapshot(b.DB, b.Recorder, false)
			var redump bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &redump); err != nil || !bytes.Equal(dump.Bytes(), redump.Bytes()) {
				t.Fatal("incomplete topology canonical redump changed", err, "before:", dump.String(), "after:", redump.String())
			}
			if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal("repeated replacement", err)
			}
			snapshot(b.DB, b.Recorder, false)
			redump.Reset()
			if err := stuntgres.Dump(p.Ctx, p.Native, &redump); err != nil || !bytes.Equal(dump.Bytes(), redump.Bytes()) {
				t.Fatal("repeated incomplete canonical bytes", err, dump.String(), redump.String())
			}
			if p.Postgres != nil {
				oracle, err := p.Postgres.Conn(p.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer oracle.Close()
				defer func() {
					ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
					defer cancel()
					for _, q := range []string{"RESET ROLE", "RESET SESSION AUTHORIZATION", "RESET search_path", "DROP SCHEMA public CASCADE", "CREATE SCHEMA public"} {
						if _, err := oracle.ExecContext(ctx, q); err != nil {
							t.Error(err)
						}
					}
				}()
				if _, err := oracle.ExecContext(p.Ctx, dump.String()); err != nil {
					t.Fatal("actual emitted incomplete exclusion SQL replay", err, dump.String())
				}
				snapshot(oracle, p.PostgresRecord, true)
				future(oracle, p.PostgresRecord)
			}
			future(b.DB, b.Recorder)
		})
	}
}

func partitionIncompleteGraph(target, key string, gap, covered, complete, attached bool) fixture.Step {
	// parent is obtained by joining the actual conparentid, not by decoding OIDs
	// or assuming that names imply inheritance. The complete bag also proves the
	// absence of synthesized gap constraints and of replay scratch providers.
	row := func(table, name string, parent any, local bool, kind string, valid bool, keys string) []any {
		count := int64(0)
		if !local {
			count = 1
		}
		return []any{table, name, parent, local, count, local, false, false, name, kind, valid, true, true, keys, local}
	}
	keys := "{2,4}"
	if key == "site" {
		keys = "{3,4}"
	}
	rows := [][]any{}
	if covered {
		if attached {
			adopted := row("pie_cov", "pie_cov_local", "pie_ex", false, "I", true, keys)
			adopted[5] = true
			rows = append(rows, adopted)
		} else {
			rows = append(rows, row("pie_cov", "pie_cov_"+key+"_during_excl", "pie_ex", false, "I", true, keys))
		}
		if complete {
			rows = append(rows, row("pie_cov", "pie_cov_tenant_id_excl", "pie_complete", false, "I", true, "{2,1}"))
		}
	}
	if gap {
		rows = append(rows, row("pie_gap", "pie_local", nil, true, "i", true, "{1}"))
		if complete {
			rows = append(rows, row("pie_gap", "pie_gap_tenant_id_excl", "pie_complete", false, "i", true, "{2,1}"))
			// Sort by conname, independently of provider creation order.
			rows[len(rows)-2], rows[len(rows)-1] = rows[len(rows)-1], rows[len(rows)-2]
		}
	}
	if covered {
		if attached {
			rows = append(rows, row("pie_leaf", "pie_saved", "pie_cov_local", false, "i", true, keys))
		} else {
			rows = append(rows, row("pie_leaf", "pie_leaf_"+key+"_during_excl", "pie_cov_"+key+"_during_excl", false, "i", true, keys))
		}
		if complete {
			rows = append(rows, row("pie_leaf", "pie_leaf_tenant_id_excl", "pie_cov_tenant_id_excl", false, "i", true, "{2,1}"))
		}
	}
	if complete {
		rows = append(rows, row(target, "pie_complete", nil, true, "I", true, "{2,1}"))
	}
	rows = append(rows, row(target, "pie_ex", nil, true, "I", false, keys))
	return fixture.Select("SELECT t.relname,c.conname,p.conname AS parent,c.conislocal,c.coninhcount,c.connoinherit,c.condeferrable,c.condeferred,i.relname AS backing,i.relkind,x.indisvalid,x.indisready,x.indislive,c.conkey::TEXT AS keys,c.conparentid=0 AS independent FROM pg_constraint c JOIN pg_class t ON t.oid=c.conrelid JOIN pg_class i ON i.oid=c.conindid JOIN pg_index x ON x.indexrelid=i.oid LEFT JOIN pg_constraint p ON p.oid=c.conparentid WHERE t.relname IN ('pie_root','pie_mid','pie_gap','pie_cov','pie_leaf') AND c.contype='x' ORDER BY t.relname,c.conname",
		[]string{"relname", "conname", "parent", "conislocal", "coninhcount", "connoinherit", "condeferrable", "condeferred", "backing", "relkind", "indisvalid", "indisready", "indislive", "keys", "independent"},
		[]string{"NAME", "NAME", "NAME", "BOOL", "INT2", "BOOL", "BOOL", "BOOL", "NAME", "CHAR", "BOOL", "BOOL", "BOOL", "TEXT", "BOOL"}, rows)
}
