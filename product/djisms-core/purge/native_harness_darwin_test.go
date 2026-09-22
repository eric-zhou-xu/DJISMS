//go:build darwin

package purge

import (
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestNativeIOKitMockHarness(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "native-offline")
	fixture := filepath.Join(dir, "descriptor.bin")
	raw, err := hex.DecodeString(reviewedHex)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(fixture, raw, 0600); err != nil {
		t.Fatal(err)
	}
	// No IOKit linkage: all service functions and COM methods are offline mocks.
	cmd := exec.Command("/usr/bin/clang", "-Wall", "-Wextra", "-Werror", "-Wno-unused-variable", "testdata/native_offline.c", "-framework", "CoreFoundation", "-o", bin)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("mock build: %v\n%s", err, out)
	}
	if out, err := exec.Command("/usr/bin/otool", "-L", bin).CombinedOutput(); err != nil || strings.Contains(string(out), "IOKit.framework") {
		t.Fatalf("mock must not link IOKit: %v %s", err, out)
	}
	if out, err := exec.Command(bin, fixture).CombinedOutput(); err != nil {
		t.Fatalf("mock: %v\n%s", err, out)
	} else {
		t.Log(string(out))
	}
}
