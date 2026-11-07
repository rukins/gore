package migrate

// splitSQL divides a script on semicolons that are outside single-quoted
// strings and "--" comments. Semicolons inside those stay put.
func splitSQL(s string) []string {
	var (
		b       []byte
		out     []string
		inQuote bool
		inLine  bool
	)
	flush := func() {
		out = append(out, string(b))
		b = b[:0]
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case inLine:
			b = append(b, c)
			if c == '\n' {
				inLine = false
			}
		case inQuote:
			b = append(b, c)
			if c == '\'' {
				if i+1 < len(s) && s[i+1] == '\'' {
					b = append(b, '\'')
					i++
					continue
				}
				inQuote = false
			}
		case c == '-' && i+1 < len(s) && s[i+1] == '-':
			inLine = true
			b = append(b, c)
		case c == '\'':
			inQuote = true
			b = append(b, c)
		case c == ';':
			flush()
		default:
			b = append(b, c)
		}
	}
	if len(b) > 0 {
		flush()
	}
	return out
}
