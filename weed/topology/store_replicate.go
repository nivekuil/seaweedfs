package topology

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/valyala/fasthttp"

	"github.com/chrislusf/seaweedfs/weed/glog"
	"github.com/chrislusf/seaweedfs/weed/operation"
	"github.com/chrislusf/seaweedfs/weed/security"
	"github.com/chrislusf/seaweedfs/weed/storage"
	"github.com/chrislusf/seaweedfs/weed/storage/needle"
	"github.com/chrislusf/seaweedfs/weed/util"
)

func OldReplicatedWrite(masterNode string, s *storage.Store,
	volumeId needle.VolumeId, n *needle.Needle,
	r *http.Request) (size uint32, isUnchanged bool, err error) {

	//check JWT
	jwt := security.OldGetJwt(r)

	var remoteLocations []operation.Location
	if r.FormValue("type") != "replicate" {
		remoteLocations, err = getWritableRemoteReplications(s, volumeId, masterNode)
		if err != nil {
			glog.V(0).Infoln(err)
			return
		}
	}

	size, isUnchanged, err = s.WriteVolumeNeedle(volumeId, n)
	if err != nil {
		err = fmt.Errorf("failed to write to local disk: %v", err)
		glog.V(0).Infoln(err)
		return
	}

	if len(remoteLocations) > 0 { //send to other replica locations
		if err = distributedOperation(remoteLocations, s, func(location operation.Location) error {
			u := url.URL{
				Scheme: "http",
				Host:   location.Url,
				Path:   r.URL.Path,
			}
			q := url.Values{
				"type": {"replicate"},
				"ttl":  {n.Ttl.String()},
			}
			if n.LastModified > 0 {
				q.Set("ts", strconv.FormatUint(n.LastModified, 10))
			}
			if n.IsChunkedManifest() {
				q.Set("cm", "true")
			}
			u.RawQuery = q.Encode()

			pairMap := make(map[string]string)
			if n.HasPairs() {
				tmpMap := make(map[string]string)
				err := json.Unmarshal(n.Pairs, &tmpMap)
				if err != nil {
					glog.V(0).Infoln("Unmarshal pairs error:", err)
				}
				for k, v := range tmpMap {
					pairMap[needle.PairNamePrefix+k] = v
				}
			}

			_, err := operation.Upload(u.String(),
				string(n.Name), bytes.NewReader(n.Data), n.IsGzipped(), string(n.Mime),
				pairMap, jwt)
			return err
		}); err != nil {
			size = 0
			err = fmt.Errorf("failed to write to replicas for volume %d: %v", volumeId, err)
			glog.V(0).Infoln(err)
		}
	}
	return
}

func ReplicatedWrite(masterNode string, s *storage.Store,
	volumeId needle.VolumeId, n *needle.Needle,
	ctx *fasthttp.RequestCtx) (size uint32, isUnchanged bool, err error) {

	//check JWT
	jwt := security.GetJwt(ctx)

	var remoteLocations []operation.Location
	if string(ctx.FormValue("type")) != "replicate" {
		remoteLocations, err = getWritableRemoteReplications(s, volumeId, masterNode)
		if err != nil {
			glog.V(0).Infoln(err)
			return
		}
	}

	size, isUnchanged, err = s.WriteVolumeNeedle(volumeId, n)
	if err != nil {
		err = fmt.Errorf("failed to write to local disk: %v", err)
		glog.V(0).Infoln(err)
		return
	}

	if len(remoteLocations) > 0 { //send to other replica locations
		if err = distributedOperation(remoteLocations, s, func(location operation.Location) error {
			u := url.URL{
				Scheme: "http",
				Host:   location.Url,
				Path:   string(ctx.Path()),
			}
			q := url.Values{
				"type": {"replicate"},
				"ttl":  {n.Ttl.String()},
			}
			if n.LastModified > 0 {
				q.Set("ts", strconv.FormatUint(n.LastModified, 10))
			}
			if n.IsChunkedManifest() {
				q.Set("cm", "true")
			}
			u.RawQuery = q.Encode()

			pairMap := make(map[string]string)
			if n.HasPairs() {
				tmpMap := make(map[string]string)
				err := json.Unmarshal(n.Pairs, &tmpMap)
				if err != nil {
					glog.V(0).Infoln("Unmarshal pairs error:", err)
				}
				for k, v := range tmpMap {
					pairMap[needle.PairNamePrefix+k] = v
				}
			}

			_, err := operation.Upload(u.String(),
				string(n.Name), bytes.NewReader(n.Data), n.IsGzipped(), string(n.Mime),
				pairMap, jwt)
			return err
		}); err != nil {
			size = 0
			err = fmt.Errorf("failed to write to replicas for volume %d: %v", volumeId, err)
			glog.V(0).Infoln(err)
		}
	}
	return
}

