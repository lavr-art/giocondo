package provision

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"sync"
	"time"
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

// JobView is a job snapshot for the API and pages.
type JobView struct {
	ID         string     `json:"id"`
	NodeID     int64      `json:"node_id"`
	Kind       string     `json:"kind"`
	Status     string     `json:"status"`
	Error      string     `json:"error,omitempty"`
	Log        string     `json:"log"`
	StartedAt  time.Time  `json:"started_at"`
	FinishedAt *time.Time `json:"finished_at"`
}

type job struct {
	mu        sync.Mutex
	v         JobView
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

// Jobs are background installation jobs (in memory, they live only as long as the panel process).
type Jobs struct {
	mu   sync.Mutex
	list []*job
	now  func() time.Time
}

func (js *Jobs) clock() time.Time {
	if js.now != nil {
		return js.now()
	}
	return time.Now().UTC()
}

// Start runs fn in a goroutine; fn output goes to the job log.
func (js *Jobs) Start(ctx context.Context, nodeID int64, kind string, fn func(ctx context.Context, log *Log) error) JobView {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	j := &job{v: JobView{ID: hex.EncodeToString(b), NodeID: nodeID, Kind: kind, Status: JobRunning, StartedAt: js.clock()}}
	js.mu.Lock()
	js.gc()
	js.list = append(js.list, j)
	js.mu.Unlock()
	go func() {
		err := fn(ctx, &Log{j: j})
		end := js.clock()
		j.mu.Lock()
		j.v.FinishedAt = &end
		if err != nil {
			j.v.Status, j.v.Error = JobFailed, err.Error()
		} else {
			j.v.Status = JobOK
		}
		j.mu.Unlock()
	}()
	return j.view()
}

// gc removes finished jobs older than jobTTL and the oldest ones beyond maxJobs. Call with js.mu held.
func (js *Jobs) gc() {
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

// Get returns a job by id.
func (js *Jobs) Get(id string) (JobView, bool) {
	js.mu.Lock()
	defer js.mu.Unlock()
	for _, j := range js.list {
		if j.v.ID == id {
			return j.view(), true
		}
	}
	return JobView{}, false
}

func (j *job) view() JobView {
	j.mu.Lock()
	defer j.mu.Unlock()
	v := j.v
	if j.truncated {
		v.Log += "\n… log truncated\n"
	}
	return v
}

// Log is the io.Writer of a job log.
type Log struct{ j *job }

func (l *Log) Write(p []byte) (int, error) { return l.j.Write(p) }
