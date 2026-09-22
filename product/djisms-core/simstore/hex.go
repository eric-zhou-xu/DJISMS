package simstore

import "encoding/hex"

func decodeHex(s string) ([]byte, error) { return hex.DecodeString(s) }
