package digitaltwin

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"
	"time"
)

func TestLoadSkillGeneratorConfigUsesOrangeBaseURL(t *testing.T) {
	t.Setenv(EnvGeneratorURL, "http://orange.test")
	t.Setenv(EnvSkillGeneratorURL, "")
	t.Setenv(EnvOrangeTwinToken, "orange-token")

	cfg := LoadSkillGeneratorConfigFromEnv()
	if cfg.URL != "http://orange.test/api/v1/digital-twin/skills/generate" {
		t.Fatalf("unexpected url %q", cfg.URL)
	}
	if cfg.Token != "orange-token" {
		t.Fatalf("unexpected token %q", cfg.Token)
	}
}

func TestNormalizeSkillGeneratorURLAcceptsReplyEndpoint(t *testing.T) {
	got := normalizeSkillGeneratorURL("http://orange.test/api/v1/digital-twin/reply")
	if got != "http://orange.test/api/v1/digital-twin/skills/generate" {
		t.Fatalf("unexpected normalized url %q", got)
	}
}

func TestNormalizeSkillName(t *testing.T) {
	if got := NormalizeSkillName(" Pome 技能 "); got != "pome" {
		t.Fatalf("unexpected skill name %q", got)
	}
	if got := NormalizeSkillName("write poem_1"); got != "write-poem-1" {
		t.Fatalf("unexpected skill name %q", got)
	}
	if got := NormalizeSkillName("../"); got != "" {
		t.Fatalf("unexpected unsafe skill name %q", got)
	}
}

func TestCallHTTPSkillGenerator(t *testing.T) {
	var got SkillGeneratorRequest
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if auth := r.Header.Get("Authorization"); auth != "Bearer test-token" {
			t.Fatalf("unexpected authorization header %q", auth)
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatal(err)
		}
		body, _ := json.Marshal(SkillGeneratorResponse{
			OwnerUserID:  "user_b",
			SkillName:    "pome",
			SkillPath:    "/tmp/skills/pome/SKILL.md",
			SkillContent: "---\nname: pome\n---\n",
			Source:       "orange_dispatcher",
		})
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(bytes.NewReader(body)),
			Header:     make(http.Header),
		}, nil
	})}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	resp, err := CallHTTPSkillGenerator(
		ctx,
		client,
		GeneratorConfig{URL: "http://orange.test/api/v1/digital-twin/skills/generate", Token: "test-token", Timeout: time.Second},
		SkillGeneratorRequest{
			OwnerUserID: "user_b",
			SkillName:   "pome",
			Description: "作诗",
			OperationID: "op",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if got.OwnerUserID != "user_b" || got.SkillName != "pome" || got.Description != "作诗" || got.OperationID != "op" {
		t.Fatalf("unexpected request %+v", got)
	}
	if resp.SkillPath != "/tmp/skills/pome/SKILL.md" || resp.Source != "orange_dispatcher" {
		t.Fatalf("unexpected response %+v", resp)
	}
}
