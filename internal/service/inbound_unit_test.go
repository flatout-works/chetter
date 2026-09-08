package service

import (
	"errors"
	"strings"
	"testing"
)

func errString(message string) error {
	return errors.New(message)
}

func TestInboundDeliveryTaskIDDeterministic(t *testing.T) {
	a := inboundDeliveryTaskID("ibd_1")
	b := inboundDeliveryTaskID("ibd_1")
	c := inboundDeliveryTaskID("ibd_2")
	if a != b {
		t.Fatalf("deterministic task id must be stable across calls: %q vs %q", a, b)
	}
	if a == c {
		t.Fatalf("distinct delivery rows must map to distinct task ids")
	}
	if !strings.HasPrefix(a, "task_") || len(a) != 37 {
		t.Fatalf("unexpected task id shape %q", a)
	}
}

func TestRenderInboundPrompt(t *testing.T) {
	endpoint := &inboundEndpointRow{
		Name:         "ci-build-events",
		ActionPrompt: "Investigate {{ .EventType }} for {{ .Payload.repository }} build {{ .Payload.build_number }} from {{ .SourceIP }}",
	}
	delivery := inboundDeliveryRow{
		DeliveryID: "dvr-9",
		EventType:  "build.completed",
		SourceIP:   "203.0.113.9",
		Payload:    `{"repository":"acme/web","build_number":42}`,
	}
	out, err := renderInboundPrompt(endpoint, delivery)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	want := "Investigate build.completed for acme/web build 42 from 203.0.113.9"
	if out != want {
		t.Fatalf("rendered %q, want %q", out, want)
	}
}

func TestRenderInboundPromptPayloadRawAndMissingField(t *testing.T) {
	endpoint := &inboundEndpointRow{
		ActionPrompt: "raw={{ .PayloadRaw }} missing=[{{ .Payload.nope }}]",
	}
	delivery := inboundDeliveryRow{
		Payload: `{"a":1}`,
	}
	out, err := renderInboundPrompt(endpoint, delivery)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if !strings.Contains(out, "raw={\"a\":1}") {
		t.Fatalf("PayloadRaw not interpolated: %q", out)
	}
	if !strings.Contains(out, "missing=[<no value>]") {
		t.Fatalf("missing payload key should render as <no value>: %q", out)
	}
}

func TestRenderInboundPromptNonObjectPayload(t *testing.T) {
	endpoint := &inboundEndpointRow{
		ActionPrompt: "value={{ .Payload }}",
	}
	delivery := inboundDeliveryRow{
		Payload: `"plain-string"`,
	}
	out, err := renderInboundPrompt(endpoint, delivery)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if !strings.Contains(out, "plain-string") {
		t.Fatalf("non-object payload not rendered: %q", out)
	}
}

func TestRenderInboundPromptTemplateError(t *testing.T) {
	endpoint := &inboundEndpointRow{ActionPrompt: "{{"}
	delivery := inboundDeliveryRow{Payload: `{}`}
	if _, err := renderInboundPrompt(endpoint, delivery); err == nil {
		t.Fatal("unparseable template should error")
	}
}

func TestIsInboundPermanentTaskError(t *testing.T) {
	permanent := []string{
		"agent \"ghost\" not found: no such agent",
		"team \"nope\" not found",
		"agent_image is required (no default configured)",
		"prompt is required",
		"invalid harness",
	}
	transient := []string{
		"dial tcp 10.0.0.1:3306: connect: connection refused",
		"context deadline exceeded",
		"database not available",
		"Error 1205: Lock wait timeout exceeded",
	}
	for _, message := range permanent {
		if !isInboundPermanentTaskError(errString(message)) {
			t.Errorf("%q should be classified permanent", message)
		}
	}
	for _, message := range transient {
		if isInboundPermanentTaskError(errString(message)) {
			t.Errorf("%q should be classified transient", message)
		}
	}
	if isInboundPermanentTaskError(nil) {
		t.Error("nil error should not be permanent")
	}
}
