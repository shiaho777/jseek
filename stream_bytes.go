package jseek

import "math/bits"

func StreamBytes(data []byte, cb func(value []byte) error) error {
	i := skipWhitespace(data, 0)
	if i >= len(data) {
		return nil
	}

	inArray := false
	if data[i] == '[' {
		inArray = true
		i = skipWhitespace(data, i+1)
		if i < len(data) && data[i] == ']' {
			return nil
		}
	}

	for i < len(data) {
		vs, ve, _, ok := valueBounds(data, i)
		if !ok {
			return ErrMalformedJSON
		}
		elemStart := i
		var elemEnd int
		if data[i] == '"' {
			elemEnd = ve + 1
		} else {
			elemEnd = ve
		}
		_ = vs
		if cberr := cb(data[elemStart:elemEnd]); cberr != nil {
			return cberr
		}

		i = skipWhitespace(data, elemEnd)
		if inArray {
			if i >= len(data) {
				return ErrMalformedJSON
			}
			switch data[i] {
			case ',':
				i = skipWhitespace(data, i+1)
			case ']':
				return nil
			default:
				return ErrMalformedJSON
			}
		} else {
			if i >= len(data) {
				return nil
			}
		}
	}
	return nil
}

func indexNL(data []byte, i int) int {
	n := len(data)
	nl := broadcast('\n')
	cr := broadcast('\r')
	for i+32 <= n {
		v0 := load64(data, i)
		m0 := zeroByteMask(v0^nl) | zeroByteMask(v0^cr)
		if m0 != 0 {
			return i + bits.TrailingZeros64(m0)>>3
		}
		v1 := load64(data, i+8)
		m1 := zeroByteMask(v1^nl) | zeroByteMask(v1^cr)
		if m1 != 0 {
			return i + 8 + bits.TrailingZeros64(m1)>>3
		}
		v2 := load64(data, i+16)
		m2 := zeroByteMask(v2^nl) | zeroByteMask(v2^cr)
		if m2 != 0 {
			return i + 16 + bits.TrailingZeros64(m2)>>3
		}
		v3 := load64(data, i+24)
		m3 := zeroByteMask(v3^nl) | zeroByteMask(v3^cr)
		if m3 != 0 {
			return i + 24 + bits.TrailingZeros64(m3)>>3
		}
		i += 32
	}
	for i+8 <= n {
		v := load64(data, i)
		m := zeroByteMask(v^nl) | zeroByteMask(v^cr)
		if m != 0 {
			return i + bits.TrailingZeros64(m)>>3
		}
		i += 8
	}
	for i < n {
		c := data[i]
		if c == '\n' || c == '\r' {
			return i
		}
		i++
	}
	return n
}

func StreamNDJSON(data []byte, cb func(value []byte) error) error {
	n := len(data)
	i := 0
	for i < n {
		for i < n {
			c := data[i]
			if c == '\n' || c == '\r' || c == ' ' || c == '\t' {
				i++
				continue
			}
			break
		}
		if i >= n {
			return nil
		}
		start := i
		nl := indexNL(data, i)
		end := nl
		for end > start {
			c := data[end-1]
			if c == ' ' || c == '\t' {
				end--
				continue
			}
			break
		}
		if end > start {
			if err := cb(data[start:end]); err != nil {
				return err
			}
		}
		i = nl
		for i < n {
			c := data[i]
			if c == '\n' || c == '\r' {
				i++
				continue
			}
			break
		}
	}
	return nil
}

func StreamNDJSONEach(data []byte, p *Paths, cb func(idx int, value []byte, dataType ValueType, err error) error) error {
	if p == nil {
		return nil
	}
	return StreamNDJSON(data, func(line []byte) error {
		var stop error
		p.Each(line, func(idx int, value []byte, vt ValueType, err error) {
			if stop != nil {
				return
			}
			if e := cb(idx, value, vt, err); e != nil {
				stop = e
			}
		})
		return stop
	})
}
