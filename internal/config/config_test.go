package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultsAndSafety(t *testing.T) {
	c := Default()
	if e := c.Validate(); e != nil {
		t.Fatal(e)
	}
	c.PaperOnly = false
	if c.Validate() == nil {
		t.Fatal("live mode forbidden")
	}
	c = Default()
	c.LLMURL = "http://0.0.0.0:8080"
	if c.Validate() == nil {
		t.Fatal("nonloopback model")
	}
	c = Default()
	c.MaxLLMBatch = 200
	if c.Validate() == nil {
		t.Fatal("unbounded LLM")
	}
}
func TestLoad(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.json")
	if e := os.WriteFile(p, []byte(`{"min_turnover_24h":5000,"paper_only":true}`), 0600); e != nil {
		t.Fatal(e)
	}
	c, e := Load(p)
	if e != nil || c.MinTurnover24h != 5000 || c.MaxLLMBatch != 10 {
		t.Fatalf("%+v %v", c, e)
	}
	if e = os.WriteFile(p, []byte(`{"paper_only":false}`), 0600); e != nil {
		t.Fatal(e)
	}
	if _, e = Load(p); e == nil {
		t.Fatal("live config")
	}
}

func TestConfigRejectsUnknownAndTrailing(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.json")
	for _, bad := range []string{`{"paper_only":true,"bogus_value":10}`, `{"paper_only":true}{"extra":1}`} {
		if e := os.WriteFile(p, []byte(bad), 0600); e != nil {
			t.Fatal(e)
		}
		if _, e := Load(p); e == nil {
			t.Fatalf("accepted config: %s", bad)
		}
	}
}
