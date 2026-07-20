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
import { Component, Input, OnInit } from '@angular/core';
import { finalize } from 'rxjs/operators';
import { DockerfileService } from '../../../../../../../../ng-swagger-gen/services/dockerfile.service';
import { DockerfileOptimization } from '../../../../../../../../ng-swagger-gen/models/dockerfile-optimization';

@Component({
    selector: 'hbr-dockerfile-optimization',
    templateUrl: './dockerfile-optimization.component.html',
    styleUrls: ['./dockerfile-optimization.component.scss'],
})
export class DockerfileOptimizationComponent implements OnInit {
    @Input() projectName: string;
    @Input() repoName: string;
    @Input() digest: string;

    loading = false;
    loadingCached = false;
    result: DockerfileOptimization = null;
    errorMessage: string = null;
    showButton = false;

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
                    this.result = res;
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

    optimize(): void {
        this.errorMessage = null;
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
                    this.result = res;
                    this.showButton = false;
                },
                error: err => {
                    const msg =
                        err?.error?.errors?.[0]?.message ||
                        err?.message ||
                        'Failed to optimize Dockerfile';
                    this.errorMessage = msg;
                },
            });
    }
}
