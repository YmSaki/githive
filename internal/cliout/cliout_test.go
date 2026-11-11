package cliout

import (
	"bytes"
	"encoding/json"
	"testing"
)

func captureStdout(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev := stdout
	stdout = &buf
	t.Cleanup(func() { stdout = prev })
	return &buf
}

// TestPrintSuccessEncodeError: an unencodable payload must surface as an
// error (so RunE returns it and the process exits non-zero) and must not
// write a half-built or hand-rolled envelope (#126).
func TestPrintSuccessEncodeError(t *testing.T) {
	buf := captureStdout(t)
	err := PrintSuccess(map[string]any{"keys": []map[string]any{{"pub": "x"}}}, nil)
	if err == nil {
		t.Fatal("PrintSuccess with []map[string]any: want error, got nil")
	}
	if buf.Len() != 0 {
		t.Fatalf("nothing should be written on encode failure, got %q", buf.String())
	}
}

func TestPrintSuccessOK(t *testing.T) {
	buf := captureStdout(t)
	if err := PrintSuccess(map[string]any{"a": "b"}, nil); err != nil {
		t.Fatal(err)
	}
	var env map[string]any
	if err := json.Unmarshal(buf.Bytes(), &env); err != nil {
		t.Fatalf("bad json: %v\n%s", err, buf.String())
	}
	if env["ok"] != true || env["data"].(map[string]any)["a"] != "b" {
		t.Fatalf("unexpected envelope: %s", buf.String())
	}
}

// TestPrintFailureUnencodableDataFallsBack: a failure envelope whose data
// cannot be encoded still prints valid canonical JSON without data.
func TestPrintFailureUnencodableDataFallsBack(t *testing.T) {
	buf := captureStdout(t)
	PrintFailure(ErrorInfo{Code: "internal", Message: `bad "quote"`, Data: map[string]any{"x": []map[string]any{{}}}})
	var env map[string]any
	if err := json.Unmarshal(buf.Bytes(), &env); err != nil {
		t.Fatalf("failure envelope is not valid JSON: %v\n%s", err, buf.String())
	}
	errMap, _ := env["error"].(map[string]any)
	if env["ok"] != false || errMap["message"] != `bad "quote"` || errMap["data"] != nil {
		t.Fatalf("unexpected envelope: %s", buf.String())
	}
}
