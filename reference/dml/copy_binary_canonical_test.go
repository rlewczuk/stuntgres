package reference_test

// Canonical output is constructed from semantic values and provider layouts,
// never by extracting a field from recorded COPY output. Unlisted accepted
// fixtures retain their independently supplied payload unchanged.
func copyBinaryCanonical(name string, input []byte) []byte {
	switch name {
	case "bool_noncanonical":
		return []byte{1}
	case "bpchar_padding":
		return []byte("a  ")
	case "time_precision", "timestamp_precision", "timestamptz_precision":
		return copyBinary64(1235000)
	case "interval_precision":
		return append(append(copyBinary64(1235000), copyBinary32(2)...), copyBinary32(3)...)
	case "numeric_nan":
		return copyBinaryHex("00000000c0000000")
	case "numeric_infinity":
		return copyBinaryHex("00000000d0000020")
	case "numeric_negative_infinity":
		return copyBinaryHex("00000000f0000020")
	case "numeric_hidden_digits":
		return copyBinaryHex("0001000000000000000c")
	case "numeric_typmod":
		return copyBinaryHex("0002000000000001000c0bb8")
	case "jsonb":
		return append([]byte{1}, []byte(`{"a": 1}`)...)
	case "jsonpath":
		return append([]byte{1}, []byte(`$."a"`)...)
	case "bit_padding":
		return append(copyBinary32(3), 0xa0)
	case "inet_ignored_cidr_flag":
		return copyBinaryHex("02180004c0000201")
	case "array_user_oid_for_builtin":
		return copyBinaryArray(23, [][2]int32{{1, 1}}, copyBinary32(1))
	case "composite_user_oid_for_builtin":
		return copyBinaryComposite([]int32{23, 25}, copyBinary32(42), []byte("hi"))
	case "range_discrete_canonicalization":
		return copyBinaryRange(2, copyBinary32(2), copyBinary32(4))
	case "range_unknown_flags":
		return []byte{1}
	case "range_bound_trailing":
		return copyBinaryRange(2, copyBinary32(1), copyBinary32(3))
	case "multirange_reorder_merge":
		return copyBinaryMultirange(copyBinaryRange(2, copyBinary32(1), copyBinary32(5)))
	}
	return input
}
