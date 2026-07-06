package digitaltwin

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/openimsdk/chat/pkg/common/imwebhook"
)

func TestCallHTTPGenerator(t *testing.T) {
	var got GeneratorRequest
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if auth := r.Header.Get("Authorization"); auth != "Bearer test-token" {
			t.Fatalf("unexpected authorization header %q", auth)
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatal(err)
		}
		body, _ := json.Marshal(GeneratorResponse{ReplyText: "generated reply"})
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(bytes.NewReader(body)),
			Header:     make(http.Header),
		}, nil
	})}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	reply, err := CallHTTPGenerator(
		ctx,
		client,
		GeneratorConfig{URL: "http://digital-twin-generator.test/reply", Token: "test-token", Timeout: time.Second},
		imwebhook.CallbackAfterSendSingleMsgReq{
			CommonCallbackReq: imwebhook.CommonCallbackReq{
				SendID:      "user_a",
				Content:     `{"content":"hello"}`,
				ServerMsgID: "server_msg",
				ClientMsgID: "client_msg",
				OperationID: "op",
			},
			RecvID: "user_b",
		},
		Config{ReplyText: "fallback", Prompt: "be concise"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if reply != "generated reply" {
		t.Fatalf("unexpected reply %q", reply)
	}
	if got.OwnerUserID != "user_b" || got.SenderUserID != "user_a" || got.MessageContent != "hello" || got.FallbackReplyText != "fallback" || got.Prompt != "be concise" {
		t.Fatalf("unexpected generator request %+v", got)
	}
}

func TestLoadGeneratorConfigFallsBackToOrangeToken(t *testing.T) {
	t.Setenv(EnvGeneratorURL, "http://orange.test")
	t.Setenv(EnvGeneratorToken, "")
	t.Setenv(EnvOrangeTwinToken, "orange-token")

	cfg := LoadGeneratorConfigFromEnv()
	if cfg.Token != "orange-token" {
		t.Fatalf("expected orange token fallback, got %q", cfg.Token)
	}
	if cfg.URL != "http://orange.test/api/v1/digital-twin/reply" {
		t.Fatalf("unexpected normalized url %q", cfg.URL)
	}
}

func TestCallHTTPGeneratorStatusErrorIncludesBody(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusUnauthorized,
			Body:       io.NopCloser(bytes.NewReader([]byte(`{"error":"unauthorized"}`))),
			Header:     make(http.Header),
		}, nil
	})}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, err := CallHTTPGeneratorResponse(
		ctx,
		client,
		GeneratorConfig{URL: "http://orange.test/api/v1/digital-twin/reply", Timeout: time.Second},
		imwebhook.CallbackAfterSendSingleMsgReq{},
		Config{},
	)
	if err == nil {
		t.Fatal("expected status error")
	}
	if got := err.Error(); got != `digital twin generator returned status 401: {"error":"unauthorized"}` {
		t.Fatalf("unexpected error %q", got)
	}
}

func TestCallHTTPGeneratorIgnoresParentCancellation(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		body, _ := json.Marshal(GeneratorResponse{ReplyText: "late generated reply"})
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(bytes.NewReader(body)),
			Header:     make(http.Header),
		}, nil
	})}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	resp, err := CallHTTPGeneratorResponse(
		ctx,
		client,
		GeneratorConfig{URL: "http://orange.test/api/v1/digital-twin/reply", Timeout: time.Second},
		imwebhook.CallbackAfterSendSingleMsgReq{},
		Config{},
	)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Text() != "late generated reply" {
		t.Fatalf("unexpected reply %q", resp.Text())
	}
}

func TestCallHTTPGeneratorAcceptsContentField(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		body, _ := json.Marshal(GeneratorResponse{Content: "content reply"})
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(bytes.NewReader(body)),
			Header:     make(http.Header),
		}, nil
	})}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	reply, err := CallHTTPGenerator(
		ctx,
		client,
		GeneratorConfig{URL: "http://digital-twin-generator.test/reply", Timeout: time.Second},
		imwebhook.CallbackAfterSendSingleMsgReq{},
		Config{},
	)
	if err != nil {
		t.Fatal(err)
	}
	if reply != "content reply" {
		t.Fatalf("unexpected reply %q", reply)
	}
}

func TestCallHTTPGeneratorResponseKeepsTraceMetadata(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		body, _ := json.Marshal(GeneratorResponse{
			ReplyText:      "generated reply",
			Source:         "orange_dispatcher",
			ProtocolSource: "openclaw_channel_tool",
			FinalizeSource: "openclaw_channel_tool",
			Metadata: map[string]any{
				"agentId":       "openim1",
				"workspacePath": "/tmp/sandboxes/openim1/openim/digital_twin/user_b",
			},
		})
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(bytes.NewReader(body)),
			Header:     make(http.Header),
		}, nil
	})}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	resp, err := CallHTTPGeneratorResponse(
		ctx,
		client,
		GeneratorConfig{URL: "http://digital-twin-generator.test/reply", Timeout: time.Second},
		imwebhook.CallbackAfterSendSingleMsgReq{},
		Config{},
	)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Text() != "generated reply" {
		t.Fatalf("unexpected reply %q", resp.Text())
	}
	trace := resp.Trace()
	if trace == nil || trace.ProtocolSource != "openclaw_channel_tool" || trace.FinalizeSource != "openclaw_channel_tool" {
		t.Fatalf("unexpected trace %+v", trace)
	}
	if trace.Metadata["agentId"] != "openim1" {
		t.Fatalf("unexpected metadata %+v", trace.Metadata)
	}
}

func TestExtractTextContentFallsBackToRawContent(t *testing.T) {
	if got := ExtractTextContent(`{"content":"hello"}`); got != "hello" {
		t.Fatalf("unexpected text %q", got)
	}
	if got := ExtractTextContent(`plain text`); got != "plain text" {
		t.Fatalf("unexpected raw text %q", got)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}
