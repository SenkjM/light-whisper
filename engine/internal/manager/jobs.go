// SPDX-FileCopyrightText: 2026 SenkjM
// SPDX-License-Identifier: AGPL-3.0-only

package manager

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/SenkjM/light-whisper/engine/internal/asr"
	"github.com/SenkjM/light-whisper/engine/internal/events"
	"github.com/SenkjM/light-whisper/engine/internal/r2t2"
	"github.com/SenkjM/light-whisper/engine/internal/scheduler"
)

// File transcription jobs (PLAN §4.5, §4.6): the file is segmented by the
// VAD of the loaded engine, nearby speech regions are merged into units of
// at most MaxJobSegmentSamples, and each unit is recognized with the
// engine's whole-clip path. The job is one preemptible scheduler task that
// keeps its own progress: between units it yields to waiting higher-priority
// work (ShouldYield), and when realtime work preempts it mid-unit the unit's
// partial result is discarded and the job resumes from that unit.

// MaxJobSegmentSamples bounds one job unit (20 s): the most work a
// preemption can discard, and the granularity of segment times.
const MaxJobSegmentSamples = 20 * asr.SampleRate

// maxFinishedJobs bounds how many finished jobs are kept for GET.
const maxFinishedJobs = 64

// JobState is the lifecycle state of a job.
type JobState string

const (
	JobQueued    JobState = "queued" // waiting, including after a preemption / yield
	JobRunning   JobState = "running"
	JobCompleted JobState = "completed"
	JobFailed    JobState = "failed"
	JobCancelled JobState = "cancelled"
)

func (s JobState) terminal() bool {
	return s == JobCompleted || s == JobFailed || s == JobCancelled
}

// ErrJobNotFound: unknown job id (404).
var ErrJobNotFound = errors.New("job not found")

// JobSegment is one recognized unit, in seconds from the start of the file.
type JobSegment struct {
	Start float64 `json:"start"`
	End   float64 `json:"end"`
	Text  string  `json:"text"`
}

// JobInfo is the GET /v1/jobs/{id} payload (and, without text and
// segments, the job_progress event payload).
type JobInfo struct {
	ID            string       `json:"id"`
	State         JobState     `json:"state"`
	Priority      string       `json:"priority"`
	Engine        string       `json:"engine,omitempty"`
	CreatedAt     time.Time    `json:"created_at"`
	StartedAt     *time.Time   `json:"started_at,omitempty"`
	FinishedAt    *time.Time   `json:"finished_at,omitempty"`
	Duration      float64      `json:"duration"`
	SegmentsTotal int          `json:"segments_total"`
	SegmentsDone  int          `json:"segments_done"`
	Progress      float64      `json:"progress"`
	Preemptions   int          `json:"preemptions"`
	Text          *string      `json:"text,omitempty"`
	Language      string       `json:"language,omitempty"`
	Segments      []JobSegment `json:"segments,omitempty"`
	Error         string       `json:"error,omitempty"`
}

// JobCounts summarize jobs in the engine status.
type JobCounts struct {
	Queued  int `json:"queued"`
	Running int `json:"running"`
}

// JobSpec is a file transcription request.
type JobSpec struct {
	PCM      []int16
	Options  asr.TranscribeOptions
	Priority string // "" = default_priority_file
	Segments bool   // include per-segment text and times in the result
}

type job struct {
	id           string
	prio         scheduler.Priority
	wantSegments bool
	opts         asr.TranscribeOptions
	duration     float64
	ctx          context.Context
	cancel       context.CancelFunc
	done         chan struct{}
	created      time.Time

	// guarded by jobStore.mu
	pcm         []int16
	state       JobState
	engine      string
	spans       []asr.Span
	planned     bool
	texts       []string
	languages   []string
	next        int
	preemptions int
	started     time.Time
	finished    time.Time
	text        string
	language    string
	err         string
}

type jobStore struct {
	mu   sync.Mutex
	jobs map[string]*job
}

func (s *jobStore) init() { s.jobs = map[string]*job{} }

func (s *jobStore) counts() JobCounts {
	s.mu.Lock()
	defer s.mu.Unlock()
	var c JobCounts
	for _, j := range s.jobs {
		switch j.state {
		case JobQueued:
			c.Queued++
		case JobRunning:
			c.Running++
		}
	}
	return c
}

func sec(samples int) float64 {
	return float64(samples*1000/asr.SampleRate) / 1000
}

