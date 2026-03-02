package claude

import (
	"context"
	"testing"
)

func TestGetSessionMessages_EmptyID(t *testing.T) {
	_, err := GetSessionMessages(context.Background(), "")
	if err == nil {
		t.Fatal("expected error for empty session ID")
	}
	if err.Error() != "claude: sessions get: sessionID must not be empty" {
		t.Fatalf("unexpected error message: %s", err.Error())
	}
}
