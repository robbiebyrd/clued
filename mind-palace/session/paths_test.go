package session

import "testing"

func TestDecodeProjectPathConvertsEncodedDirName(t *testing.T) {
	if got := DecodeProjectPath("-Users-alice-Projects-myapp"); got != "/Users/alice/Projects/myapp" {
		t.Fatalf("DecodeProjectPath = %s", got)
	}
}

func TestDecodeProjectPathIsAbsoluteWithHyphenatedComponents(t *testing.T) {
	if got := DecodeProjectPath("-Users-rob-byrd-Projects-foo"); got == "" || got[0] != '/' {
		t.Fatalf("DecodeProjectPath = %q, want absolute path", got)
	}
}

func TestEncodeProjectPath(t *testing.T) {
	if got := EncodeProjectPath("/Users/alice/Projects/myapp"); got != "-Users-alice-Projects-myapp" {
		t.Fatalf("EncodeProjectPath = %s", got)
	}
}

func TestProjectPathRoundTrip(t *testing.T) {
	const p = "/Users/alice/Projects/myapp"
	if got := DecodeProjectPath(EncodeProjectPath(p)); got != p {
		t.Fatalf("round trip = %s", got)
	}
}
