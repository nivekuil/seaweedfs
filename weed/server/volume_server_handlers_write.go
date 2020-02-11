package weed_server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/chrislusf/seaweedfs/weed/glog"
	"github.com/chrislusf/seaweedfs/weed/operation"
	"github.com/chrislusf/seaweedfs/weed/stats"
	"github.com/chrislusf/seaweedfs/weed/storage/needle"
	"github.com/chrislusf/seaweedfs/weed/topology"
)

func (vs *VolumeServer) OldPostHandler(w http.ResponseWriter, r *http.Request) {

	stats.VolumeServerRequestCounter.WithLabelValues("post").Inc()
	start := time.Now()
	defer func() {
		stats.VolumeServerRequestHistogram.WithLabelValues("post").Observe(time.Since(start).Seconds())
	}()

	if e := r.ParseForm(); e != nil {
		glog.V(0).Infoln("form parse error:", e)
		oldWriteJsonError(w, r, http.StatusBadRequest, e)
		return
	}

	vid, fid, _, _, _ := parseURLPath(r.URL.Path)
	volumeId, ve := needle.NewVolumeId(vid)
	if ve != nil {
		glog.V(0).Infoln("NewVolumeId error:", ve)
		oldWriteJsonError(w, r, http.StatusBadRequest, ve)
		return
	}

	if !vs.oldMaybeCheckJwtAuthorization(r, vid, fid, true) {
		oldWriteJsonError(w, r, http.StatusUnauthorized, errors.New("wrong jwt"))
		return
	}

	needle, originalSize, ne := needle.OldCreateNeedleFromRequest(r, vs.FixJpgOrientation, vs.fileSizeLimitBytes)
	if ne != nil {
		oldWriteJsonError(w, r, http.StatusBadRequest, ne)
		return
	}

	ret := operation.UploadResult{}
	_, isUnchanged, writeError := topology.OldReplicatedWrite(vs.GetMaster(), vs.store, volumeId, needle, r)

	// http 304 status code does not allow body
	if writeError == nil && isUnchanged {
		w.WriteHeader(http.StatusNotModified)
		return
	}

	httpStatus := http.StatusCreated
	if writeError != nil {
		httpStatus = http.StatusInternalServerError
		ret.Error = writeError.Error()
	}
	if needle.HasName() {
		ret.Name = string(needle.Name)
	}
	ret.Size = uint32(originalSize)
	ret.ETag = needle.Etag()
	oldSetEtag(w, ret.ETag)
	oldWriteJsonQuiet(w, r, httpStatus, ret)
}

func (vs *VolumeServer) OldDeleteHandler(w http.ResponseWriter, r *http.Request) {

	stats.VolumeServerRequestCounter.WithLabelValues("delete").Inc()
	start := time.Now()
	defer func() {
		stats.VolumeServerRequestHistogram.WithLabelValues("delete").Observe(time.Since(start).Seconds())
	}()

	n := new(needle.Needle)
	vid, fid, _, _, _ := parseURLPath(r.URL.Path)
	volumeId, _ := needle.NewVolumeId(vid)
	n.ParsePath(fid)

	if !vs.oldMaybeCheckJwtAuthorization(r, vid, fid, true) {
		oldWriteJsonError(w, r, http.StatusUnauthorized, errors.New("wrong jwt"))
		return
	}

	// glog.V(2).Infof("volume %s deleting %s", vid, n)

	cookie := n.Cookie

	ecVolume, hasEcVolume := vs.store.FindEcVolume(volumeId)

	if hasEcVolume {
		count, err := vs.store.DeleteEcShardNeedle(context.Background(), ecVolume, n, cookie)
		oldWriteDeleteResult(err, count, w, r)
		return
	}

	_, ok := vs.store.ReadVolumeNeedle(volumeId, n)
	if ok != nil {
		m := make(map[string]uint32)
		m["size"] = 0
		oldWriteJsonQuiet(w, r, http.StatusNotFound, m)
		return
	}

	if n.Cookie != cookie {
		glog.V(0).Infoln("delete", r.URL.Path, "with unmaching cookie from ", r.RemoteAddr, "agent", r.UserAgent())
		oldWriteJsonError(w, r, http.StatusBadRequest, errors.New("File Random Cookie does not match."))
		return
	}

	count := int64(n.Size)

	if n.IsChunkedManifest() {
		chunkManifest, e := operation.LoadChunkManifest(n.Data, n.IsGzipped())
		if e != nil {
			oldWriteJsonError(w, r, http.StatusInternalServerError, fmt.Errorf("Load chunks manifest error: %v", e))
			return
		}
		// make sure all chunks had deleted before delete manifest
		if e := chunkManifest.DeleteChunks(vs.GetMaster(), vs.grpcDialOption); e != nil {
			oldWriteJsonError(w, r, http.StatusInternalServerError, fmt.Errorf("Delete chunks error: %v", e))
			return
		}
		count = chunkManifest.Size
	}

	n.LastModified = uint64(time.Now().Unix())
	if len(r.FormValue("ts")) > 0 {
		modifiedTime, err := strconv.ParseInt(r.FormValue("ts"), 10, 64)
		if err == nil {
			n.LastModified = uint64(modifiedTime)
		}
	}

	_, err := topology.OldReplicatedDelete(vs.GetMaster(), vs.store, volumeId, n, r)

	oldWriteDeleteResult(err, count, w, r)

}

func oldWriteDeleteResult(err error, count int64, w http.ResponseWriter, r *http.Request) {
	if err == nil {
		m := make(map[string]int64)
		m["size"] = count
		oldWriteJsonQuiet(w, r, http.StatusAccepted, m)
	} else {
		oldWriteJsonError(w, r, http.StatusInternalServerError, fmt.Errorf("Deletion Failed: %v", err))
	}
}

func oldSetEtag(w http.ResponseWriter, etag string) {
	if etag != "" {
		if strings.HasPrefix(etag, "\"") {
			w.Header().Set("ETag", etag)
		} else {
			w.Header().Set("ETag", "\""+etag+"\"")
		}
	}
}
