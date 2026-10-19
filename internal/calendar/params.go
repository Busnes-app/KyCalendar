package calendar

import (
	"bytes"
	"strings"
)

// MaxParamValue caps each parameter value KyCalendar decodes from outside. go-ical builds them a
// byte at a time, so decoding costs the value's length squared: a 256 KiB value takes seconds.
// Real values (TZID, CN, ALTREP) are far shorter.
const MaxParamValue = 1 << 10

// LongParams reports a content line with a parameter value over MaxParamValue, reading lines
// and parameters exactly as go-ical's decoder does (decoder.go: readContinuedLine,
// decodeContentLine, decodeParam), so no syntax the decoder accepts escapes the check. Linear.
func LongParams(body []byte) bool {
	var line []byte
	for len(body) > 0 {
		phys := body
		if i := bytes.IndexByte(body, '\n'); i >= 0 {
			phys, body = body[:i], body[i+1:]
		} else {
			body = nil
		}
		line = append(line, bytes.TrimSuffix(phys, []byte("\r"))...) // bufio.ReadLine drops only that \r
		if len(body) > 0 && (body[0] == ' ' || body[0] == '\t') {
			body = body[1:] // a fold: the decoder drops the space and joins the next line
			continue
		}
		if longParam(string(line)) {
			return true
		}
		line = line[:0]
	}
	return false
}

func longParam(s string) bool {
	i := strings.IndexAny(s, ";:")
	if i < 0 {
		return false
	}
	for s = s[i:]; strings.HasPrefix(s, ";"); {
		// The parameter name runs to the next '=' anywhere on the line, a ':' included.
		eq := strings.IndexByte(s, '=')
		if eq < 0 {
			return false
		}
		s = s[eq+1:]
		for {
			var n int
			if strings.HasPrefix(s, `"`) {
				end := strings.IndexByte(s[1:], '"')
				if end < 0 {
					return len(s)-1 > MaxParamValue // read to the end, then refused
				}
				n, s = end, s[end+2:]
			} else {
				end := strings.IndexAny(s, "\";,:")
				if end < 0 {
					end = len(s)
				}
				n, s = end, s[end:]
				if strings.HasPrefix(s, `"`) {
					return n > MaxParamValue // read up to the quote, then refused
				}
			}
			if n > MaxParamValue {
				return true
			}
			if s == "" || s[0] != ',' {
				break // ';' next parameter, ':' the value; anything else the decoder refuses
			}
			s = s[1:]
		}
	}
	return false
}
