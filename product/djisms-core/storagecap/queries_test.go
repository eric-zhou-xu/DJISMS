package storagecap

import (
	"strings"
	"testing"
)

func TestCapabilityFixedPlan(t *testing.T) {
	for i, d := range definitions {
		if strings.Contains(d.command, "=\"") || strings.Contains(d.command, "CMGD=") && !strings.HasSuffix(d.command, "=?") {
			t.Fatal(d)
		}
		q := Query(i + 1)
		p := newATStream(1, q)
		if e := p.markWritten(); e != nil {
			t.Fatal(e)
		}
		body := d.prefix + " (0,1,2)"
		if i == 0 {
			body = `+CPMS: "ME",0,23,"ME",0,23,"ME",0,23`
		}
		if i == 1 {
			body = "+CNMI: 2,1,0,0,0"
		}
		if e := p.feed([]byte(d.command + "\r\r\n" + body + "\r\nOK\r\n")); e != nil {
			t.Fatal(e)
		}
		if e := p.validateSuccess(); e != nil {
			t.Fatal(e)
		}
	}
}
func TestCapabilityExplicitRejection(t *testing.T) {
	p := newATStream(1, 3)
	p.markWritten()
	if e := p.feed([]byte("AT+CPMS=?\r\r\nERROR\r\n")); e != nil {
		t.Fatal(e)
	}
	if !p.rejected || !p.complete() {
		t.Fatal("rejection not classified")
	}
	for _, v := range []string{"ERROR\r\n", "AT+CPMS=?\r\r\nUNKNOWN\r\nOK\r\n"} {
		q := newATStream(1, 3)
		q.markWritten()
		if q.feed([]byte(v)) == nil {
			t.Fatal("accepted", v)
		}
	}
}
