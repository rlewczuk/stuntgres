package reference_test

import (
	"database/sql"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

func partitionInfoScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c := fixture.SequenceCoreConn(t, p.Ctx, b.DB)
			body(t, p, b, c)
		})
	}
}

func partitionInfoExec(t *testing.T, p *fixture.Pair, b fixture.Backend, x fixture.Executor, queries ...string) {
	t.Helper()
	for _, q := range queries {
		fixture.RunStep(t, p.Ctx, x, b.Recorder, fixture.Exec(q, 0))
	}
}

func partitionInfoSeed(t *testing.T, p *fixture.Pair, b fixture.Backend, x fixture.Executor) {
	t.Helper()
	partitionInfoExec(t, p, b, x,
		"CREATE TABLE pinfo_r(k INT,payload TEXT,CONSTRAINT pinfo_r_key UNIQUE(k,payload)) PARTITION BY RANGE(k)",
		"CREATE TABLE pinfo_m PARTITION OF pinfo_r FOR VALUES FROM(0) TO(10) PARTITION BY RANGE(k)",
		"CREATE TABLE pinfo_l PARTITION OF pinfo_m FOR VALUES FROM(0) TO(5)",
		"CREATE TABLE pinfo_s PARTITION OF pinfo_r FOR VALUES FROM(10) TO(20)",
		"CREATE TABLE pinfo_e PARTITION OF pinfo_r FOR VALUES FROM(20) TO(30) PARTITION BY RANGE(k)")
	fixture.RunStep(t, p.Ctx, x, b.Recorder, fixture.Exec("INSERT INTO pinfo_r VALUES(1,'one'),(11,'eleven')", 2))
}

func partitionInfoTreeRows(index bool) [][]any {
	r, m, l, s, e := "pinfo_r", "pinfo_m", "pinfo_l", "pinfo_s", "pinfo_e"
	if index {
		r, m, l, s, e = "pinfo_r_key", "pinfo_m_k_payload_key", "pinfo_l_k_payload_key", "pinfo_s_k_payload_key", "pinfo_e_k_payload_key"
	}
	return [][]any{{r, nil, false, int64(0)}, {m, r, false, int64(1)}, {l, m, true, int64(2)}, {s, r, true, int64(1)}, {e, r, false, int64(1)}}
}

func partitionInfoTree(q string, rows [][]any, args ...any) fixture.Step {
	s := fixture.Select(q, []string{"relid", "parentrelid", "isleaf", "level"}, []string{"REGCLASS", "REGCLASS", "BOOL", "INT4"}, rows, args...)
	s.Bag = true
	return s
}

func partitionInfoMetadata(pg, viewColumn bool) func(*testing.T, *sql.Rows) {
	return func(t *testing.T, rows *sql.Rows) {
		t.Helper()
		cols, err := rows.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		for _, c := range cols {
			var scan reflect.Type
			switch c.DatabaseTypeName() {
			case "REGCLASS":
				scan = reflect.TypeFor[string]()
				if pg {
					scan = reflect.TypeFor[any]()
				}
			case "BOOL":
				scan = reflect.TypeFor[bool]()
			case "INT4":
				scan = reflect.TypeFor[int64]()
				if pg {
					scan = reflect.TypeFor[int32]()
				}
			default:
				t.Fatalf("unexpected partition inquiry type %s", c.DatabaseTypeName())
			}
			n, nk := c.Nullable()
			l, lk := c.Length()
			precision, scale, dk := c.DecimalSize()
			// Native view columns have known nullable descriptors, unlike
			// expression/source results; pq exposes neither nullability.
			wantNullable := viewColumn && !pg
			if c.ScanType() != scan || n != wantNullable || nk != wantNullable || l != 0 || lk || precision != 0 || scale != 0 || dk {
				t.Fatalf("metadata %s: scan=%v want=%v nullable=%t/%t length=%d/%t decimal=%d,%d/%t", c.Name(), c.ScanType(), scan, n, nk, l, lk, precision, scale, dk)
			}
		}
	}
}

func partitionInfoRun(t *testing.T, p *fixture.Pair, b fixture.Backend, x fixture.Executor, s fixture.Step) {
	t.Helper()
	if s.CheckMetadata == nil {
		s.CheckMetadata = partitionInfoMetadata(b.PG, false)
	}
	s.CheckScan = func(t *testing.T, rows *sql.Rows, rowIndex int) {
		cols, err := rows.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		cells := make([]any, len(cols))
		dest := make([]any, len(cols))
		for i := range cells {
			dest[i] = &cells[i]
		}
		if err := rows.Scan(dest...); err != nil {
			t.Fatal(err)
		}
		for i, col := range cols {
			if col.DatabaseTypeName() != "REGCLASS" || cells[i] == nil {
				continue
			}
			if b.PG {
				if _, ok := cells[i].([]byte); !ok {
					t.Fatalf("raw PG REGCLASS %T, want []byte", cells[i])
				}
			} else if _, ok := cells[i].(string); !ok {
				t.Fatalf("raw native REGCLASS %T, want string", cells[i])
			}
		}
	}
	fixture.RunStep(t, p.Ctx, x, b.Recorder, s)
}

func partitionInfoAncestors(t *testing.T, p *fixture.Pair, b fixture.Backend, x fixture.Executor, input string, names ...string) {
	t.Helper()
	var want [][]any
	for _, name := range names {
		want = append(want, []any{name})
	}
	s := fixture.Select("SELECT * FROM pg_partition_ancestors("+input+")", []string{"relid"}, []string{"REGCLASS"}, want)
	s.Bag = true
	partitionInfoRun(t, p, b, x, s)
}

func partitionInfoRoot(t *testing.T, p *fixture.Pair, b fixture.Backend, x fixture.Executor, input string, root any) {
	t.Helper()
	partitionInfoRun(t, p, b, x, fixture.Select("SELECT pg_partition_root("+input+")", []string{"pg_partition_root"}, []string{"REGCLASS"}, [][]any{{root}}))
}

func partitionInfoEmpty(t *testing.T, p *fixture.Pair, b fixture.Backend, x fixture.Executor, input string) {
	t.Helper()
	partitionInfoRun(t, p, b, x, partitionInfoTree("SELECT * FROM pg_partition_tree("+input+")", nil))
	partitionInfoAncestors(t, p, b, x, input)
	partitionInfoRoot(t, p, b, x, input, nil)
}
