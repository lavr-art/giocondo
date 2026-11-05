package provisioning

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"sync"
	"time"

	"github.com/artemlavr/giocondo/internal/model/panel"
	"github.com/artemlavr/giocondo/internal/service/operation"
)

// Job states.
const (
	JobRunning = "running"
	JobOK      = "ok"
	JobFailed  = "failed"
)

// Limits of the in-memory job log.
const (
	maxJobs   = 20
	jobTTL    = time.Hour
	maxJobLog = 256 << 10
)

type job struct {
	mu        sync.Mutex
	v         panel.JobView
	truncated bool
}

// Write appends to the job log; after maxJobLog bytes the log is truncated.
func (j *job) Write(p []byte) (int, error) {
	j.mu.Lock()
	defer j.mu.Unlock()

	if room := maxJobLog - len(j.v.Log); room > 0 {
		if len(p) > room {
			j.v.Log += string(p[:room])
			j.truncated = true
		} else {
			j.v.Log += string(p)
		}
	} else if !j.truncated {
		j.truncated = true
	}

	return len(p), nil
}

// jobManager are background installation jobs (in memory, they live only as long as the panel process).
type jobManager struct {
	mu       sync.Mutex
	list     []*job
	Now      func() time.Time
	Executor operation.TaskExecutor
}

// Start runs fn in a goroutine; fn output goes to the job log.
func (js *jobManager) Start(
	_ context.Context,
	nodeID int64,
	kind string,
	fn func(ctx context.Context, log *jobLog) error,
) panel.JobView {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	j := &job{
		v: panel.JobView{
			ID:        hex.EncodeToString(b),
			NodeID:    nodeID,
			Kind:      kind,
			Status:    JobRunning,
			StartedAt: js.clock(),
		},
	}

	js.mu.Lock()
	js.gc()
	js.list = append(js.list, j)
	js.mu.Unlock()

	if js.Executor == nil || !js.Executor.Submit(SSHInstallTimeout, func(ctx context.Context) {
		err := fn(ctx, &jobLog{j: j})
		end := js.clock()

		j.mu.Lock()

		j.v.FinishedAt = &end
		if err != nil {
			j.v.Status, j.v.Error = JobFailed, err.Error()
		} else {
			j.v.Status = JobOK
		}
		j.mu.Unlock()
	}) {
		j.mu.Lock()
		end := js.clock()
		j.v.FinishedAt = &end
		j.v.Status = JobFailed
		j.v.Error = context.Canceled.Error()
		j.mu.Unlock()
	}

	return j.view()
}

// Get returns a job by id.
func (js *jobManager) Get(id string) (panel.JobView, bool) {
	js.mu.Lock()
	defer js.mu.Unlock()

	for _, j := range js.list {
		if j.v.ID == id {
			return j.view(), true
		}
	}

	return panel.JobView{}, false
}

func (js *jobManager) clock() time.Time {
	if js.Now != nil {
		return js.Now()
	}

	return time.Now().UTC()
}

// gc removes finished jobs older than jobTTL and the oldest ones beyond maxJobs. Call with js.mu held.
func (js *jobManager) gc() {
	now := js.clock()

	keep := js.list[:0]
	for _, j := range js.list {
		v := j.view()
		if v.FinishedAt != nil && now.Sub(*v.FinishedAt) > jobTTL {
			continue
		}

		keep = append(keep, j)
	}

	if over := len(keep) - (maxJobs - 1); over > 0 {
		keep = keep[over:]
	}

	js.list = keep
}

func (j *job) view() panel.JobView {
	j.mu.Lock()
	defer j.mu.Unlock()

	v := j.v
	if j.truncated {
		v.Log += "\n… log truncated\n"
	}

	return v
}

// Log is the io.Writer of a job log.
type jobLog struct{ j *job }

func (l *jobLog) Write(p []byte) (int, error) { return l.j.Write(p) }

func (s *Service) jobs() *jobManager {
	s.jobsOnce.Do(func() { s.jobsList = &jobManager{Executor: s.executor} })

	return s.jobsList
}
