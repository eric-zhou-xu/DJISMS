package status

import "testing"

func TestSIMReadOnlyIdentity(t *testing.T) {
	a := CardFingerprint("+QCCID: 89860000000000000001")
	b := CardFingerprint("+QCCID: 89860000000000000002")
	if len(a) != 64 || a == b || CardFingerprint("+QCCID: invalid") != "" {
		t.Fatal("unstable or invalid card identity")
	}
	if PhoneNumber(`+CNUM: ,"+8613800000000",145`) != "+8613800000000" {
		t.Fatal("number parsing")
	}
	if PhoneNumber(`+CNUM: ,"bad",145`) != "" {
		t.Fatal("invalid number")
	}
	if e := validateResponse(SubscriberNumber, nil); e != nil {
		t.Fatal(e)
	}
	p := newATStream(1, SubscriberNumber)
	_ = p.markWritten()
	if e := p.feed([]byte("AT+CNUM\r\r\nOK\r\n")); e != nil {
		t.Fatal(e)
	}
	if e := p.validateSuccess(); e != nil {
		t.Fatal(e)
	}
	p = newATStream(1, PINState)
	_ = p.markWritten()
	_ = p.feed([]byte("AT+CPIN?\r\r\nOK\r\n"))
	if p.validateSuccess() == nil {
		t.Fatal("old required response weakened")
	}
}
