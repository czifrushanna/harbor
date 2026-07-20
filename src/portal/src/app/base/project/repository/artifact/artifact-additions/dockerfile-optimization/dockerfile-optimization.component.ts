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
import { Component, Input, OnDestroy, OnInit } from '@angular/core';
import { Subscription, timer } from 'rxjs';
import { finalize, switchMap } from 'rxjs/operators';
import { DockerfileService } from '../../../../../../../../ng-swagger-gen/services/dockerfile.service';
import { DockerfileOptimization } from '../../../../../../../../ng-swagger-gen/models/dockerfile-optimization';

const POLL_INTERVAL_MS = 3000;
// Stop polling after ~10 minutes; the backend job has its own timeout that marks
// the record errored, so a stuck poll means something is genuinely wrong.
const MAX_POLLS = 200;

const STATUS_PENDING = 'Pending';
const STATUS_RUNNING = 'Running';
const STATUS_SUCCESS = 'Success';
const STATUS_ERROR = 'Error';

@Component({
    selector: 'hbr-dockerfile-optimization',
    templateUrl: './dockerfile-optimization.component.html',
    styleUrls: ['./dockerfile-optimization.component.scss'],
})
export class DockerfileOptimizationComponent implements OnInit, OnDestroy {
    @Input() projectName: string;
    @Input() repoName: string;
    @Input() digest: string;

    loading = false;
    loadingCached = false;
    inProgress = false;
    result: DockerfileOptimization = null;
    errorMessage: string = null;
    showButton = false;

    private pollSubscription: Subscription;
    private pollCount = 0;

    constructor(private dockerfileService: DockerfileService) {}

    ngOnInit(): void {
        this.loadingCached = true;
        this.dockerfileService
            .getDockerfileOptimization({
                projectName: this.projectName,
                repositoryName: this.repoName,
                reference: this.digest,
            })
            .pipe(finalize(() => (this.loadingCached = false)))
            .subscribe({
                next: res => {
                    this.handleRecord(res);
                },
                error: err => {
                    if (err?.status === 404) {
                        this.showButton = true;
                    } else {
                        this.errorMessage =
                            err?.error?.errors?.[0]?.message ||
                            err?.message ||
                            'Failed to check for cached optimization';
                    }
                },
            });
    }

    ngOnDestroy(): void {
        this.stopPolling();
    }

    optimize(): void {
        this.errorMessage = null;
        this.result = null;
        this.loading = true;
        this.dockerfileService
            .optimizeDockerfile({
                projectName: this.projectName,
                repositoryName: this.repoName,
                reference: this.digest,
            })
            .pipe(finalize(() => (this.loading = false)))
            .subscribe({
                next: res => {
                    this.showButton = false;
                    this.handleRecord(res);
                },
                error: err => {
                    this.errorMessage =
                        err?.error?.errors?.[0]?.message ||
                        err?.message ||
                        'Failed to start Dockerfile optimization';
                },
            });
    }

    // handleRecord interprets the record status: terminal states render, pending
    // and running states enter/continue the polling loop.
    private handleRecord(rec: DockerfileOptimization): void {
        if (
            rec.status === STATUS_PENDING ||
            rec.status === STATUS_RUNNING
        ) {
            this.inProgress = true;
            this.startPolling();
            return;
        }

        this.inProgress = false;
        this.stopPolling();

        if (rec.status === STATUS_ERROR) {
            this.errorMessage = rec.error || 'Optimization failed';
            this.showButton = true;
            return;
        }

        // Success, or a legacy record without a status field
        this.result = rec;
        this.showButton = false;
    }

    private startPolling(): void {
        if (this.pollSubscription) {
            return;
        }
        this.pollCount = 0;
        this.pollSubscription = timer(POLL_INTERVAL_MS, POLL_INTERVAL_MS)
            .pipe(
                switchMap(() =>
                    this.dockerfileService.getDockerfileOptimization({
                        projectName: this.projectName,
                        repositoryName: this.repoName,
                        reference: this.digest,
                    })
                )
            )
            .subscribe({
                next: res => {
                    this.pollCount++;
                    if (this.pollCount > MAX_POLLS) {
                        this.stopPolling();
                        this.inProgress = false;
                        this.errorMessage =
                            'Optimization is taking too long; please retry later';
                        this.showButton = true;
                        return;
                    }
                    this.handleRecord(res);
                },
                error: () => {
                    // transient polling errors are ignored; the next tick retries
                    this.pollCount++;
                },
            });
    }

    private stopPolling(): void {
        if (this.pollSubscription) {
            this.pollSubscription.unsubscribe();
            this.pollSubscription = null;
        }
    }
}
