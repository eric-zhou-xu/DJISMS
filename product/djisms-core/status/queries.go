package status

import (
	"encoding/csv"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

type Query uint8

const (
	Manufacturer Query = iota + 1
	Model
	Revision
	PINState
	SignalQuality
	Registration
	EPSRegistration
	Operator
	SIMCardID
	SubscriberNumber
)

type definition struct {
	command, prefix string
	identity        bool
}

var definitions = [...]definition{
	{"AT+CGMI", "+CGMI:", true}, {"AT+CGMM", "+CGMM:", true}, {"AT+CGMR", "+CGMR:", true},
	{"AT+CPIN?", "+CPIN:", false}, {"AT+CSQ", "+CSQ:", false},
	{"AT+CREG?", "+CREG:", false}, {"AT+CEREG?", "+CEREG:", false}, {"AT+COPS?", "+COPS:", false},
	{"AT+QCCID", "+QCCID:", false}, {"AT+CNUM", "+CNUM:", false},
}

func (q Query) command() (string, error) {
	if q < 1 || int(q) > len(definitions) {
		return "", errors.New("query outside fixed read-only plan")
	}
	return definitions[q-1].command, nil
}
func csvFields(s string) ([]string, error) {
	r := csv.NewReader(strings.NewReader(strings.TrimSpace(s)))
	r.TrimLeadingSpace = true
	r.FieldsPerRecord = -1
	return r.Read()
}
func validateResponse(q Query, lines []string) error {
	if q == SubscriberNumber && len(lines) == 0 {
		return nil
	}
	if len(lines) == 0 || len(lines) > 8 {
		return errors.New("missing or oversized response body")
	}
	if definitions[q-1].identity {
		return nil
	}
	if len(lines) != 1 {
		return errors.New("ambiguous duplicate query body")
	}
	raw, ok := strings.CutPrefix(lines[0], definitions[q-1].prefix)
	if !ok {
		return errors.New("wrong query body")
	}
	if q == PINState {
		if strings.TrimSpace(raw) == "" {
			return errors.New("empty SIM state")
		}
		return nil
	}
	fields, e := csvFields(raw)
	if e != nil {
		return e
	}
	number := func(i int, lo, hi int) bool {
		if i >= len(fields) {
			return false
		}
		v, e := strconv.Atoi(strings.TrimSpace(fields[i]))
		return e == nil && v >= lo && v <= hi
	}
	switch q {
	case SIMCardID:
		digits := strings.TrimSpace(raw)
		if len(digits) < 18 || len(digits) > 22 || strings.Trim(digits, "0123456789") != "" {
			return errors.New("invalid SIM card identifier")
		}
	case SubscriberNumber:
		if len(fields) < 3 || len(fields) > 6 || len(fields[1]) > 32 || strings.Trim(fields[1], "+0123456789") != "" {
			return errors.New("invalid subscriber number")
		}
	case SignalQuality:
		if len(fields) != 2 || !(number(0, 0, 31) || number(0, 99, 99)) || !(number(1, 0, 7) || number(1, 99, 99)) {
			return errors.New("invalid CSQ fields")
		}
	case Registration, EPSRegistration:
		// Read response starts with unquoted n,stat. URCs have stat and optional quoted location.
		parts := strings.SplitN(strings.TrimSpace(raw), ",", 3)
		if len(parts) < 2 || strings.ContainsAny(parts[0]+parts[1], "\"") || !number(0, 0, 7) || !number(1, 0, 10) {
			return errors.New("registration body is not n,stat read response")
		}
	case Operator:
		if !number(0, 0, 4) || (len(fields) > 1 && !number(1, 0, 2)) || len(fields) > 4 {
			return errors.New("invalid COPS read fields")
		}
	default:
		return fmt.Errorf("unhandled query %d", q)
	}
	return nil
}
