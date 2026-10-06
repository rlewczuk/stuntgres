package stuntgres_test

import (
	"stuntgres"
	"testing"
	"time"
)

var timePublicInputs = []struct{ input, want string }{
	{"04:05:06.789", "04:05:06.789"}, {"04:05:06", "04:05:06"}, {"04:05", "04:05:00"}, {"040506", "04:05:06"}, {"0405", "04:05:00"}, {"04:05.5", "00:04:05.5"}, {"4:5:6", "04:05:06"},
	{"04:05 AM", "04:05:00"}, {"04:05 pm", "16:05:00"}, {"12:00 AM", "00:00:00"}, {"12:00 PM", "12:00:00"}, {"00:00 AM", "00:00:00"}, {"00:00 PM", "12:00:00"},
	{"04:05:06.789-8", "04:05:06.789"}, {"04:05:06-08:00", "04:05:06"}, {"04:05:06+0730", "04:05:06"}, {"04:05:06+07:30:00", "04:05:06"}, {"02:03 PST", "02:03:00"}, {"11:59 EDT", "11:59:00"},
	{"2003-03-07 15:36:39 America/New_York", "15:36:39"}, {"2003-07-07 15:36:39 America/New_York", "15:36:39"}, {"2003-07-07 15:36:39", "15:36:39"}, {"2003-07-07 15:36:39 Asia/Kolkata", "15:36:39"},
	{"04:05 Etc/GMT+5", "04:05:00"}, {"04:05 eTc/UtC", "04:05:00"}, {"04:05 Universal", "04:05:00"}, {"04:05 X0", "04:05:00"}, {"04:05 X0Y0", "04:05:00"}, {"2003-07-07 04:05 X0Y", "04:05:00"},
	{"04:05 MET DST", "04:05:00"}, {"04:05 MSK", "04:05:00"}, {"t040506", "04:05:06"}, {"t04:05:06", "04:05:06"}, {"j2451545.5", "12:00:00"}, {"j2451545 04:05", "04:05:00"}, {"04:05 on", "04:05:00"}, {"04:05 AD", "04:05:00"}, {"allballs", "00:00:00"},
	{"23:59:60", "24:00:00"}, {"23:59:59.9999999", "24:00:00"}, {"04:05:60", "04:06:00"}, {"04:05:06.0000005", "04:05:06"}, {"04:05:06.0000015", "04:05:06.000002"},
}

func TestTimeAcceptedInputs(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	execSQL(t, db, "CREATE TABLE anchor(id INTEGER)")
	execSQL(t, db, "INSERT INTO anchor VALUES(1)")
	execSQL(t, db, "CREATE TABLE t(id INTEGER,v TIME)")
	ins, err := db.Prepare("INSERT INTO t VALUES($1,$2)")
	if err != nil {
		t.Fatal(err)
	}
	defer ins.Close()
	for i, tc := range timePublicInputs {
		t.Run(tc.input, func(t *testing.T) {
			q := "SELECT TIME WITHOUT TIME ZONE '" + tc.input + "' FROM anchor"
			timeQuery(t, db, q, []string{"time"}, []string{"TIME"}, [][]any{{tc.want}})
			stmt, err := db.Prepare(q)
			if err != nil {
				t.Fatal(err)
			}
			rows, err := stmt.Query()
			if err != nil {
				t.Fatal(err)
			}
			timeCheckRows(t, rows, []string{"time"}, []string{"TIME"}, [][]any{{tc.want}})
			stmt.Close()
			execSQL(t, db, "INSERT INTO t VALUES($1,$2)", i*2, tc.input)
			result, err := ins.Exec(i*2+1, tc.input)
			if err != nil {
				t.Fatal(err)
			}
			n, err := result.RowsAffected()
			if err != nil || n != 1 {
				t.Fatal(n, err)
			}
			timeQuery(t, db, "SELECT v FROM t WHERE id=$1", []string{"v"}, []string{"TIME"}, [][]any{{tc.want}}, i*2+1)
		})
	}
}

func TestTimeParametersPrecisionAndOrdering(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	execSQL(t, db, "CREATE TABLE p(id INTEGER,v TIME(0),w TIME(6))")
	ins, err := db.Prepare("INSERT INTO p VALUES($1,$2,$2)")
	if err != nil {
		t.Fatal(err)
	}
	defer ins.Close()
	for _, tc := range []struct {
		id  int
		arg any
	}{{1, "00:00"}, {2, "24:00"}, {3, nil}, {4, []byte("04:05:06.5")}, {5, time.Date(2003, 7, 7, 15, 36, 39, 123456000, time.FixedZone("offset", 7*3600))}} {
		r, err := ins.Exec(tc.id, tc.arg)
		if err != nil {
			t.Fatal(err)
		}
		n, err := r.RowsAffected()
		if err != nil || n != 1 {
			t.Fatal(n, err)
		}
	}
	timeQuery(t, db, "SELECT id,v,w FROM p ORDER BY id", []string{"id", "v", "w"}, []string{"INT4", "TIME", "TIME"}, [][]any{{int64(1), "00:00:00", "00:00:00"}, {int64(2), "24:00:00", "24:00:00"}, {int64(3), nil, nil}, {int64(4), "04:05:07", "04:05:06.5"}, {int64(5), "15:36:39", "15:36:39.123456"}})
	held, err := db.Prepare("SELECT id FROM p WHERE v=$1 ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	for _, tc := range []struct {
		arg  any
		want [][]any
	}{{"00:00", [][]any{{int64(1)}}}, {"24:00", [][]any{{int64(2)}}}, {nil, nil}, {"00:00", [][]any{{int64(1)}}}} {
		rows, err := held.Query(tc.arg)
		if err != nil {
			t.Fatal(err)
		}
		timeCheckRows(t, rows, []string{"id"}, []string{"INT4"}, tc.want)
	}
	execSQL(t, db, "CREATE TABLE rounding(v TIME(0))")
	execSQL(t, db, "INSERT INTO rounding VALUES(TIME(3) '04:05:06.4995'),('04:05:06.4995'),('23:59:59.5')")
	timeQuery(t, db, "SELECT v FROM rounding ORDER BY v", []string{"v"}, []string{"TIME"}, [][]any{{"04:05:06"}, {"04:05:07"}, {"24:00:00"}})
	execSQL(t, db, "CREATE TABLE ordering(v TIME)")
	execSQL(t, db, "INSERT INTO ordering VALUES('00:00'),('04:05:06.1'),('04:05:06.2'),('24:00'),(NULL)")
	timeQuery(t, db, "SELECT v FROM ordering ORDER BY v", []string{"v"}, []string{"TIME"}, [][]any{{"00:00:00"}, {"04:05:06.1"}, {"04:05:06.2"}, {"24:00:00"}, {nil}})
	timeQuery(t, db, "SELECT v FROM ordering ORDER BY v DESC", []string{"v"}, []string{"TIME"}, [][]any{{nil}, {"24:00:00"}, {"04:05:06.2"}, {"04:05:06.1"}, {"00:00:00"}})
	timeQuery(t, db, "SELECT v FROM ordering WHERE v='24:00'", []string{"v"}, []string{"TIME"}, [][]any{{"24:00:00"}})
	bytes := []byte("04:05:06.2")
	rows, err := db.Query("SELECT v FROM ordering WHERE v=$1", bytes)
	if err != nil {
		t.Fatal(err)
	}
	copy(bytes, "00:00:00.0")
	timeCheckRows(t, rows, []string{"v"}, []string{"TIME"}, [][]any{{"04:05:06.2"}})
}
