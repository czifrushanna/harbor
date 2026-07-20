// Copyright Project Harbor Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//    http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package optimizeradapter

import (
	"context"
	"sync"
	"time"

	v1 "github.com/goharbor/harbor/src/pkg/optimizer/rest/v1"
)

// jobEntry tracks one submitted optimization. report is nil while the worker is
// still running.
type jobEntry struct {
	report    *v1.OptimizationReport
	createdAt time.Time
}

// store is an in-memory job store. Suitable for a single-replica deployment: a
// restart loses in-flight jobs, which surfaces as a 404 to the polling Harbor job
// and marks the optimization errored. Replace with a Redis-backed store (like the
// trivy adapter) if multiple replicas or restart resilience are ever needed.
type store struct {
	mu      sync.RWMutex
	entries map[string]*jobEntry
	ttl     time.Duration
}

func newStore(ttl time.Duration) *store {
	return &store{
		entries: make(map[string]*jobEntry),
		ttl:     ttl,
	}
}

// put registers a new running job.
func (s *store) put(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries[id] = &jobEntry{createdAt: time.Now()}
}

// complete stores the terminal report of a job.
func (s *store) complete(id string, report *v1.OptimizationReport) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if e, ok := s.entries[id]; ok {
		e.report = report
	}
}

// get returns the entry for the id, or nil when unknown/expired.
func (s *store) get(id string) *jobEntry {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.entries[id]
}

// sweep periodically evicts entries older than the TTL. Runs until ctx is done.
func (s *store) sweep(ctx context.Context) {
	tk := time.NewTicker(s.ttl / 4)
	defer tk.Stop()

	for {
		select {
		case <-tk.C:
			cutoff := time.Now().Add(-s.ttl)
			s.mu.Lock()
			for id, e := range s.entries {
				if e.createdAt.Before(cutoff) {
					delete(s.entries, id)
				}
			}
			s.mu.Unlock()
		case <-ctx.Done():
			return
		}
	}
}
