package chat

import (
	"archive/zip"
	"bytes"
	"strings"
	"testing"
)

// buildSkillZip builds an in-memory zip for a skill. When subdir=true the files
// live under "<name>/", otherwise they sit at the archive root (flat layout).
func buildSkillZip(t *testing.T, name string, subdir bool) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	entries := map[string]string{
		"SKILL.md":          "---\nname: " + name + "\n---\n",
		"analyze.py":        "print('hi')\n",
		"sub/nested.txt":    "nested\n",
	}
	for rel, content := range entries {
		arc := rel
		if subdir {
			arc = name + "/" + rel
		}
		if err := func() error {
			f, err := w.Create(arc)
			if err != nil {
				return err
			}
			_, err = f.Write([]byte(content))
			return err
		}(); err != nil {
			t.Fatalf("create zip entry %q: %v", arc, err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close zip writer: %v", err)
	}
	return buf.Bytes()
}

func TestNormalizePlazaSkillZip_SubdirLayout(t *testing.T) {
	raw := buildSkillZip(t, "ai-index-analysis", true)
	out, err := normalizePlazaSkillZip("ai-index-analysis", raw)
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	verifySingleTopDir(t, out, "ai-index-analysis")
}

func TestNormalizePlazaSkillZip_FlatLayout(t *testing.T) {
	raw := buildSkillZip(t, "disk-space-check", false)
	out, err := normalizePlazaSkillZip("disk-space-check", raw)
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	verifySingleTopDir(t, out, "disk-space-check")
}

// verifySingleTopDir asserts every entry in the zip lives under "<skillName>/"
// and that SKILL.md is present at the expected location.
func verifySingleTopDir(t *testing.T, raw []byte, skillName string) {
	t.Helper()
	r, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		t.Fatalf("read normalized zip: %v", err)
	}
	prefix := skillName + "/"
	want := map[string]bool{
		prefix + "SKILL.md":       false,
		prefix + "analyze.py":     false,
		prefix + "sub/nested.txt": false,
	}
	for _, f := range r.File {
		if !strings.HasPrefix(f.Name, prefix) {
			t.Fatalf("entry %q not under %q", f.Name, prefix)
		}
		if _, ok := want[f.Name]; ok {
			want[f.Name] = true
		}
	}
	for name, seen := range want {
		if !seen {
			t.Fatalf("expected entry %q missing in normalized zip", name)
		}
	}
}
