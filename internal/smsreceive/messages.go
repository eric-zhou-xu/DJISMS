package smsreceive

import (
	"errors"
	"strconv"
	"strings"
)

// RawMessage preserves exactly the device-returned header and PDU spelling.
// Status is the device-reported status, not a reconstructed pre-read state.
type RawMessage struct {
	Index      int    `json:"storage_index"`
	Status     int    `json:"reported_status"`
	Alpha      string `json:"reported_alpha"`
	TPDULength int    `json:"reported_tpdu_octets"`
	Header     string `json:"raw_header"`
	HeaderHex  string `json:"raw_header_line_hex"`
	PDU        string `json:"raw_pdu"`
	PDULineHex string `json:"raw_pdu_line_hex"`
	Offset     int    `json:"stream_byte_offset"`
}

func hexLine(s string) bool {
	if len(s) < 2 || len(s)%2 != 0 {
		return false
	}
	for _, c := range s {
		if !(c >= '0' && c <= '9' || c >= 'A' && c <= 'F' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}
func parseHeader(s string) (m RawMessage, e error) {
	raw, ok := strings.CutPrefix(s, "+CMGL:")
	if !ok {
		return m, errors.New("not a CMGL header")
	}
	v, e := csvFields(raw)
	if e != nil {
		return m, e
	}
	if len(v) != 3 && len(v) != 4 {
		return m, errors.New("invalid PDU list header fields")
	}
	m.Header = s
	m.Index, e = strconv.Atoi(strings.TrimSpace(v[0]))
	if e != nil || m.Index < 0 || m.Index > 65535 {
		return m, errors.New("invalid SMS index")
	}
	m.Status, e = strconv.Atoi(strings.TrimSpace(v[1]))
	if e != nil || m.Status < 0 || m.Status > 3 {
		return m, errors.New("invalid SMS status")
	}
	m.TPDULength, e = strconv.Atoi(strings.TrimSpace(v[len(v)-1]))
	if e != nil || m.TPDULength < 1 || m.TPDULength > 255 {
		return m, errors.New("invalid TPDU declared size")
	}
	if len(v) == 4 {
		m.Alpha = v[2]
	}
	return m, nil
}
func storageFields(a Report) ([]string, error) {
	if len(a.ResponseLines) != 1 {
		return nil, errors.New("storage response missing")
	}
	s, ok := strings.CutPrefix(a.ResponseLines[0], "+CPMS:")
	if !ok {
		return nil, errors.New("storage prefix")
	}
	return csvFields(s)
}
func validatePrerequisites(q []Report) error {
	if len(q) != 3 {
		return errors.New("SMS prerequisites not recorded")
	}
	if len(q[0].ResponseLines) != 1 || strings.TrimSpace(strings.TrimPrefix(q[0].ResponseLines[0], "+CMGF:")) != "0" {
		return errors.New("PDU mode prerequisite failed; no setter allowed")
	}
	v, e := storageFields(q[1])
	if e != nil {
		return e
	}
	if len(v) != 9 {
		return errors.New("storage shape")
	}
	for i := 0; i < 9; i += 3 {
		if v[i] != "ME" || v[i+2] != "23" {
			return errors.New("storage differs from inspected ME/23 profile; no switching")
		}
	}
	if len(q[2].ResponseLines) != 1 || strings.ReplaceAll(q[2].ResponseLines[0], " ", "") != "+CNMI:2,1,0,0,0" {
		return errors.New("notification settings differ from inspected profile; no setter allowed")
	}
	return nil
}
func reconcileSettings(q []Report) error {
	if len(q) != 7 {
		return errors.New("incomplete receive plan")
	}
	before, e := storageFields(q[1])
	if e != nil {
		return e
	}
	after, e := storageFields(q[4])
	if e != nil {
		return e
	}
	if strings.Join(before, "\x00") != strings.Join(after, "\x00") {
		return errors.New("SMS storage changed during acquisition; preserve evidence, no retry")
	}
	if strings.Join(q[0].ResponseLines, "\n") != strings.Join(q[5].ResponseLines, "\n") || strings.Join(q[2].ResponseLines, "\n") != strings.Join(q[6].ResponseLines, "\n") {
		return errors.New("SMS format/notification reconciliation mismatch")
	}
	used, e := strconv.Atoi(before[1])
	if e != nil {
		return e
	}
	if len(q[3].Messages) != used {
		return errors.New("message count does not match selected storage; snapshot incomplete")
	}
	return nil
}
