package mcp

import (
	"errors"
	"testing"

	"github.com/leolin310148/borz/internal/protocol"
)

func TestFormatEvalRawBranches(t *testing.T) {
	if res := formatEvalRaw(&protocol.Response{}); len(res.Content) == 0 {
		t.Fatal("empty raw result should still return text content")
	}
	if res := formatEvalRaw(&protocol.Response{Data: &protocol.ResponseData{Result: "plain"}}); len(res.Content) == 0 {
		t.Fatal("string raw result should return text content")
	}
	if res := formatEvalRaw(&protocol.Response{Data: &protocol.ResponseData{Result: map[string]any{"ok": true}}}); len(res.Content) == 0 {
		t.Fatal("object raw result should return text content")
	}
}

func TestAdditionalResponseFormatterBranches(t *testing.T) {
	if res := checkError(nil, errors.New("boom")); len(res.Content) == 0 {
		t.Fatal("checkError err should return content")
	}
	if res := checkError(&protocol.Response{Success: false, Error: "bad"}, nil); len(res.Content) == 0 {
		t.Fatal("checkError response failure should return content")
	}
	if res := textResult(&protocol.Response{Data: &protocol.ResponseData{URL: "https://example.test", Title: "Title"}}, "Done"); len(res.Content) == 0 {
		t.Fatal("textResult should include content")
	}
	if res := formatScreenshot(&protocol.Response{}); len(res.Content) == 0 {
		t.Fatal("empty screenshot should return text")
	}
	if res := formatScreenshot(&protocol.Response{Data: &protocol.ResponseData{DataURL: "data:image/jpeg;base64,abc"}}); len(res.Content) == 0 {
		t.Fatal("jpeg screenshot should return image content")
	}
	if res := formatTabList(&protocol.Response{}); len(res.Content) == 0 {
		t.Fatal("empty tab list should return text")
	}
	if res := formatEval(&protocol.Response{}); len(res.Content) == 0 {
		t.Fatal("nil eval should return text")
	}
	if res := formatEval(&protocol.Response{Data: &protocol.ResponseData{}}); len(res.Content) == 0 {
		t.Fatal("undefined eval should return text")
	}
}
