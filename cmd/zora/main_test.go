package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestVersionCommandPrintsBuildMetadata(t *testing.T) {
	oldVersion, oldCommit, oldDate := version, commit, date
	version, commit, date = "1.2.3", "abc123", "2026-04-26T12:00:00Z"
	defer func() {
		version, commit, date = oldVersion, oldCommit, oldDate
	}()

	var stdout, stderr bytes.Buffer
	code := run([]string{"version"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run version code = %d stderr = %q", code, stderr.String())
	}

	got := stdout.String()
	for _, want := range []string{"zora 1.2.3", "commit: abc123", "built: 2026-04-26T12:00:00Z"} {
		if !strings.Contains(got, want) {
			t.Fatalf("version output %q missing %q", got, want)
		}
	}
}
