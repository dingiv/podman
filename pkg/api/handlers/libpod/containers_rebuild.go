//go:build !remote && (linux || freebsd)

package libpod

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/sirupsen/logrus"

	"go.podman.io/podman/v6/libpod"
	"go.podman.io/podman/v6/libpod/define"
	"go.podman.io/podman/v6/pkg/api/handlers/utils"
	api "go.podman.io/podman/v6/pkg/api/types"
	"go.podman.io/podman/v6/pkg/domain/entities"
	"go.podman.io/podman/v6/pkg/specgen"
	"go.podman.io/podman/v6/pkg/specgen/generate"
	"go.podman.io/podman/v6/pkg/specgenutil"
	"go.podman.io/storage"
)

// EasyTidyRebuildContainer is the easytidy native rebuild endpoint:
//
//	POST /libpod/containers/{name}/easytidy-rebuild?new_name=<tmp>&image=<dest>&easytidy_fast=true
//
// Body: identical to the libpod create body (the caller constructs it with
// the replacement container's full spec — userns, hostname, mounts, env...).
//
// Server-side atomic rebuild of a container: commit its RW layer into an
// image (fork fast semantics: raw same-mapping diff, layer id-mapping
// recorded), create a replacement container from that image with the spec
// from the body, start the replacement, remove the original and rename the
// replacement to the original name.
//
// Everything happens in-process, so the RW layer chain is preserved without
// manifest/blob round trips and never skips intermediate layers.  The caller
// owns the spec — this endpoint never derives or second-guesses it.
//
// Failure semantics:
//   - any failure before the original container is removed → best-effort
//     restart of the original (only if it was running when we stopped it)
//     and a 4xx/5xx response; the replacement is never left running
//   - after the original has been removed there is nothing to roll back to;
//     the committed image remains as the data fallback and the replacement
//     keeps running under new_name — the response names it for manual recovery
func EasyTidyRebuildContainer(w http.ResponseWriter, r *http.Request) {
	runtime := r.Context().Value(api.RuntimeKey).(*libpod.Runtime)

	name := utils.GetName(r)
	newName := r.URL.Query().Get("new_name")
	destImage := r.URL.Query().Get("image")
	fast := false
	if v := r.URL.Query().Get("easytidy_fast"); v != "" {
		parsed, err := strconv.ParseBool(v)
		if err != nil {
			utils.Error(w, http.StatusBadRequest, fmt.Errorf("invalid easytidy_fast parameter: %w", err))
			return
		}
		fast = parsed
	}
	if newName == "" || destImage == "" {
		utils.Error(w, http.StatusBadRequest, errors.New("new_name and image query parameters are required"))
		return
	}
	if newName == name {
		utils.Error(w, http.StatusBadRequest, fmt.Errorf("new_name %q must differ from the container being rebuilt", name))
		return
	}

	old, err := runtime.LookupContainer(name)
	if err != nil {
		utils.Error(w, http.StatusNotFound, err)
		return
	}
	started := time.Now()

	wasRunning := false
	if status, err := old.State(); err != nil {
		utils.InternalServerError(w, fmt.Errorf("inspecting container %q for rebuild: %w", name, err))
		return
	} else {
		wasRunning = status == define.ContainerStateRunning
	}
	// Best-effort restore: put the original back the way we found it.
	restore := func(stage string, cause error) {
		if !wasRunning {
			return
		}
		if err := old.Start(r.Context(), false); err != nil {
			logrus.Errorf("easytidy-rebuild: failed to restart original container %q after %s failure: %v", name, stage, err)
		}
		logrus.Infof("easytidy-rebuild: original container %q restarted after %s failure: %v", name, stage, cause)
	}

	// 1. decode the replacement spec first (fail fast before touching the
	// original container).  Mirror CreateContainer: seed the wire struct with
	// the runtime defaults so unset fields resolve exactly like a normal
	// create, then decode.
	conf, err := runtime.GetConfigNoCopy()
	if err != nil {
		utils.InternalServerError(w, err)
		return
	}
	noHosts := conf.Containers.NoHosts
	privileged := conf.Containers.Privileged
	wire := specGeneratorWire{
		SpecGenerator: specgen.SpecGenerator{
			ContainerNetworkConfig: specgen.ContainerNetworkConfig{
				UseImageHosts: &noHosts,
			},
			ContainerSecurityConfig: specgen.ContainerSecurityConfig{
				Umask:      conf.Containers.Umask,
				Privileged: &privileged,
			},
			ContainerHealthCheckConfig: specgen.ContainerHealthCheckConfig{
				HealthLogDestination: define.DefaultHealthCheckLocalDestination,
				HealthMaxLogCount:    define.DefaultHealthMaxLogCount,
				HealthMaxLogSize:     define.DefaultHealthMaxLogSize,
			},
		},
	}
	if err := utils.ReadJSONFromBody(r, &wire); err != nil {
		utils.Error(w, http.StatusBadRequest, err)
		return
	}
	sg := wire.SpecGenerator
	rLimits, err := parseRLimits(wire.Rlimits)
	if err != nil {
		utils.Error(w, http.StatusBadRequest, fmt.Errorf("invalid rlimit: %w", err))
		return
	}
	sg.Rlimits = rLimits

	if sg.Passwd == nil {
		t := true
		sg.Passwd = &t
	}

	// need to check for memory limit to adjust swap (parity with create)
	if sg.ResourceLimits != nil && sg.ResourceLimits.Memory != nil {
		s := ""
		var l int64
		if sg.ResourceLimits.Memory.Swap != nil {
			s = strconv.Itoa(int(*sg.ResourceLimits.Memory.Swap))
		}
		if sg.ResourceLimits.Memory.Limit != nil {
			l = *sg.ResourceLimits.Memory.Limit
		}
		specgenutil.LimitToSwap(sg.ResourceLimits.Memory, s, l)
	}

	sg.Image = destImage
	sg.Name = newName

	// 2. stop the original (stopped containers commit a consistent snapshot;
	// the replacement is started at the end anyway).  Already-stopped is fine.
	if err := old.Stop(); err != nil && !errors.Is(err, define.ErrCtrStopped) {
		utils.InternalServerError(w, fmt.Errorf("stopping container for rebuild: %w", err))
		restore("stop", err)
		return
	}

	// 3. commit the RW layer into the destination image.  fast=true here is
	// the ONE caller of the fork's incremental raw commit: O(diff) streaming
	// with the same-mapping layer id-mapping recording; pause/squash off.
	// Everything else (manual podman commit, GUI snapshot) keeps vanilla
	// full-snapshot semantics via fast=false.
	commitOptions := libpod.ContainerCommitOptions{
		Pause:        false,
		Squash:       false,
		EasyTidyFast: true,
	}
	committed, err := old.Commit(r.Context(), destImage, commitOptions)
	if err != nil {
		utils.InternalServerError(w, fmt.Errorf("committing container for rebuild: %w", err))
		restore("commit", err)
		return
	}
	logrus.Infof("easytidy-rebuild: committed %s -> %s (%.1fs)", name, committed.ID(), time.Since(started).Seconds())

	// 4. build the replacement exactly like the create endpoint does:
	// CompleteSpec (image env/labels/stop-signal merge, default envs...) →
	// MakeContainer → ExecuteCreate (NewContainer + volume preparation).
	warn, err := generate.CompleteSpec(r.Context(), runtime, &sg)
	if err != nil {
		if errors.Is(err, storage.ErrImageUnknown) {
			utils.Error(w, http.StatusNotFound, fmt.Errorf("no such image: %w", err))
			restore("complete-spec", err)
			return
		}
		utils.InternalServerError(w, err)
		restore("complete-spec", err)
		return
	}

	rtSpec, spec, opts, err := generate.MakeContainer(r.Context(), runtime, &sg, false, nil)
	if err != nil {
		if errors.Is(err, storage.ErrImageUnknown) {
			utils.Error(w, http.StatusNotFound, fmt.Errorf("no such image: %w", err))
			restore("make-container", err)
			return
		}
		utils.InternalServerError(w, err)
		restore("make-container", err)
		return
	}

	// easytidy (2026-10-02): the replacement container is created via the
	// SLOW path on purpose.  On the same-mapping rebuild chain the vanilla
	// slow path performs NO chown and NO mapped copy anyway (the committed
	// top layer's recorded mapping deep-equals the request), so skipping it
	// bought nothing — while the layer-selection compare it runs is the
	// guard rail that keeps fossil layers from being picked as the RW
	// parent.  The `easytidy_fast` query now only gates the incremental
	// commit above.
	_ = fast
	ctr, err := generate.ExecuteCreate(r.Context(), runtime, rtSpec, spec, false, opts...)
	if err != nil {
		utils.InternalServerError(w, err)
		restore("create", err)
		return
	}

	// 5. start the replacement.
	if err := ctr.Start(r.Context(), false); err != nil {
		utils.InternalServerError(w, fmt.Errorf("starting replacement container: %w", err))
		restore("start", err)
		return
	}

	// 6. remove the original (its RW data moved on to the replacement) and
	// rename the replacement to the original name.  Past this point the
	// original cannot be restored — the committed image is the fallback.
	timeout := uint(0)
	if err := runtime.RemoveContainer(r.Context(), old, true, false, &timeout); err != nil {
		utils.InternalServerError(w, fmt.Errorf("removing original container %q: %w (replacement %s keeps running as %q; original is stopped and intact)",
			name, err, ctr.ID(), newName))
		return
	}
	if _, err := runtime.RenameContainer(r.Context(), ctr, name); err != nil {
		utils.InternalServerError(w, fmt.Errorf("renaming replacement container to %q: %w (manual recovery: podman rename %s %s)",
			name, err, newName, name))
		return
	}

	logrus.Infof("easytidy-rebuild: %s rebuilt as %s in %.1fs", name, ctr.ID(), time.Since(started).Seconds())
	utils.WriteJSON(w, http.StatusCreated, entities.ContainerCreateResponse{ID: ctr.ID(), Warnings: warn})
}
