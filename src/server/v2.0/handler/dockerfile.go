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

package handler

import (
	"context"
	"fmt"
	"strings"

	"github.com/go-openapi/runtime/middleware"
	"github.com/go-openapi/strfmt"

	"github.com/goharbor/harbor/src/common/rbac"
	"github.com/goharbor/harbor/src/controller/artifact"
	buildkitdockerfilectl "github.com/goharbor/harbor/src/controller/buildkitdockerfile"
	"github.com/goharbor/harbor/src/lib/errors"
	buildkitdockerfilepkg "github.com/goharbor/harbor/src/pkg/buildkitdockerfile"
	dockerfileoptdao "github.com/goharbor/harbor/src/pkg/dockerfileoptimization/dao"
	"github.com/goharbor/harbor/src/pkg/registry"
	"github.com/goharbor/harbor/src/server/v2.0/models"
	operation "github.com/goharbor/harbor/src/server/v2.0/restapi/operations/dockerfile"
)

func newDockerfileAPI() *dockerfileAPI {
	return &dockerfileAPI{
		artCtl:      artifact.Ctl,
		dockerCtl:   buildkitdockerfilectl.DefaultController,
		registryCli: registry.Cli,
		optDAO:      dockerfileoptdao.New(),
	}
}

type dockerfileAPI struct {
	BaseAPI
	artCtl      artifact.Controller
	dockerCtl   buildkitdockerfilectl.Controller
	registryCli registry.Client
	optDAO      dockerfileoptdao.DAO
}

func (d *dockerfileAPI) Prepare(ctx context.Context, _ string, params any) middleware.Responder {
	if err := unescapePathParams(params, "RepositoryName"); err != nil {
		d.SendError(ctx, err)
	}
	return nil
}

func (d *dockerfileAPI) OptimizeDockerfile(ctx context.Context, params operation.OptimizeDockerfileParams) middleware.Responder {
	if err := d.RequireProjectAccess(ctx, params.ProjectName, rbac.ActionRead, rbac.ResourceArtifact); err != nil {
		return d.SendError(ctx, err)
	}

	repository := fmt.Sprintf("%s/%s", params.ProjectName, params.RepositoryName)
	art, err := d.artCtl.GetByReference(ctx, repository, params.Reference, nil)
	if err != nil {
		return d.SendError(ctx, err)
	}

	src := &buildkitdockerfilepkg.RegistryBlobSource{
		Client:     d.registryCli,
		Repository: art.RepositoryName,
		Reference:  art.Digest,
	}

	result, err := d.dockerCtl.ExtractDockerfileFromSource(ctx, src)
	if err != nil {
		if strings.Contains(err.Error(), "attestation manifest not found") {
			return d.SendError(ctx, errors.New(nil).WithCode(errors.PreconditionCode).
				WithMessage("no BuildKit provenance attestation found for this image"))
		}
		return d.SendError(ctx, err)
	}

	optimized, err := buildkitdockerfilepkg.OptimizeWithEnvConfig(ctx, result.Dockerfile)
	if err != nil {
		return d.SendError(ctx, err)
	}

	rec := &dockerfileoptdao.DockerfileOptimization{
		RepositoryName:            art.RepositoryName,
		ArtifactDigest:            art.Digest,
		Dockerfile:                result.Dockerfile,
		OptimizedDockerfile:       optimized,
		AttestationManifestDigest: result.AttestationManifestDigest,
		StatementDigest:           result.StatementDigest,
	}
	if err := d.optDAO.Upsert(ctx, rec); err != nil {
		return d.SendError(ctx, err)
	}

	return operation.NewOptimizeDockerfileOK().WithPayload(&models.DockerfileOptimization{
		Dockerfile:                result.Dockerfile,
		OptimizedDockerfile:       optimized,
		AttestationManifestDigest: result.AttestationManifestDigest,
		StatementDigest:           result.StatementDigest,
	})
}

func (d *dockerfileAPI) GetDockerfileOptimization(ctx context.Context, params operation.GetDockerfileOptimizationParams) middleware.Responder {
	if err := d.RequireProjectAccess(ctx, params.ProjectName, rbac.ActionRead, rbac.ResourceArtifact); err != nil {
		return d.SendError(ctx, err)
	}

	repository := fmt.Sprintf("%s/%s", params.ProjectName, params.RepositoryName)
	art, err := d.artCtl.GetByReference(ctx, repository, params.Reference, nil)
	if err != nil {
		return d.SendError(ctx, err)
	}

	rec, err := d.optDAO.GetByArtifact(ctx, art.RepositoryName, art.Digest)
	if err != nil {
		return d.SendError(ctx, err)
	}

	return operation.NewGetDockerfileOptimizationOK().WithPayload(&models.DockerfileOptimization{
		Dockerfile:                rec.Dockerfile,
		OptimizedDockerfile:       rec.OptimizedDockerfile,
		AttestationManifestDigest: rec.AttestationManifestDigest,
		StatementDigest:           rec.StatementDigest,
		CreatedAt:                 strfmt.DateTime(rec.CreatedAt),
	})
}
