package status

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

// CardFingerprint exposes a stable local association without publishing the ICCID.
func CardFingerprint(line string) string {
	if !strings.HasPrefix(line, "+QCCID:") {
		return ""
	}
	raw := strings.TrimSpace(strings.TrimPrefix(line, "+QCCID:"))
	if len(raw) < 18 || len(raw) > 22 || strings.Trim(raw, "0123456789") != "" {
		return ""
	}
	h := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(h[:])
}
func PhoneNumber(line string) string {
	raw, ok := strings.CutPrefix(line, "+CNUM:")
	if !ok {
		return ""
	}
	f, e := csvFields(raw)
	if e != nil || len(f) < 3 || len(f[1]) > 32 || strings.Trim(f[1], "+0123456789") != "" {
		return ""
	}
	return f[1]
}
