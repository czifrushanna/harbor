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
import { ComponentFixture, TestBed, fakeAsync, tick } from '@angular/core/testing';
import { of, throwError } from 'rxjs';
import { DockerfileOptimizationComponent } from './dockerfile-optimization.component';
import { SharedTestingModule } from '../../../../../../shared/shared.module';
import { DockerfileService } from '../../../../../../../../ng-swagger-gen/services/dockerfile.service';
import { DockerfileOptimization } from '../../../../../../../../ng-swagger-gen/models/dockerfile-optimization';

describe('DockerfileOptimizationComponent', () => {
    let component: DockerfileOptimizationComponent;
    let fixture: ComponentFixture<DockerfileOptimizationComponent>;
    let fakedDockerfileService: {
        getDockerfileOptimization: jasmine.Spy;
        optimizeDockerfile: jasmine.Spy;
    };

    const pendingRecord: DockerfileOptimization = { status: 'Pending' };
    const runningRecord: DockerfileOptimization = { status: 'Running' };
    const successRecord: DockerfileOptimization = {
        status: 'Success',
        dockerfile: 'FROM scratch\n',
        optimized_dockerfile: 'FROM alpine:3.21\n',
    };
    const errorRecord: DockerfileOptimization = {
        status: 'Error',
        error: 'no attestation found',
    };

    function createComponent(): void {
        fixture = TestBed.createComponent(DockerfileOptimizationComponent);
        component = fixture.componentInstance;
        component.projectName = 'library';
        component.repoName = 'library/photon';
        component.digest = 'sha256:artifact';
    }

    beforeEach(() => {
        fakedDockerfileService = {
            getDockerfileOptimization: jasmine
                .createSpy('getDockerfileOptimization')
                .and.returnValue(throwError({ status: 404 })),
            optimizeDockerfile: jasmine.createSpy('optimizeDockerfile'),
        };

        TestBed.configureTestingModule({
            imports: [SharedTestingModule],
            declarations: [DockerfileOptimizationComponent],
            providers: [
                {
                    provide: DockerfileService,
                    useValue: fakedDockerfileService,
                },
            ],
        });
    });

    afterEach(() => {
        if (component) {
            component.ngOnDestroy();
        }
    });

    it('should create and show the button when no cached record exists (404)', () => {
        createComponent();
        fixture.detectChanges();

        expect(component).toBeTruthy();
        expect(component.showButton).toBeTrue();
        expect(component.loadingCached).toBeFalse();
        expect(component.errorMessage).toBeNull();
    });

    it('should surface a non-404 error from the initial cached lookup', () => {
        fakedDockerfileService.getDockerfileOptimization.and.returnValue(
            throwError({ error: { errors: [{ message: 'boom' }] } })
        );
        createComponent();
        fixture.detectChanges();

        expect(component.errorMessage).toBe('boom');
        expect(component.showButton).toBeFalse();
    });

    it('should render a cached Success record without polling', () => {
        fakedDockerfileService.getDockerfileOptimization.and.returnValues(
            of(successRecord)
        );
        createComponent();
        fixture.detectChanges();

        expect(component.result).toEqual(successRecord);
        expect(component.inProgress).toBeFalse();
        expect(component.showButton).toBeFalse();
        expect(
            fakedDockerfileService.getDockerfileOptimization
        ).toHaveBeenCalledTimes(1);
    });

    it('should render a cached Error record and offer the retry button', () => {
        fakedDockerfileService.getDockerfileOptimization.and.returnValues(
            of(errorRecord)
        );
        createComponent();
        fixture.detectChanges();

        expect(component.errorMessage).toBe('no attestation found');
        expect(component.showButton).toBeTrue();
        expect(component.result).toBeNull();
    });

    it('optimize() should start a fresh optimization and render the result', () => {
        fakedDockerfileService.optimizeDockerfile.and.returnValue(
            of(successRecord)
        );
        createComponent();
        fixture.detectChanges(); // triggers the initial (404) cached lookup

        component.optimize();

        expect(component.result).toEqual(successRecord);
        expect(component.loading).toBeFalse();
        expect(component.showButton).toBeFalse();
    });

    it('optimize() should surface an error message on failure', () => {
        fakedDockerfileService.optimizeDockerfile.and.returnValue(
            throwError({ error: { errors: [{ message: 'adapter unreachable' }] } })
        );
        createComponent();
        fixture.detectChanges();

        component.optimize();

        expect(component.errorMessage).toBe('adapter unreachable');
        expect(component.loading).toBeFalse();
    });

    it('should poll while Pending/Running and stop once a terminal status arrives', fakeAsync(() => {
        fakedDockerfileService.optimizeDockerfile.and.returnValue(
            of(pendingRecord)
        );
        createComponent();
        fixture.detectChanges();

        component.optimize();
        expect(component.inProgress).toBeTrue();

        // First poll tick: still running.
        fakedDockerfileService.getDockerfileOptimization.and.returnValue(
            of(runningRecord)
        );
        tick(3000);
        expect(component.inProgress).toBeTrue();
        expect(component.result).toBeNull();

        // Second poll tick: terminal Success.
        fakedDockerfileService.getDockerfileOptimization.and.returnValue(
            of(successRecord)
        );
        tick(3000);
        expect(component.inProgress).toBeFalse();
        expect(component.result).toEqual(successRecord);

        // Polling must have stopped: no further calls even after more time passes.
        const callsSoFar =
            fakedDockerfileService.getDockerfileOptimization.calls.count();
        tick(3000);
        expect(
            fakedDockerfileService.getDockerfileOptimization.calls.count()
        ).toBe(callsSoFar);

        component.ngOnDestroy();
        tick(10000);
    }));

    it('should ignore transient polling errors and keep polling', fakeAsync(() => {
        fakedDockerfileService.optimizeDockerfile.and.returnValue(
            of(pendingRecord)
        );
        createComponent();
        fixture.detectChanges();
        component.optimize();

        fakedDockerfileService.getDockerfileOptimization.and.returnValue(
            throwError({ status: 500 })
        );
        tick(3000);
        expect(component.inProgress).toBeTrue();
        expect(component.errorMessage).toBeNull();

        fakedDockerfileService.getDockerfileOptimization.and.returnValue(
            of(successRecord)
        );
        tick(3000);
        expect(component.inProgress).toBeFalse();
        expect(component.result).toEqual(successRecord);

        component.ngOnDestroy();
        tick(10000);
    }));

    it('should give up and show a timeout message after MAX_POLLS', fakeAsync(() => {
        fakedDockerfileService.optimizeDockerfile.and.returnValue(
            of(pendingRecord)
        );
        createComponent();
        fixture.detectChanges();
        component.optimize();

        fakedDockerfileService.getDockerfileOptimization.and.returnValue(
            of(runningRecord)
        );
        // MAX_POLLS is 200; the 201st tick trips the timeout guard.
        tick(3000 * 201);

        expect(component.inProgress).toBeFalse();
        expect(component.errorMessage).toBe(
            'Optimization is taking too long; please retry later'
        );
        expect(component.showButton).toBeTrue();

        component.ngOnDestroy();
    }));

    it('ngOnDestroy should unsubscribe from an in-flight poll', fakeAsync(() => {
        fakedDockerfileService.optimizeDockerfile.and.returnValue(
            of(pendingRecord)
        );
        createComponent();
        fixture.detectChanges();
        component.optimize();

        fakedDockerfileService.getDockerfileOptimization.and.returnValue(
            of(runningRecord)
        );
        component.ngOnDestroy();

        const callsBefore =
            fakedDockerfileService.getDockerfileOptimization.calls.count();
        tick(10000);
        expect(
            fakedDockerfileService.getDockerfileOptimization.calls.count()
        ).toBe(callsBefore);
    }));

});
