package decrypt

import "testing"

func TestDecryptAgeRejectsEmptyInput(t *testing.T) {
	if _, err := DecryptAge(nil, "x.yaml", []byte("key")); err == nil {
		t.Fatal("expected error for empty content")
	}
	if _, err := DecryptAge([]byte("ciphertext"), "x.yaml", nil); err == nil {
		t.Fatal("expected error for empty key")
	}
}

func TestFormatFromPath(t *testing.T) {
	cases := map[string]string{
		"foo.yaml":   "yaml",
		"foo.yml":    "yaml",
		"foo.json":   "json",
		"foo.env":    "dotenv",
		"foo.ini":    "dotenv",
		"foo":        "binary",
		"foo.txt":    "binary",
		"FOO.YAML":   "yaml",
		"path/a.env": "dotenv",
	}
	for path, want := range cases {
		if got := FormatFromPath(path); got != want {
			t.Errorf("FormatFromPath(%q) = %q, want %q", path, got, want)
		}
	}
}
