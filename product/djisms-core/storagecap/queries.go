package storagecap

import (
	"errors"
	"strings"
)

type Query uint8

const SubscriberNumber Query = 255

type definition struct {
	command, prefix string
	identity        bool
}

var definitions = [...]definition{
	{"AT+CPMS?", "+CPMS:", false}, {"AT+CNMI?", "+CNMI:", false}, {"AT+CPMS=?", "+CPMS:", false}, {"AT+CMGL=?", "+CMGL:", false}, {"AT+CMGR=?", "+CMGR:", false}, {"AT+CMGD=?", "+CMGD:", false}, {"AT+CMGW=?", "+CMGW:", false},
}

func (q Query) command() (string, error) {
	if q < 1 || int(q) > len(definitions) {
		return "", errors.New("outside immutable capability query plan")
	}
	return definitions[q-1].command, nil
}
func identityLine(command, text string) bool { return false }
func validateResponse(q Query, lines []string) error {
	if q >= 3 && len(lines) == 1 && lines[0] == "ERROR" {
		return nil
	}
	if q >= 4 && len(lines) == 0 {
		return nil
	}
	if len(lines) != 1 || !strings.HasPrefix(lines[0], definitions[q-1].prefix) {
		return errors.New("unexpected capability response")
	}
	return nil
}
