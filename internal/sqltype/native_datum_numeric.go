package sqltype

import "fmt"

func (w *nativeDatumWriter) numeric(v Value) error {
	switch v.special {
	case numericNaN:
		return w.word(2, 0xc000)
	case numericInfinity:
		return w.word(2, 0xd000)
	case numericNegativeInfinity:
		return w.word(2, 0xf000)
	}
	if v.scale < 0 || v.scale > 16383 {
		return fmt.Errorf("invalid native numeric scale")
	}
	point := int64(len(v.text)) + v.exponent
	weight, low, count := int64(0), int64(0), int64(0)
	if v.text != "" {
		weight = floorDiv(point-1, 4)
		low = floorDiv(v.exponent, 4)
		count = weight - low + 1
	}
	if weight < -32768 || weight > 32767 {
		return fmt.Errorf("invalid native numeric weight")
	}
	sign := uint64(0)
	if v.negative {
		sign = 0x4000
	}
	// Short header carries sign, display scale and signed seven-bit weight.
	if v.scale <= 63 && weight >= -64 && weight <= 63 {
		header := uint64(0x8000) | uint64(v.scale)<<7 | uint64(weight)&0x7f
		if sign != 0 {
			header |= 0x2000
		}
		if err := w.word(2, header); err != nil {
			return err
		}
	} else {
		if err := w.word(2, sign|uint64(v.scale)); err != nil {
			return err
		}
		if err := w.word(2, uint64(weight)); err != nil {
			return err
		}
	}
	for group := weight; count > 0 && group >= low; group-- {
		digit := uint64(0)
		for place := group*4 + 3; place >= group*4; place-- {
			digit *= 10
			index := point - 1 - place
			if index >= 0 && index < int64(len(v.text)) {
				digit += uint64(v.text[index] - '0')
			}
		}
		if err := w.word(2, digit); err != nil {
			return err
		}
	}
	return w.ctx.Err()
}
