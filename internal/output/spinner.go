package output

import (
	"fmt"
	"io"
	"sync"
	"time"
)

// spinnerFrames are braille characters chosen so that every frame is one column
// wide. Braille glyphs all occupy a single cell, so swapping frames cannot
// change the width of the line; box-drawing and geometric glyphs such as ▪ ◾ ◼
// ⬛ carry differing East Asian widths and would make the label wobble left and
// right on every tick.
var spinnerFrames = []rune("⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏")

// spinnerElapsedThreshold is the minimum a spinner may run before its line
// gains an elapsed-seconds suffix. The suffix is automatic: below the threshold
// the line stays byte-identical to the plain label, so a capture whose
// collectors finish in milliseconds never shows a number at all, while an agent
// run or an import that is still going after two seconds always will.
const spinnerElapsedThreshold = 2 * time.Second

// spinnerLine renders the text drawn after the animated frame. Under
// spinnerElapsedThreshold the label is returned untouched; at or above it a
// plain-seconds suffix is appended, so the whole line reads "claude · 47s".
// Seconds are reported as a bare count — 127s, 3661s, never "2m7s" — because
// every other shape wraps at a boundary, and a wrapped counter reads as a new,
// unrelated number to someone who looked away for a moment. Monotonic elapsed
// time is the one number that only ever grows.
func spinnerLine(label string, elapsed time.Duration) string {
	if elapsed < spinnerElapsedThreshold {
		return label
	}
	return fmt.Sprintf("%s · %ds", label, int(elapsed/time.Second))
}

// Spinner animates a progress frame while a slow operation runs, and erases the
// line when stopped so the caller's next write lands on a clean line.
//
// Whether it animates at all is decided once, in NewSpinner, by the same
// per-writer rule NewStyle uses for colour. A non-terminal writer — a CI log, a
// pipe, a file, a test buffer — makes every method a no-op that writes nothing:
// an animation that leaked escape sequences into a log would be worse than the
// silence it exists to remove.
type Spinner struct {
	w       io.Writer
	style   Style
	tick    time.Duration
	mu      sync.Mutex
	stop    chan struct{}
	wg      sync.WaitGroup
	running bool
}

// NewSpinner returns a Spinner writing to w. The decision whether to animate is
// made once here, and it is exactly NewStyle's: NO_COLOR, TERM=dumb, a
// non-terminal writer, and the legacy Windows console all come out of that one
// check disabled, so the same bytes reach a terminal and a file and only the
// terminal sees a spinner.
func NewSpinner(w io.Writer) *Spinner {
	return &Spinner{w: w, style: NewStyle(w), tick: 100 * time.Millisecond}
}

// Start begins animating with the given label. A Spinner already running is
// stopped first, so a new label always replaces the previous one cleanly. When
// the writer is not animated, Start does nothing.
func (s *Spinner) Start(label string) {
	if !s.style.enabled {
		return
	}
	s.Stop()
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return
	}
	s.running = true
	s.stop = make(chan struct{})
	stop := s.stop
	s.mu.Unlock()

	s.wg.Add(1)
	go s.run(label, stop)
}

func (s *Spinner) run(label string, stop chan struct{}) {
	defer s.wg.Done()
	ticker := time.NewTicker(s.tick)
	defer ticker.Stop()
	// The first frame is drawn before waiting on the ticker. Drawing only on a
	// tick meant a collector that finished inside the first interval never drew
	// anything at all, and on a warm machine nearly every collector does: a
	// whole capture produced one frame, so the spinner was invisible exactly
	// where it was supposed to say "this is running".
	//
	// Erasing is what makes this safe to do for fast collectors too. A label
	// that appears and is erased within milliseconds reads as the line moving
	// through the collectors, not as a flicker.
	start := time.Now()
	for i := 0; ; i++ {
		frame := string(spinnerFrames[i%len(spinnerFrames)])
		_, _ = io.WriteString(s.w, "\r"+s.style.Accent(frame)+" "+spinnerLine(label, time.Since(start)))
		select {
		case <-stop:
			return
		case <-ticker.C:
		}
	}
}

// Stop halts the animation, waits for the ticker goroutine to actually exit,
// and erases the line with "\r\x1b[K" so the caller's next write lands on a
// clean line. It is safe to call more than once, and safe to call before Start.
func (s *Spinner) Stop() {
	s.mu.Lock()
	if !s.running {
		s.mu.Unlock()
		return
	}
	s.running = false
	close(s.stop)
	s.mu.Unlock()
	s.wg.Wait()
	if s.style.enabled {
		_, _ = io.WriteString(s.w, "\r\x1b[K")
	}
}
