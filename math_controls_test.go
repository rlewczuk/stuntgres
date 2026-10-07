package stuntgres_test

import (
	"bytes"
	"context"
	"errors"
	"math"
	"strings"
	"testing"

	"stuntgres"
)

func TestMathConsumers(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), "math-consumers")
	execSQL(t, db, "CREATE TABLE m(id INT,n NUMERIC,d NUMERIC)")
	execSQL(t, db, "INSERT INTO m VALUES(1,1.25,4),(2,1.24,0),(3,-1.25,4)")
	wantTuples(t, db, "SELECT round(trim_scale(n),1) FROM m ORDER BY id", [][]any{{"1.3"}, {"1.2"}, {"-1.3"}})
	wantTuples(t, db, "SELECT sign(count(*)),round(count(*),1) FROM m", [][]any{{float64(1), "3.0"}})
	wantTuples(t, db, "SELECT round(CASE WHEN true THEN n ELSE 0.0 END,1) FROM m ORDER BY id", [][]any{{"1.3"}, {"1.2"}, {"-1.3"}})
	_, err := db.Exec("UPDATE m SET n=mod(n,d)")
	var diagnostic *stuntgres.Error
	if !errors.As(err, &diagnostic) || diagnostic.Code != "22012" {
		t.Fatal(err)
	}
	wantTuples(t, db, "SELECT n FROM m ORDER BY id", [][]any{{"1.25"}, {"1.24"}, {"-1.25"}})
	result, err := db.Exec("UPDATE m SET n=round(n,1)")
	if err != nil {
		t.Fatal(err)
	}
	if n, err := result.RowsAffected(); err != nil || n != 3 {
		t.Fatal(n, err)
	}
	execSQL(t, db, "CREATE TABLE copied(n NUMERIC)")
	execSQL(t, db, "INSERT INTO copied SELECT trim_scale(n) FROM m")
	wantTuples(t, db, "SELECT n FROM copied ORDER BY n", [][]any{{"-1.3"}, {"1.2"}, {"1.3"}})
	execSQL(t, db, "CREATE TABLE nullable(n NUMERIC,i INT)")
	execSQL(t, db, "INSERT INTO nullable VALUES(NULL,9)")
	_, err = db.Exec("SELECT mod(n,mod(i,0)) FROM nullable")
	if !errors.As(err, &diagnostic) || diagnostic.Code != "22012" {
		t.Fatal("demanded runtime sibling", err)
	}
	wantTuples(t, db, "SELECT mod(n,0) FROM nullable", [][]any{{nil}})
}

func TestMathControls(t *testing.T) {
	ctx := context.Background()
	db := openLocal(t, stuntgres.NewDriver(), "math-controls")
	execSQL(t, db, "CREATE TABLE source(id INT,n NUMERIC,d DOUBLE PRECISION)")
	execSQL(t, db, "INSERT INTO source VALUES(1,1.23,'-0.5'),(2,8.4100,'Infinity'),(3,NULL,NULL)")
	execSQL(t, db, "CREATE TABLE materialized(id INT,padded NUMERIC,trimmed NUMERIC,remainder NUMERIC,sgn NUMERIC,d DOUBLE PRECISION)")
	execSQL(t, db, "INSERT INTO materialized SELECT id,round(n,4),trim_scale(n),mod(n,4.0),sign(n),trunc(d) FROM source")
	execSQL(t, db, "CREATE VIEW math_view AS SELECT id,round(n,1) AS rounded,trim_scale(n) AS trimmed,mod(n,4.0) AS remainder FROM source")
	execSQL(t, db, "CREATE VIEW null_math_view AS SELECT round(NULL,mod(id,0)) AS result FROM source")
	wantTuples(t, db, "SELECT rounded,trimmed,remainder FROM math_view ORDER BY id", [][]any{{"1.2", "1.23", "1.23"}, {"8.4", "8.41", "0.4100"}, {nil, nil, nil}})
	held, err := db.Query("SELECT padded,trimmed FROM materialized ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	var dump bytes.Buffer
	if err := stuntgres.Dump(ctx, db, &dump); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Reset(ctx, db); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(ctx, db, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	wantTuples(t, db, "SELECT padded,trimmed,remainder,sgn FROM materialized ORDER BY id", [][]any{{"1.2300", "1.23", "1.23", "1"}, {"8.4100", "8.41", "0.4100", "1"}, {nil, nil, nil, nil}})
	wantTuples(t, db, "SELECT rounded,trimmed,remainder FROM math_view ORDER BY id", [][]any{{"1.2", "1.23", "1.23"}, {"8.4", "8.41", "0.4100"}, {nil, nil, nil}})
	wantTuples(t, db, "SELECT result FROM null_math_view", [][]any{{nil}, {nil}, {nil}})
	var negativeZero float64
	if err := db.QueryRow("SELECT d FROM materialized WHERE id=1").Scan(&negativeZero); err != nil || negativeZero != 0 || !math.Signbit(negativeZero) {
		t.Fatal(negativeZero, err)
	}
	var redump bytes.Buffer
	if err := stuntgres.Dump(ctx, db, &redump); err != nil || redump.String() != dump.String() {
		t.Fatal("deterministic replay", err)
	}
	types, err := held.ColumnTypes()
	if err != nil || types[0].DatabaseTypeName() != "NUMERIC" {
		t.Fatal(types, err)
	}
	if !held.Next() {
		t.Fatal(held.Err())
	}
	var a, b string
	if err := held.Scan(&a, &b); err != nil || a != "1.2300" || b != "1.23" {
		t.Fatal(a, b, err)
	}
	if err := stuntgres.Restore(ctx, db, strings.NewReader("CREATE TABLE staged(n NUMERIC); INSERT INTO staged VALUES(1),('oops')")); err == nil {
		t.Fatal("invalid restore succeeded")
	}
	wantTuples(t, db, "SELECT padded FROM materialized ORDER BY id", [][]any{{"1.2300"}, {"8.4100"}, {nil}})
}
