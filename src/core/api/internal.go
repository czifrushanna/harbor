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

package api

import (
	"context"
	"encoding/json"
	"io"
	"strings"

	o "github.com/beego/beego/v2/client/orm"

	"github.com/goharbor/harbor/src/common"
	"github.com/goharbor/harbor/src/common/models"
	buildkitdockerfilectl "github.com/goharbor/harbor/src/controller/buildkitdockerfile"
	"github.com/goharbor/harbor/src/controller/quota"
	"github.com/goharbor/harbor/src/controller/user"
	"github.com/goharbor/harbor/src/core/auth"
	"github.com/goharbor/harbor/src/lib/config"
	"github.com/goharbor/harbor/src/lib/errors"
	"github.com/goharbor/harbor/src/lib/log"
	"github.com/goharbor/harbor/src/lib/orm"
	buildkitdockerfilepkg "github.com/goharbor/harbor/src/pkg/buildkitdockerfile"
)

// InternalAPI handles request of harbor admin...
type InternalAPI struct {
	BaseController
}

var buildkitDockerfileCtl buildkitdockerfilectl.Controller = buildkitdockerfilectl.DefaultController
var buildkitDockerfileOptimize = buildkitdockerfilepkg.OptimizeWithEnvConfig

type buildkitDockerfileExtractRequest struct {
	OCIArchivePath string `json:"oci_archive_path"`
	Optimize       bool   `json:"optimize,omitempty"`
}

type buildkitDockerfileExtractResponse struct {
	Dockerfile                string `json:"dockerfile"`
	OptimizedDockerfile       string `json:"optimized_dockerfile,omitempty"`
	AttestationManifestDigest string `json:"attestation_manifest_digest"`
	StatementDigest           string `json:"statement_digest"`
}

// Prepare validates the URL and parms
func (ia *InternalAPI) Prepare() {
	ia.BaseController.Prepare()
	if !ia.SecurityCtx.IsAuthenticated() {
		ia.SendUnAuthorizedError(errors.New("UnAuthorized"))
		return
	}
	if !ia.SecurityCtx.IsSysAdmin() {
		ia.SendForbiddenError(errors.New(ia.SecurityCtx.GetUsername()))
		return
	}
}

// RenameAdmin we don't provide flexibility in this API, as this is a workaround.
func (ia *InternalAPI) RenameAdmin() {
	ctx := ia.Ctx.Request.Context()
	if !auth.IsSuperUser(ctx, ia.SecurityCtx.GetUsername()) {
		log.Errorf("User %s is not super user, not allow to rename admin.", ia.SecurityCtx.GetUsername())
		ia.SendForbiddenError(errors.New(ia.SecurityCtx.GetUsername()))
		return
	}
	newName := common.NewHarborAdminName
	if err := user.Ctl.UpdateProfile(ctx, &models.User{
		UserID:   1,
		Username: newName,
	}, "username"); err != nil {
		log.Errorf("Failed to change admin's username, error: %v", err)
		ia.SendInternalServerError(errors.New("failed to rename admin user"))
		return
	}
	log.Debugf("The super user has been renamed to: %s", newName)
	if err := ia.DestroySession(); err != nil {
		log.Errorf("failed to destroy session for admin user, error: %v", err)
		return
	}
}

// ExtractBuildkitDockerfile extracts a Dockerfile from a BuildKit-enabled OCI archive.
func (ia *InternalAPI) ExtractBuildkitDockerfile() {
	ctx := ia.Ctx.Request.Context()
	defer ia.Ctx.Request.Body.Close()

	result, optimize, err := ia.extractBuildkitDockerfile(ctx)
	if err != nil {
		ia.SendError(err)
		return
	}

	optimizedDockerfile := ""
	if optimize {
		optimizedDockerfile, err = buildkitDockerfileOptimize(ctx, result.Dockerfile)
		if err != nil {
			ia.SendError(err)
			return
		}
	}

	ia.WriteJSONData(&buildkitDockerfileExtractResponse{
		Dockerfile:                result.Dockerfile,
		OptimizedDockerfile:       optimizedDockerfile,
		AttestationManifestDigest: result.AttestationManifestDigest,
		StatementDigest:           result.StatementDigest,
	})
}

func (ia *InternalAPI) extractBuildkitDockerfile(ctx context.Context) (*buildkitdockerfilectl.Result, bool, error) {
	if strings.HasPrefix(ia.Ctx.Request.Header.Get("Content-Type"), "multipart/form-data") {
		file, _, err := ia.Ctx.Request.FormFile("oci_archive")
		if err != nil {
			file, _, err = ia.Ctx.Request.FormFile("archive")
		}
		if err != nil {
			return nil, false, errors.BadRequestError(err).WithMessage("oci_archive upload is required")
		}
		defer file.Close()

		result, err := buildkitDockerfileCtl.ExtractDockerfileFromReader(ctx, file)
		return result, strings.EqualFold(ia.Ctx.Request.FormValue("optimize"), "true"), err
	}

	req := &buildkitDockerfileExtractRequest{}
	body, err := io.ReadAll(ia.Ctx.Request.Body)
	if err != nil {
		return nil, false, errors.BadRequestError(nil).WithMessage("failed to read request body")
	}
	if err := json.Unmarshal(body, req); err != nil {
		return nil, false, errors.BadRequestError(nil).WithMessage(err.Error())
	}
	if len(req.OCIArchivePath) == 0 {
		return nil, false, errors.BadRequestError(nil).WithMessage("oci_archive_path is required")
	}

	result, err := buildkitDockerfileCtl.ExtractDockerfile(ctx, req.OCIArchivePath)
	return result, req.Optimize, err
}

// SyncQuota ...
func (ia *InternalAPI) SyncQuota() {
	if !config.QuotaPerProjectEnable(orm.Context()) {
		ia.SendError(errors.ForbiddenError(nil).WithMessage("quota per project is deactivated"))
		return
	}
	ctx := orm.Context()
	cur := config.ReadOnly(ctx)
	cfgMgr := config.GetCfgManager(ctx)
	if !cur {
		cfgMgr.Set(ctx, common.ReadOnly, true)
		err := cfgMgr.Save(ctx)
		if err != nil {
			log.Warningf("failed to save context into config manager, error: %v", err)
		}
	}
	// For api call, to avoid the timeout, it should be asynchronous
	go func() {
		defer func() {
			ctx := orm.Context()
			cfgMgr.Set(ctx, common.ReadOnly, cur)
			err := cfgMgr.Save(ctx)
			if err != nil {
				log.Warningf("failed to save context into config manager asynchronously, error: %v", err)
			}
		}()
		log.Info("start to sync quota(API), the system will be set to ReadOnly and back it normal once it done.")
		ctx := orm.NewContext(context.TODO(), o.NewOrm())
		err := quota.RefreshForProjects(ctx)
		if err != nil {
			log.Errorf("fail to sync quota(API), but with error: %v, please try to do it again.", err)
			return
		}
		log.Info("success to sync quota(API).")
	}()
}
