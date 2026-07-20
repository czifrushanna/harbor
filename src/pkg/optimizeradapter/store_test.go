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
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	v1 "github.com/goharbor/harbor/src/pkg/optimizer/rest/v1"
)

func TestStore_PutThenGetIsRunning(t *testing.T) {
	s := newStore(time.Hour)
	s.put("job-1")

	e := s.get("job-1")
	require.NotNil(t, e)
	require.Nil(t, e.report)
}

func TestStore_GetUnknownID(t *testing.T) {
	s := newStore(time.Hour)
	require.Nil(t, s.get("missing"))
}

func TestStore_CompleteUnknownIDIsNoOp(t *testing.T) {
	s := newStore(time.Hour)
	// No put() first: complete on an unknown id must not panic or create an entry.
	s.complete("missing", &v1.OptimizationReport{Status: v1.ReportStatusSuccess})
	require.Nil(t, s.get("missing"))
}

func TestStore_CompleteSetsReport(t *testing.T) {
	s := newStore(time.Hour)
	s.put("job-1")

	report := &v1.OptimizationReport{Status: v1.ReportStatusSuccess, Dockerfile: "FROM scratch"}
	s.complete("job-1", report)

	e := s.get("job-1")
	require.NotNil(t, e)
	require.Same(t, report, e.report)
}

func TestStore_SweepEvictsExpiredEntries(t *testing.T) {
	s := newStore(10 * time.Millisecond)
	s.put("old")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go s.sweep(ctx)

	require.Eventually(t, func() bool {
		return s.get("old") == nil
	}, time.Second, 5*time.Millisecond)
}

func TestStore_SweepKeepsFreshEntries(t *testing.T) {
	s := newStore(time.Hour)
	s.put("fresh")

	ctx, cancel := context.WithCancel(context.Background())
	go s.sweep(ctx)
	defer cancel()

	// Give the sweeper a couple of ticks (ttl/4 interval) to run; the fresh entry
	// must survive since its age is nowhere near the hour-long TTL.
	time.Sleep(50 * time.Millisecond)
	require.NotNil(t, s.get("fresh"))
}

func TestStore_SweepStopsOnContextDone(t *testing.T) {
	s := newStore(10 * time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		s.sweep(ctx)
		close(done)
	}()

	cancel()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("sweep did not exit after context cancellation")
	}
}