// infoLocked renders a job; full adds the text and segments.
func (j *job) infoLocked(full bool) JobInfo {
	info := JobInfo{
		ID: j.id, State: j.state, Priority: j.prio.String(), Engine: j.engine,
		CreatedAt: j.created, Duration: j.duration, SegmentsTotal: len(j.spans),
		SegmentsDone: j.next, Preemptions: j.preemptions, Language: j.language, Error: j.err,
	}
	if !j.started.IsZero() {
		t := j.started
		info.StartedAt = &t
	}
	if !j.finished.IsZero() {
		t := j.finished
		info.FinishedAt = &t
	}
	var total, done int
	for i, sp := range j.spans {
		total += sp.End - sp.Start
		if i < j.next {
			done += sp.End - sp.Start
		}
	}
	switch {
	case j.state == JobCompleted:
		info.Progress = 1
	case total > 0:
		info.Progress = float64(done) / float64(total)
	}
	if full {
		if j.state == JobCompleted {
			text := j.text
			info.Text = &text
		}
		if j.wantSegments {
			info.Segments = []JobSegment{}
			for i := 0; i < j.next; i++ {
				info.Segments = append(info.Segments, JobSegment{Start: sec(j.spans[i].Start), End: sec(j.spans[i].End), Text: j.texts[i]})
			}
		}
	}
	return info
}

// PlanSegments merges VAD speech regions into job units: consecutive
// regions join while the unit stays within maxLen samples (pauses inside a
// unit are kept, as in Qwen3's whole-clip trimming); a single region longer
// than maxLen is split into maxLen pieces. Silence between units is never
// recognized.
func PlanSegments(regions []asr.Span, total, maxLen int) []asr.Span {
	var out []asr.Span
	for _, r := range regions {
		r.Start = max(0, r.Start)
		r.End = min(total, r.End)
		if r.End <= r.Start {
			continue
		}
		if n := len(out); n > 0 && r.Start >= out[n-1].End && r.End-out[n-1].Start <= maxLen {
			out[n-1].End = r.End
			continue
		}
		for r.End-r.Start > maxLen {
			out = append(out, asr.Span{Start: r.Start, End: r.Start + maxLen})
			r.Start += maxLen
		}
		out = append(out, r)
	}
	return out
}

func newJobID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

func (m *Manager) publishJob(j *job) {
	m.jobs.mu.Lock()
	info := j.infoLocked(false)
	m.jobs.mu.Unlock()
	m.opts.Hub.Publish(events.TypeJobProgress, info)
}

// SubmitJob queues a file transcription job and returns immediately.
func (m *Manager) SubmitJob(spec JobSpec) (JobInfo, error) {
	eff := m.opts.Store.Effective()
	if _, ok := asr.KindForEngine(eff.Engine); !ok {
		return JobInfo{}, ErrNoLocalEngine
	}
	prio, err := m.ResolvePriority(KindFile, spec.Priority)
	if err != nil {
		return JobInfo{}, err
	}
	opts := spec.Options
	opts.Interrupt = nil
	if opts.Language == "" {
		opts.Language = eff.DefaultLanguage
	}
	ctx, cancel := context.WithCancel(context.Background())
	j := &job{
		id: newJobID(), prio: prio, wantSegments: spec.Segments, opts: opts,
		duration: sec(len(spec.PCM)), ctx: ctx, cancel: cancel, done: make(chan struct{}),
		created: m.opts.Now().UTC(), pcm: spec.PCM, state: JobQueued,
	}
	m.jobs.mu.Lock()
	m.jobs.jobs[j.id] = j
	m.pruneJobsLocked()
	info := j.infoLocked(false)
	m.jobs.mu.Unlock()
	m.opts.Hub.Publish(events.TypeJobProgress, info)
	m.wg.Add(1)
	go m.runJob(j)
	return info, nil
}

func (m *Manager) pruneJobsLocked() {
	var finished []*job
	for _, j := range m.jobs.jobs {
		if j.state.terminal() {
			finished = append(finished, j)
		}
	}
	if len(finished) <= maxFinishedJobs {
		return
	}
	sort.Slice(finished, func(a, b int) bool { return finished[a].finished.Before(finished[b].finished) })
	for _, j := range finished[:len(finished)-maxFinishedJobs] {
		delete(m.jobs.jobs, j.id)
	}
}

func (m *Manager) runJob(j *job) {
	defer m.wg.Done()
	defer close(j.done)
	err := m.sched.DoPreemptible(j.ctx, j.prio, func(r *scheduler.Run) error { return m.jobStep(j, r) })
	m.jobs.mu.Lock()
	j.finished = m.opts.Now().UTC()
	switch {
	case err == nil:
		j.state = JobCompleted
		var pieces []string
		for _, t := range j.texts {
			if t != "" {
				pieces = append(pieces, t)
			}
		}
		j.text = r2t2.JoinPieces(pieces)
		for _, l := range j.languages {
			if l != "" && l != "unknown" {
				j.language = l
				break
			}
		}
		if j.language == "" && len(j.languages) > 0 {
			j.language = "unknown"
		}
	case j.ctx.Err() != nil:
		j.state = JobCancelled
	default:
		j.state = JobFailed
		j.err = err.Error()
	}
	j.pcm = nil
	m.pruneJobsLocked()
	m.jobs.mu.Unlock()
	m.publishJob(j)
	m.publishStatus()
}

