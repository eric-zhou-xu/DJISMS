//go:build darwin

// Package host verifies the existing ECM data path without changing networking.
package host

import (
	"context"
	"errors"
	"fmt"
	"github.com/iniwex5/vohive/product/djisms-core/discovery"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"reflect"
	"strings"
	"syscall"
	"time"
)

type Snapshot struct {
	Device           discovery.Device `json:"device"`
	IPv4             string           `json:"ipv4"`
	InterfaceIndex   int              `json:"interface_index"`
	DefaultGateway   string           `json:"default_gateway"`
	DefaultInterface string           `json:"default_interface"`
	SIP              string           `json:"sip"`
	HTTPS            bool             `json:"https_verified"`
	ObservedUTC      string           `json:"observed_utc"`
}

// NotReady describes an unavailable network before acquisition, not a failed
// reconciliation. Callers must never use it to excuse a post-operation check.
type NotReady struct{ Err error }

func (e NotReady) Error() string { return e.Err.Error() }
func (e NotReady) Unwrap() error { return e.Err }

func Capture(ctx context.Context, device discovery.Device) (Snapshot, error) {
	s := Snapshot{Device: device, ObservedUTC: time.Now().UTC().Format(time.RFC3339Nano)}
	if e := discovery.Validate(device); e != nil {
		return s, e
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	nic, e := net.InterfaceByName(device.Network)
	if e != nil {
		return s, NotReady{e}
	}
	if nic.Flags&net.FlagUp == 0 {
		return s, NotReady{errors.New("existing ECM interface is down")}
	}
	s.InterfaceIndex = nic.Index
	addrs, e := nic.Addrs()
	if e != nil {
		return s, e
	}
	for _, addr := range addrs {
		ip, _, e := net.ParseCIDR(addr.String())
		if e == nil && ip.To4() != nil && !ip.IsLinkLocalUnicast() {
			if s.IPv4 != "" {
				return s, errors.New("ambiguous ECM IPv4")
			}
			s.IPv4 = ip.String()
		}
	}
	if s.IPv4 == "" {
		return s, NotReady{errors.New("ECM has no routable IPv4")}
	}
	route, e := exec.CommandContext(ctx, "/sbin/route", "-n", "get", "default").Output()
	if e != nil {
		return s, NotReady{e}
	}
	for _, line := range strings.Split(string(route), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 {
			switch fields[0] {
			case "gateway:":
				s.DefaultGateway = fields[1]
			case "interface:":
				s.DefaultInterface = fields[1]
			}
		}
	}
	if s.DefaultGateway == "" || s.DefaultInterface == "" {
		return s, NotReady{errors.New("default route unavailable")}
	}
	sip, e := exec.CommandContext(ctx, "/usr/bin/csrutil", "status").Output()
	if e != nil {
		return s, e
	}
	s.SIP = strings.TrimSpace(string(sip))
	if !strings.Contains(s.SIP, "enabled") || strings.Contains(s.SIP, "disabled") {
		return s, errors.New("SIP must remain enabled")
	}
	dialer := net.Dialer{Timeout: 5 * time.Second, LocalAddr: &net.TCPAddr{IP: net.ParseIP(s.IPv4)}, Control: func(network, address string, c syscall.RawConn) error {
		var inner error
		e := c.Control(func(fd uintptr) { inner = syscall.SetsockoptInt(int(fd), syscall.IPPROTO_IP, 25, nic.Index) })
		return errors.Join(e, inner)
	}}
	transport := &http.Transport{Proxy: nil, DialContext: func(c context.Context, network, address string) (net.Conn, error) {
		return dialer.DialContext(c, "tcp4", address)
	}, DisableKeepAlives: true, TLSHandshakeTimeout: 5 * time.Second, MaxResponseHeaderBytes: 8192}
	defer transport.CloseIdleConnections()
	client := http.Client{Transport: transport, Timeout: 12 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	req, e := http.NewRequestWithContext(ctx, "GET", "https://www.apple.com/library/test/success.html", nil)
	if e != nil {
		return s, e
	}
	response, e := client.Do(req)
	if e != nil {
		return s, NotReady{fmt.Errorf("ECM HTTPS verification: %w", e)}
	}
	defer response.Body.Close()
	body, e := io.ReadAll(io.LimitReader(response.Body, 16385))
	if e != nil {
		return s, e
	}
	if response.StatusCode != 200 || len(body) > 16384 || !strings.Contains(string(body), "Success") || response.TLS == nil || len(response.TLS.VerifiedChains) == 0 {
		return s, errors.New("ECM HTTPS response failed validation")
	}
	s.HTTPS = true
	return s, nil
}
func Compare(before, after Snapshot) error {
	if e := discovery.Reconcile(before.Device, after.Device); e != nil {
		return e
	}
	a, b := before, after
	a.ObservedUTC = ""
	b.ObservedUTC = ""
	if !a.HTTPS || !b.HTTPS || !reflect.DeepEqual(a, b) {
		return errors.New("host network or SIP reconciliation differs")
	}
	return nil
}

// CompareDuring permits only IF2's owner to change while this process holds its
// non-seizing interface handle. The native layer pins the retained registry ID.
func CompareDuring(before, after Snapshot) error {
	if len(before.Device.Interfaces) != 6 || len(after.Device.Interfaces) != 6 {
		return errors.New("missing interface ownership")
	}
	copyInterfaces := append([]discovery.Interface(nil), after.Device.Interfaces...)
	if !strings.HasPrefix(copyInterfaces[2].Owner, fmt.Sprintf("pid %d,", os.Getpid())) {
		return errors.New("IF2 ownership missing during acquisition")
	}
	copyInterfaces[2].Owner = before.Device.Interfaces[2].Owner
	after.Device.Interfaces = copyInterfaces
	return Compare(before, after)
}
