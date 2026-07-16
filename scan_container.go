package jseek

func skipContainerGeneric(data []byte, i int) (int, bool) {
	n := len(data)
	if i >= n {
		return i, false
	}
	i++
	depth := 1
	for i < n {
		c := data[i]
		if c == '"' {
			end, ok := skipStringBody(data, i+1)
			if !ok {
				return n, false
			}
			i = end
			continue
		}
		if c == '{' || c == '[' {
			depth++
			i++
			continue
		}
		if c == '}' || c == ']' {
			depth--
			i++
			if depth == 0 {
				return i, true
			}
			continue
		}
		i++
		for i+4 <= n {
			c0 := data[i]
			if c0 == '"' || c0 == '{' || c0 == '[' || c0 == '}' || c0 == ']' {
				break
			}
			c1 := data[i+1]
			if c1 == '"' || c1 == '{' || c1 == '[' || c1 == '}' || c1 == ']' {
				i++
				break
			}
			c2 := data[i+2]
			if c2 == '"' || c2 == '{' || c2 == '[' || c2 == '}' || c2 == ']' {
				i += 2
				break
			}
			c3 := data[i+3]
			if c3 == '"' || c3 == '{' || c3 == '[' || c3 == '}' || c3 == ']' {
				i += 3
				break
			}
			i += 4
		}
	}
	return i, false
}