func (m *Manager) setJobState(j *job, st JobState) {
	m.jobs.mu.Lock()
	j.state = st
	if st == JobRunning && j.started.IsZero() {
		j.started = m.opts.Now().UTC()
	}
	m.jobs.mu.Unlock()
	m.publishJob(j)
}

// jobStep runs on the inference thread; it is called again after every
// preemption or yield and continues from j.next.
func (m *Manager) jobStep(j *job, r *scheduler.Run) error {
	defer m.touch()
	m.setJobState(j, JobRunning)
	if err := m.ensureLoaded(); err != nil {
		return err
	}
	m.jobs.mu.Lock()
	if j.engine == "" {
		j.engine = m.opts.Backend.Info().Engine
	}
	pcm, planned := j.pcm, j.planned
	m.jobs.mu.Unlock()
	if !planned {
		regions, err := m.opts.Backend.SpeechSegments(pcm)
		if err != nil {
			return fmt.Errorf("VAD segmentation: %w", err)
		}
		spans := PlanSegments(regions, len(pcm), m.opts.JobSegmentSamples)
		m.jobs.mu.Lock()
		j.spans, j.planned = spans, true
		j.texts = make([]string, len(spans))
		j.languages = make([]string, len(spans))
		m.jobs.mu.Unlock()
		m.publishJob(j)
	}
	opts := j.opts
	opts.Interrupt = r.Interrupted()
	for {
		m.jobs.mu.Lock()
		next, n := j.next, len(j.spans)
		m.jobs.mu.Unlock()
		if next >= n {
			return nil
		}
		if r.ShouldYield() {
			m.setJobState(j, JobQueued)
			return scheduler.ErrYield
		}
		sp := j.spans[next]
		res, err := m.opts.Backend.Transcribe(pcm[sp.Start:sp.End], opts)
		if err != nil {
			if r.Preempted() {
				m.jobs.mu.Lock()
				j.preemptions++
				m.jobs.mu.Unlock()
				m.setJobState(j, JobQueued)
			}
			return err
		}
		m.jobs.mu.Lock()
		j.texts[next] = res.Text
		j.languages[next] = res.Language
		j.next++
		m.jobs.mu.Unlock()
		m.publishJob(j)
	}
}

// Job returns a job by id.
func (m *Manager) Job(id string) (JobInfo, error) {
	m.jobs.mu.Lock()
	defer m.jobs.mu.Unlock()
	j, ok := m.jobs.jobs[id]
	if !ok {
		return JobInfo{}, ErrJobNotFound
	}
	return j.infoLocked(true), nil
}

// Jobs lists the known jobs, oldest first (without text and segments).
func (m *Manager) Jobs() []JobInfo {
	m.jobs.mu.Lock()
	defer m.jobs.mu.Unlock()
	out := make([]JobInfo, 0, len(m.jobs.jobs))
	for _, j := range m.jobs.jobs {
		out = append(out, j.infoLocked(false))
	}
	sort.Slice(out, func(a, b int) bool {
		if out[a].CreatedAt.Equal(out[b].CreatedAt) {
			return out[a].ID < out[b].ID
		}
		return out[a].CreatedAt.Before(out[b].CreatedAt)
	})
	return out
}

// CancelJob cancels a queued or running job and waits briefly for it to
// stop (a running Qwen3 unit aborts at the next transcribe.cpp boundary, an
// R2T2 unit before the next chunk push; a VAD or model-load step in flight
// completes first). Deleting a finished job removes it.
func (m *Manager) CancelJob(ctx context.Context, id string) (JobInfo, error) {
	m.jobs.mu.Lock()
	j, ok := m.jobs.jobs[id]
	if !ok {
		m.jobs.mu.Unlock()
		return JobInfo{}, ErrJobNotFound
	}
	if j.state.terminal() {
		delete(m.jobs.jobs, id)
		info := j.infoLocked(false)
		m.jobs.mu.Unlock()
		return info, nil
	}
	m.jobs.mu.Unlock()
	j.cancel()
	select {
	case <-j.done:
	case <-ctx.Done():
	case <-time.After(5 * time.Second):
	}
	m.jobs.mu.Lock()
	defer m.jobs.mu.Unlock()
	return j.infoLocked(false), nil
}

func (m *Manager) cancelAllJobs() {
	m.jobs.mu.Lock()
	var pending []*job
	for _, j := range m.jobs.jobs {
		if !j.state.terminal() {
			pending = append(pending, j)
		}
	}
	m.jobs.mu.Unlock()
	for _, j := range pending {
		j.cancel()
	}
}
