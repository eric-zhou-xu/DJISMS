package atproto

import "testing"

func TestTypedPDULengthAndMTIBounds(t *testing.T) {
	for _, v := range []struct {
		k, h, p string
		n       int
	}{{"CMT", "+CMT: ,2", "001034", 2}, {"CDS", "+CDS: 2", "000234", 2}, {"CBM", "+CBM: 2", "1234", 2}} {
		var d Demux
		yes, f, e := d.FeedLine(v.h, []byte(v.h+"\r\n"), 0)
		if e != nil || !yes || f != nil || !d.Pending() {
			t.Fatal(v, e)
		}
		yes, f, e = d.FeedLine(v.p, []byte(v.p+"\r\n"), len(v.h)+2)
		if e != nil || !yes || f == nil || f.Kind != v.k || d.Pending() {
			t.Fatal(v, e)
		}
	}
	for _, p := range []string{"", "0", "GG", "0200", "001234", "00103400"} {
		if e := PDU(p, 2, "CMT"); e == nil {
			t.Fatal(p)
		}
	}
}
func FuzzDemuxNeverTreatsTerminalAsPDU(f *testing.F) {
	f.Add("OK")
	f.Add("AT+CGMI")
	f.Fuzz(func(t *testing.T, body string) {
		if len(body) > 1024 {
			return
		}
		var d Demux
		d.FeedLine("+CMT: ,2", []byte("+CMT: ,2\r\n"), 0)
		_, frame, e := d.FeedLine(body, []byte(body+"\r\n"), 10)
		if e == nil && frame != nil {
			if PDU(frame.PDU, 2, "CMT") != nil {
				t.Fatal("invalid body accepted")
			}
		}
	})
}
