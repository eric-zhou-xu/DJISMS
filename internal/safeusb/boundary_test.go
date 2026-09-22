package safeusb

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// This is an explicit safety boundary test, not a functional USB test. Expanding
// the small set of OS operations requires a deliberate test + source review.
func TestNativeDescriptorReaderHasNoWriteOrOpenOperations(t *testing.T) {
	raw, err := os.ReadFile("read_darwin.go")
	if err != nil {
		t.Fatal(err)
	}
	source := string(raw)
	source = regexp.MustCompile(`(?m)//[^\n]*`).ReplaceAllString(source, "")
	if strings.Contains(source, "libusb") {
		t.Fatal("libusb enumeration is not passive on Darwin")
	}
	for _, word := range []string{"USBDeviceOpen", "USBInterfaceOpen", "DeviceRequest", "WritePipe", "ReadPipe", "SetConfiguration", "SetAlternateInterface", "ResetDevice", "USBDeviceSuspend", "IOConnectCall", "dlopen", "dlsym"} {
		if strings.Contains(source, word) {
			t.Fatalf("forbidden native operation %s", word)
		}
	}
	allowed := map[string]bool{"QueryInterface": true, "Release": true, "GetConfigurationDescriptorPtr": true}
	for _, call := range regexp.MustCompile(`->([A-Z][A-Za-z0-9_]*)\s*\(`).FindAllStringSubmatch(source, -1) {
		if !allowed[call[1]] {
			t.Fatal("unreviewed COM operation", call[1])
		}
	}
}
