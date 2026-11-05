package main

import (
	"strings"
	"testing"
)

func TestProgressLogMode(t *testing.T) {
	var b strings.Builder
	p := newProgress("log", 30, &b)
	p.SetPrefix("1/2 group-1-Test")
	p.Phase("files", 100)
	for i := 1; i <= 100; i++ {
		p.Set(i, 0, "x linked")
	}
	p.Done()
	lines := strings.Split(strings.TrimSpace(b.String()), "\n")
	if len(lines) != 2 { // the start and the end; the rest falls within the interval
		t.Fatalf("lines:\n%s", b.String())
	}
	if !strings.HasPrefix(lines[1], "1/2 group-1-Test: files 100/100 (100%), x linked, ") || strings.Contains(b.String(), "\033") {
		t.Errorf("last line %q", lines[1])
	}
}

func TestProgressLineMode(t *testing.T) {
	var b strings.Builder
	p := newProgress("line", 30, &b)
	p.Phase("items", 0)
	p.Set(300, 6975, "")
	p.Logf("a message")
	if !strings.Contains(b.String(), "\r\033[K") || !strings.Contains(b.String(), "\n: a message\n") {
		t.Errorf("output %q", b.String())
	}
}

func TestProgressQuiet(t *testing.T) {
	var b strings.Builder
	p := newProgress("quiet", 30, &b)
	p.Phase("files", 10)
	p.Set(5, 0, "")
	p.Logf("not shown")
	p.Done()
	p.Warnf("shown")
	if b.String() != ": shown\n" {
		t.Errorf("output %q", b.String())
	}
}

func TestProgressAutoIsLogOffTerminal(t *testing.T) {
	if p := newProgress("auto", 30, &strings.Builder{}); p.mode != "log" {
		t.Errorf("mode %q", p.mode)
	}
}
