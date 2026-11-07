package rc

import (
	"context"
	"errors"
	"fmt"
	"github.com/mattn/go-runewidth"
	"github.com/qjfoidnh/BaiduPCS-Go/pcsutil/converter"
	"math"
	"sort"
	"strings"
	"time"
)

// ErrProgressCallback identifies a failure rendering overall transfer progress.
var ErrProgressCallback = errors.New("transfer progress callback failed")

func runProgressCallback(o Options, snapshot ProgressSnapshot) (err error) {
	defer func() {
		if r := recover(); r != nil {
			cause, ok := r.(error)
			if !ok {
				cause = fmt.Errorf("%v", r)
			}
			err = fmt.Errorf("%w: %w", ErrProgressCallback, cause)
		}
	}()
	if o.Progress != nil {
		o.Progress(snapshot)
	} else {
		if err := logAction(o, "progress", snapshot.String()); err != nil {
			return fmt.Errorf("%w: %w", ErrProgressCallback, err)
		}
	}
	return nil
}

type transferSettingsKey struct{}
type progressKey struct{}
type workerSettings struct {
	Threads  int    `json:"threads"`
	ETag     string `json:"etag,omitempty"`
	Modified string `json:"modified,omitempty"`
}
type transferProgress struct {
	Done, Total, Rate int64
	ETag, Modified    string
	Reset             bool
	CheckpointFailed  bool
	ProtocolFailed    bool
}

func reportProgress(ctx context.Context, v transferProgress) {
	if f, ok := ctx.Value(progressKey{}).(func(transferProgress)); ok {
		f(v)
	}
}
func downloadThreads(o Options) int {
	threads, budget, transfers := o.DownloadThreads, o.MaxConnections, o.Transfers
	if threads < 1 {
		threads = 4
	}
	if budget < 1 {
		budget = 16
	}
	if transfers < 1 {
		transfers = 1
	}
	return max(1, min(threads, budget/transfers))
}
func (r Result) String() string {
	return fmt.Sprintf("copied=%d skipped=%d deleted=%d failed=%d canceled=%d pending=%d bytes=%d", r.Copied, r.Skipped, r.Deleted, r.Failed, r.Canceled, r.Pending, r.Bytes)
}

type FileProgress struct {
	Path              string
	Done, Total, Rate int64
}
type ProgressSnapshot struct {
	Files                            []FileProgress
	Done, Total, Rate                int64
	Completed, Count, Active, Failed int
	Canceled, Pending                int
	Final                            bool
	Elapsed, ETA                     time.Duration
}

func humanBytes(n int64) string {
	units := []string{"B", "KiB", "MiB", "GiB", "TiB"}
	v := float64(n)
	i := 0
	for v >= 1024 && i < len(units)-1 {
		v /= 1024
		i++
	}
	return fmt.Sprintf("%.1f %s", v, units[i])
}
func (p ProgressSnapshot) percentage() float64 {
	if p.Total > 0 {
		return 100 * float64(p.Done) / float64(p.Total)
	}
	if p.Count > 0 {
		return 100 * float64(p.Completed) / float64(p.Count)
	}
	if p.Final && p.Active == 0 && p.Pending == 0 && p.Failed == 0 && p.Canceled == 0 {
		return 100
	}
	return 0
}
func (p ProgressSnapshot) String() string {
	percent := p.percentage()
	eta := "--"
	if p.Rate > 0 && p.ETA >= 0 {
		eta = p.ETA.Round(time.Second).String()
	}
	return fmt.Sprintf("%.1f%% | %s / %s | %s/s | elapsed %s | ETA %s | files %d/%d | active=%d failed=%d canceled=%d pending=%d", percent, humanBytes(p.Done), humanBytes(p.Total), humanBytes(p.Rate), p.Elapsed.Round(time.Second), eta, p.Completed, p.Count, p.Active, p.Failed, p.Canceled, p.Pending)
}
func (p ProgressSnapshot) Lines() []string {
	percent := p.percentage()
	eta := "--"
	if p.Rate > 0 && p.ETA >= 0 {
		eta = p.ETA.Round(time.Second).String()
	}
	lines := []string{fmt.Sprintf("%.1f%% | %s / %s | %s/s | ETA %s", percent, humanBytes(p.Done), humanBytes(p.Total), humanBytes(p.Rate), eta), fmt.Sprintf("files %d/%d | active %d | failed %d | elapsed %s", p.Completed, p.Count, p.Active, p.Failed, p.Elapsed.Round(time.Second))}
	if p.Canceled > 0 || p.Pending > 0 {
		lines = append(lines, fmt.Sprintf("canceled %d | pending %d", p.Canceled, p.Pending))
	}
	for _, f := range p.Files {
		name := strings.Map(func(r rune) rune {
			if r < 32 || r == 127 {
				return ' '
			}
			return r
		}, f.Path)
		name = runewidth.Truncate(name, 44, "...")
		percent := float64(0)
		if f.Total > 0 {
			percent = 100 * float64(f.Done) / float64(f.Total)
		}
		lines = append(lines, fmt.Sprintf("%5.1f%% | %s/s | %s", percent, humanBytes(f.Rate), name))
	}
	for i := range lines {
		lines[i] = runewidth.Truncate(lines[i], 78, "...")
	}
	return lines
}

type progressState struct {
	started    time.Time
	total      int64
	totalFiles int
	finished   int64
	files      map[string]FileProgress
	updated    map[string]time.Time
}

func newProgress(p *Plan) *progressState {
	s := &progressState{started: time.Now(), files: map[string]FileProgress{}, updated: map[string]time.Time{}}
	for _, a := range p.Actions {
		if a.Kind == "copy" || a.Kind == "skip" {
			s.totalFiles++
			if a.Source != nil {
				s.total += a.Source.Size
			}
		}
	}
	return s
}
func (s *progressState) start(a Action) {
	size := int64(0)
	if a.Source != nil {
		size = a.Source.Size
	}
	s.files[a.Path] = FileProgress{Path: a.Path, Total: size}
}
func (s *progressState) update(path string, v transferProgress) {
	f, ok := s.files[path]
	if !ok {
		return
	}
	f.Done = max(0, min(f.Total, v.Done))
	f.Rate = max(0, v.Rate)
	s.files[path] = f
	s.updated[path] = time.Now()
}
func (s *progressState) finish(a Action, success bool) {
	if success && a.Source != nil {
		s.finished += a.Source.Size
	} else {
		// Retain observed partial bytes without counting an interrupted file as
		// copied or promising that its checkpoint was saved successfully.
		s.finished += s.files[a.Path].Done
	}
	delete(s.files, a.Path)
	delete(s.updated, a.Path)
}
func (s *progressState) snapshot(r Result, active int) ProgressSnapshot {
	p := ProgressSnapshot{Done: s.finished, Total: s.total, Count: s.totalFiles, Completed: r.Copied + r.Skipped, Failed: r.Failed, Canceled: r.Canceled, Active: active, Elapsed: time.Since(s.started)}
	p.Pending = max(0, p.Count-p.Completed-p.Failed-p.Canceled-active)
	for path, f := range s.files {
		if time.Since(s.updated[path]) > 5*time.Second {
			f.Rate = 0
		}
		p.Done += f.Done
		// Each worker rate fits int64, but their combined rate may not.
		p.Rate += min(f.Rate, math.MaxInt64-p.Rate)
		p.Files = append(p.Files, f)
	}
	sort.Slice(p.Files, func(i, j int) bool { return p.Files[i].Path < p.Files[j].Path })
	p.ETA = converter.RemainingTime(p.Total, p.Done, p.Rate)
	return p
}
