package actionitems

import "testing"

func TestDecodeGeneratedResponseObject(t *testing.T) {
	got, err := decodeGeneratedResponse(`{"items":[{"category":"needs_action","title":"Hi"}]}`)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got.Items) != 1 || got.Items[0].Title != "Hi" {
		t.Fatalf("got = %#v", got)
	}
}

func TestDecodeGeneratedResponseBareArray(t *testing.T) {
	got, err := decodeGeneratedResponse(`[{"category":"needs_action","title":"Hi"}]`)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got.Items) != 1 || got.Items[0].Title != "Hi" {
		t.Fatalf("got = %#v", got)
	}
}

func TestDecodeGeneratedResponseFencedArray(t *testing.T) {
	payload := "```json\n[{\"category\":\"needs_action\",\"title\":\"Hi\"}]\n```"
	got, err := decodeGeneratedResponse(payload)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got.Items) != 1 || got.Items[0].Title != "Hi" {
		t.Fatalf("got = %#v", got)
	}
}

func TestDecodeGeneratedResponseAlternativeKey(t *testing.T) {
	got, err := decodeGeneratedResponse(`{"actions":[{"category":"needs_action","title":"Hi"}]}`)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got.Items) != 1 || got.Items[0].Title != "Hi" {
		t.Fatalf("got = %#v", got)
	}
}

func TestDecodeGeneratedResponseGarbageReturnsObjectError(t *testing.T) {
	if _, err := decodeGeneratedResponse(`not json`); err == nil {
		t.Fatalf("expected error")
	}
}
