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

// optimizer-adapter runs the rec-engine Dockerfile optimizer as a pluggable Harbor
// optimizer adapter service. See src/pkg/optimizeradapter for the implementation.
package main

import (
	"context"
	"errors"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/goharbor/harbor/src/lib/log"
	"github.com/goharbor/harbor/src/pkg/optimizeradapter"
)

func main() {
	cfg, err := optimizeradapter.LoadConfig()
	if err != nil {
		log.Fatalf("optimizer-adapter: %v", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	server := optimizeradapter.NewServer(cfg)
	server.StartSweeper(ctx)

	httpServer := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           server.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		log.Infof("optimizer-adapter %s listening on %s", optimizeradapter.Version, cfg.ListenAddr)
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("optimizer-adapter: %v", err)
		}
	}()

	<-ctx.Done()
	log.Info("optimizer-adapter shutting down")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		log.Errorf("optimizer-adapter shutdown: %v", err)
	}
}
