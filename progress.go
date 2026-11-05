package main

import (
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"
)

// progress reports what a long run is doing, on stderr: one updating line on a
// terminal ("line"), a plain line every interval otherwise ("log"), or nothing
// but messages that matter ("quiet"). It is safe for concurrent use.
type progress struct {
	mode     string
	interval time.Duration
	w        io.Writer

	mu      sync.Mutex
	prefix  string
	phase   string
	total   int
	done    int
	extra   string
	started time.Time
	shown   time.Time
	open    bool // an updating line is on screen
}

// newProgress resolves "auto" to "line" on a terminal and "log" elsewhere.
func newProgress(mode string, intervalSec int, w io.Writer) *progress {
	if mode == "auto" {
		mode = "log"
		if f, ok := w.(*os.File); ok {
			if st, err := f.Stat(); err == nil && st.Mode()&os.ModeCharDevice != 0 {
				mode = "line"
			}
		}
	}
	return &progress{mode: mode, interval: time.Duration(intervalSec) * time.Second, w: w}
}

func quietProgress() *progress { return &progress{mode: "quiet", w: io.Discard} }

// SetPrefix names what the following phases belong to, e.g. "2/20 group-1-Name".
func (p *progress) SetPrefix(s string) {
	p.mu.Lock()
	p.prefix = s
	p.mu.Unlock()
}

// Logf prints a message line (not in quiet mode).
func (p *progress) Logf(format string, args ...any) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.mode == "quiet" {
		return
	}
	p.endLine()
	fmt.Fprintf(p.w, "%s: %s\n", p.prefix, fmt.Sprintf(format, args...))
}

// Warnf prints a message line in every mode, quiet included.
func (p *progress) Warnf(format string, args ...any) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.endLine()
	fmt.Fprintf(p.w, "%s: %s\n", p.prefix, fmt.Sprintf(format, args...))
}

// Phase starts counting towards total (0: unknown).
func (p *progress) Phase(name string, total int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.endLine()
	p.phase, p.total, p.done, p.extra, p.started = name, total, 0, "", time.Now()
	p.shown = time.Time{}
	p.show(false)
}

// Set updates the count of the current phase (and its total, if it became known).
func (p *progress) Set(done, total int, extra string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.done, p.extra = done, extra
	if total > 0 {
		p.total = total
	}
	p.show(false)
}

// Done shows the final state of the phase.
func (p *progress) Done() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.phase == "" {
		return
	}
	p.show(true)
	p.endLine()
	p.phase = ""
}

func (p *progress) show(final bool) {
	now := time.Now()
	switch p.mode {
	case "quiet":
		return
	case "line":
		if !final && now.Sub(p.shown) < 200*time.Millisecond {
			return
		}
		fmt.Fprintf(p.w, "\r\033[K%s", p.render(now))
		p.open = true
	default: // log
		if !final && !p.shown.IsZero() && now.Sub(p.shown) < p.interval {
			return
		}
		fmt.Fprintln(p.w, p.render(now))
	}
	p.shown = now
}

func (p *progress) render(now time.Time) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s: %s", p.prefix, p.phase)
	if p.total > 0 {
		fmt.Fprintf(&b, " %d/%d (%d%%)", p.done, p.total, 100*p.done/p.total)
	} else if p.done > 0 {
		fmt.Fprintf(&b, " %d", p.done)
	}
	if p.extra != "" {
		fmt.Fprintf(&b, ", %s", p.extra)
	}
	el := now.Sub(p.started)
	fmt.Fprintf(&b, ", %s", el.Round(time.Second))
	if p.total > 0 && p.done > 0 && p.done < p.total && el > 3*time.Second {
		left := time.Duration(float64(el) / float64(p.done) * float64(p.total-p.done))
		fmt.Fprintf(&b, ", about %s left", left.Round(time.Second))
	}
	return b.String()
}

func (p *progress) endLine() {
	if p.open {
		fmt.Fprintln(p.w)
		p.open = false
	}
}
