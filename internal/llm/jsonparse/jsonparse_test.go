package jsonparse

import (
	"reflect"
	"testing"
)

func TestUnmarshalLenientPlainJSON(t *testing.T) {
	var got map[string]string
	if err := UnmarshalLenient([]byte(`{"a":"b"}`), &got); err != nil {
		t.Fatalf("UnmarshalLenient: %v", err)
	}
	if !reflect.DeepEqual(got, map[string]string{"a": "b"}) {
		t.Fatalf("got = %#v", got)
	}
}

func TestUnmarshalLenientStripsJSONFence(t *testing.T) {
	payload := "```json\n{\"a\":\"b\"}\n```"
	var got map[string]string
	if err := UnmarshalLenient([]byte(payload), &got); err != nil {
		t.Fatalf("UnmarshalLenient: %v", err)
	}
	if got["a"] != "b" {
		t.Fatalf("got = %#v", got)
	}
}

func TestUnmarshalLenientStripsBareFence(t *testing.T) {
	payload := "```\n{\"x\":1}\n```\n"
	var got map[string]int
	if err := UnmarshalLenient([]byte(payload), &got); err != nil {
		t.Fatalf("UnmarshalLenient: %v", err)
	}
	if got["x"] != 1 {
		t.Fatalf("got = %#v", got)
	}
}

func TestUnmarshalLenientHandlesLeadingWhitespace(t *testing.T) {
	payload := "   \n```json\n{\"ok\":true}\n```\n   "
	var got map[string]bool
	if err := UnmarshalLenient([]byte(payload), &got); err != nil {
		t.Fatalf("UnmarshalLenient: %v", err)
	}
	if !got["ok"] {
		t.Fatalf("got = %#v", got)
	}
}

func TestUnmarshalLenientErrorsOnEmpty(t *testing.T) {
	var got map[string]string
	if err := UnmarshalLenient([]byte("```\n```"), &got); err == nil {
		t.Fatalf("expected error on empty payload")
	}
}
