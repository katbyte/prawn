// The shared apply harness pieces: throttle, guards, and the flag-fed
// ApplyPass constructor.

package cli

import (
	"time"

	"github.com/katbyte/prawn/lib/pr"
)

// NewApplyPass wires the flag-level knobs shared by every check into the
// harness (lib/pr's ApplyPass); the caller fills the per-pass wording
// (Noun, GateLabel, ConfirmAll, ConfirmAI).
func (f *FlagData) NewApplyPass(m FlagsApplyModes, title func(int) string, closeOne pr.CloseFunc) *pr.ApplyPass {
	threshold := m.Threshold
	if threshold <= 0 {
		threshold = JudgeThreshold
	}
	return &pr.ApplyPass{
		RepoTag:   f.RepoTag(),
		DryRun:    f.DryRun,
		Yes:       f.Yes,
		Auto:      m.ApplyWithAIAuto,
		Max:       m.Max,
		Threshold: threshold,
		Title:     title,
		URL:       f.PRURL,
		ScoreTag:  ScoreTag,
		Close:     closeOne,
	}
}

// mutationThrottle keeps ~2s between GitHub mutations: friendly to secondary
// rate limits, and a runaway apply can be ^C'd before much damage.
const mutationThrottle = 2100 * time.Millisecond

// RESTStateOpen is the REST API's lowercase issue/PR state.
const RESTStateOpen = "open"

// NewThrottle returns a func that sleeps to keep at least mutationThrottle
// between calls (no sleep on the first call).
func NewThrottle() func() {
	var lastCall time.Time
	return func() {
		if !lastCall.IsZero() {
			if wait := mutationThrottle - time.Since(lastCall); wait > 0 {
				time.Sleep(wait)
			}
		}
		lastCall = time.Now()
	}
}
