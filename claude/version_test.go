package claude

import "testing"

func TestSDKVersion(t *testing.T) {
	if SDKVersion != "0.3.0" {
		t.Fatalf("expected SDK version 0.3.0, got %s", SDKVersion)
	}
}
