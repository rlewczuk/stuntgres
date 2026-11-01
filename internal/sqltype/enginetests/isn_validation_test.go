package enginetests

import (
	"reflect"
	"testing"
	"unsafe"

	"stuntgres/internal/sqltype"
	"stuntgres/internal/util"
)

func TestIsnValidationCodec(t *testing.T) {
	var boolean *sqltype.Descriptor
	for _, d := range sqltype.Builtins() {
		if d.ID() == sqltype.Bool { boolean = d }
	}
	types := isnUnitTypes(t)
	foreign := isnUnitTypes(t)
	for i, tc := range []struct {
		bad, good string
		number int64
	}{
		{"2222222222221", "2222222222222", 2222222222222},
		{"1234567890", "123456789X", 9781234567897},
		{"9781234567890", "9781234567897", 9781234567897},
		{"M123456780", "M123456785", 9790123456785},
		{"9790123456780", "9790123456785", 9790123456785},
		{"12345670", "12345679", 9771234567003},
		{"9771234567000", "9771234567003", 9771234567003},
		{"123456789010", "123456789012", 123456789012},
	} {
		d := types[i]
		if _, err := d.Input(t.Context(), tc.bad, -1); err == nil { t.Fatal("strict checksum accepted", i) }
		marked, err := d.InputWithEnvironment(t.Context(), tc.bad, -1, sqltype.InputEnvironment{IsnWeak: true})
		if err != nil { t.Fatal(i, err) }
		if got := reflect.ValueOf(marked).FieldByName("integer").Int(); got != 2*tc.number+1 { t.Fatal("independent packed input", i, got) }
		truth, err := sqltype.IsValidISN(marked, d.Usage(), boolean)
		if err != nil { t.Fatal(err) }
		if b, err := truth.Boolean(); err != nil || b { t.Fatal("invalid bit", i, err) }
		repaired, err := sqltype.MakeValidISN(marked, d.Usage())
		if err != nil || repaired.Type() != d || reflect.ValueOf(repaired).FieldByName("integer").Int() != 2*tc.number { t.Fatal("repair packed value/type", i, err) }
		good := isnUnitInput(t, d, tc.good)
		if !sqltype.SameDatum(repaired, good) || sqltype.SameDatum(marked, good) { t.Fatal("repair or original identity", i) }
		again, err := sqltype.MakeValidISN(repaired, d.Usage())
		if err != nil || !sqltype.SameDatum(again, repaired) { t.Fatal("idempotence", i, err) }
		if reflect.ValueOf(marked).FieldByName("integer").Int() != 2*tc.number+1 { t.Fatal("source mutated", i) }
		for _, value := range []sqltype.Value{marked, sqltype.Null(d)} {
			if _, err := sqltype.MakeValidISN(value, foreign[i].Usage()); err == nil { t.Fatal("foreign generation accepted", i) }
			if _, err := sqltype.IsValidISN(value, foreign[i].Usage(), boolean); err == nil { t.Fatal("foreign validity generation", i) }
		}
		packed := []int64{-1, 20000000000000}
		if i != 0 { packed = append(packed, 4444444444444) }
		for _, number := range packed {
			bad := marked
			field := reflect.ValueOf(&bad).Elem().FieldByName("integer")
			*(*int64)(unsafe.Pointer(field.UnsafeAddr())) = number
			if _, err := sqltype.MakeValidISN(bad, d.Usage()); err == nil { t.Fatal("invalid packed role accepted", i, number) }
			if _, err := sqltype.IsValidISN(bad, d.Usage(), boolean); err == nil { t.Fatal("invalid packed validity role", i, number) }
		}
		wrongUsage := d.Usage()
		wrongUsage.Typmod = 0
		if _, err := sqltype.MakeValidISN(marked, wrongUsage); err == nil { t.Fatal("invalid formal framing") }
		for _, operation := range []func() (sqltype.Value, error){
			func() (sqltype.Value, error) { return sqltype.MakeValidISN(sqltype.Null(d), d.Usage()) },
			func() (sqltype.Value, error) { return sqltype.IsValidISN(sqltype.Null(d), d.Usage(), boolean) },
		} {
			value, err := operation()
			if err != nil || !value.IsNull() { t.Fatal("typed strict NULL", i, err) }
		}
		var hot sqltype.Value
		if allocations, bytes := util.AllocationsPerCall(64, func() {
			var err error
			hot, err = sqltype.MakeValidISN(marked, d.Usage())
			if err != nil { panic(err) }
			if _, err := sqltype.IsValidISN(hot, d.Usage(), boolean); err != nil { panic(err) }
		}); allocations != 0 || bytes != 0 { t.Fatal("inline operation allocated", i, allocations, bytes) }
		if !sqltype.SameDatum(hot, good) { t.Fatal("inline operation lost datum") }
		if _, err := d.InputWithEnvironment(t.Context(), "bad", -1, sqltype.InputEnvironment{IsnWeak: true}); err == nil { t.Fatal("weak syntax bypass", i) }
	}
	for _, i := range []int{0, 7} {
		marked := isnUnitInput(t, types[i], "0000000000000!")
		plain, err := sqltype.MakeValidISN(marked, types[i].Usage())
		if err != nil || reflect.ValueOf(marked).FieldByName("integer").Int() != 1 || reflect.ValueOf(plain).FieldByName("integer").Int() != 0 { t.Fatal("zero flag boundary", i, err) }
	}
	for _, tc := range []struct {
		role int
		input string
		number int64
	}{{0, "9999999999994!", 9999999999994}, {5, "9771234567898!", 9771234567898}, {6, "9771234567898!", 9771234567898}} {
		marked := isnUnitInput(t, types[tc.role], tc.input)
		plain, err := sqltype.MakeValidISN(marked, types[tc.role].Usage())
		if err != nil || reflect.ValueOf(marked).FieldByName("integer").Int() != tc.number*2+1 || reflect.ValueOf(plain).FieldByName("integer").Int() != tc.number*2 {
			t.Fatal("upper/issue numeric boundary", tc.role, err)
		}
	}
}
