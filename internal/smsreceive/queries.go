package smsreceive

import (
	"encoding/csv"
	"errors"
	"strconv"
	"strings"
)

type Query uint8

const (
	MessageFormat Query = iota + 1
	Storage
	Notifications
	ListMessages
	StorageAfter
	MessageFormatAfter
	NotificationsAfter
)

type definition struct {
	command, prefix string
	identity        bool
}

var definitions = [...]definition{{"AT+CMGF?", "+CMGF:", false}, {"AT+CPMS?", "+CPMS:", false}, {"AT+CNMI?", "+CNMI:", false}, {"AT+CMGL=4", "+CMGL:", false}, {"AT+CPMS?", "+CPMS:", false}, {"AT+CMGF?", "+CMGF:", false}, {"AT+CNMI?", "+CNMI:", false}}

func (q Query) command() (string, error) {
	if q < 1 || int(q) > len(definitions) {
		return "", errors.New("outside fixed SMS receive plan")
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
	if q == ListMessages {
		return nil
	}
	switch q {
	case StorageAfter:
		q = Storage
	case MessageFormatAfter:
		q = MessageFormat
	case NotificationsAfter:
		q = Notifications
	}

	if len(lines) != 1 {
		return errors.New("missing or duplicate settings response")
	}
	raw, ok := strings.CutPrefix(lines[0], definitions[q-1].prefix)
	if !ok {
		return errors.New("wrong settings prefix")
	}
	v, e := csvFields(raw)
	if e != nil {
		return e
	}
	number := func(i, lo, hi int) bool {
		if i >= len(v) {
			return false
		}
		n, e := strconv.Atoi(strings.TrimSpace(v[i]))
		return e == nil && n >= lo && n <= hi
	}
	switch q {
	case MessageFormat:
		if len(v) != 1 || !number(0, 0, 1) {
			return errors.New("invalid message format")
		}
	case Storage:
		if len(v) != 9 {
			return errors.New("invalid storage response")
		}
		for i := 0; i < 9; i += 3 {
			if len(v[i]) < 1 || len(v[i]) > 8 || !number(i+1, 0, 100000) || !number(i+2, 0, 100000) {
				return errors.New("invalid storage fields")
			}
			used, _ := strconv.Atoi(v[i+1])
			total, _ := strconv.Atoi(v[i+2])
			if used > total {
				return errors.New("storage used exceeds total")
			}
		}
	case Notifications:
		if len(v) != 5 || !number(0, 0, 3) || !number(1, 0, 3) || !number(2, 0, 3) || !number(3, 0, 2) || !number(4, 0, 1) {
			return errors.New("invalid notification settings")
		}
	default:
		return errors.New("unknown settings query")
	}
	return nil
}