func OldReplicatedDelete(masterNode string, store *storage.Store,
	volumeId needle.VolumeId, n *needle.Needle,
	r *http.Request) (size uint32, err error) {

	//check JWT
	jwt := security.OldGetJwt(r)

	var remoteLocations []operation.Location
	if r.FormValue("type") != "replicate" {
		remoteLocations, err = getWritableRemoteReplications(store, volumeId, masterNode)
		if err != nil {
			glog.V(0).Infoln(err)
			return
		}
	}

	size, err = store.DeleteVolumeNeedle(volumeId, n)
	if err != nil {
		glog.V(0).Infoln("delete error:", err)
		return
	}

	if len(remoteLocations) > 0 { //send to other replica locations
		if err = distributedOperation(remoteLocations, store, func(location operation.Location) error {
			return util.Delete("http://"+location.Url+r.URL.Path+"?type=replicate", string(jwt))
		}); err != nil {
			size = 0
		}
	}
	return
}

func ReplicatedDelete(masterNode string, store *storage.Store,
	volumeId needle.VolumeId, n *needle.Needle,
	ctx *fasthttp.RequestCtx) (size uint32, err error) {

	//check JWT
	jwt := security.GetJwt(ctx)

	var remoteLocations []operation.Location
	if string(ctx.FormValue("type")) != "replicate" {
		remoteLocations, err = getWritableRemoteReplications(store, volumeId, masterNode)
		if err != nil {
			glog.V(0).Infoln(err)
			return
		}
	}

	size, err = store.DeleteVolumeNeedle(volumeId, n)
	if err != nil {
		glog.V(0).Infoln("delete error:", err)
		return
	}

	if len(remoteLocations) > 0 { //send to other replica locations
		if err = distributedOperation(remoteLocations, store, func(location operation.Location) error {
			return util.Delete("http://"+location.Url+string(ctx.Path())+"?type=replicate", string(jwt))
		}); err != nil {
			size = 0
		}
	}
	return
}

type DistributedOperationResult map[string]error

func (dr DistributedOperationResult) Error() error {
	var errs []string
	for k, v := range dr {
		if v != nil {
			errs = append(errs, fmt.Sprintf("[%s]: %v", k, v))
		}
	}
	if len(errs) == 0 {
		return nil
	}
	return errors.New(strings.Join(errs, "\n"))
}

type RemoteResult struct {
	Host  string
	Error error
}

func distributedOperation(locations []operation.Location, store *storage.Store, op func(location operation.Location) error) error {
	length := len(locations)
	results := make(chan RemoteResult)
	for _, location := range locations {
		go func(location operation.Location, results chan RemoteResult) {
			results <- RemoteResult{location.Url, op(location)}
		}(location, results)
	}
	ret := DistributedOperationResult(make(map[string]error))
	for i := 0; i < length; i++ {
		result := <-results
		ret[result.Host] = result.Error
	}

	return ret.Error()
}

func getWritableRemoteReplications(s *storage.Store, volumeId needle.VolumeId, masterNode string) (
	remoteLocations []operation.Location, err error) {
	copyCount := s.GetVolume(volumeId).ReplicaPlacement.GetCopyCount()
	if copyCount > 1 {
		if lookupResult, lookupErr := operation.Lookup(masterNode, volumeId.String()); lookupErr == nil {
			if len(lookupResult.Locations) < copyCount {
				err = fmt.Errorf("replicating opetations [%d] is less than volume's replication copy count [%d]",
					len(lookupResult.Locations), copyCount)
				return
			}
			selfUrl := s.Ip + ":" + strconv.Itoa(s.Port)
			for _, location := range lookupResult.Locations {
				if location.Url != selfUrl {
					remoteLocations = append(remoteLocations, location)
				}
			}
		} else {
			err = fmt.Errorf("failed to lookup for %d: %v", volumeId, lookupErr)
			return
		}
	}

	return
}
