package daemon

import (
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"runtime"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/dockerversion"
	"github.com/docker/docker/image"
	"github.com/docker/docker/layer"
	"github.com/docker/docker/pkg/httputils"
	"github.com/docker/docker/pkg/progress"
	"github.com/docker/docker/pkg/streamformatter"
	"github.com/docker/docker/reference"
)

// ImportImage imports an image, getting the archived layer data either from
// inConfig (if src is "-"), or from a URI specified in src. Progress output is
// written to outStream. Repository and tag names can optionally be given in
// the repo and tag arguments, respectively.
func (daemon *Daemon) ImportImage(src string, newRef reference.Named, msg string, inConfig io.ReadCloser, outStream io.Writer, config *container.Config) error {
	var (
		sf      = streamformatter.NewJSONStreamFormatter()
		archive io.ReadCloser
		resp    *http.Response
	)

	if src == "-" {
		archive = inConfig
	} else {
		inConfig.Close()
		u, err := url.Parse(src)
		if err != nil {
			return err
		}
		if u.Scheme == "" {
			u.Scheme = "http"
			u.Host = src
			u.Path = ""
		}
		outStream.Write(sf.FormatStatus("", "Downloading from %s", u))
		resp, err = httputils.Download(u.String())
		if err != nil {
			return err
		}
		progressOutput := sf.NewProgressOutput(outStream, true)
		archive = progress.NewProgressReader(resp.Body, progressOutput, resp.ContentLength, "", "Importing")
	}

	defer archive.Close()
	if len(msg) == 0 {
		msg = "Imported from " + src
	}
	// TODO: support windows baselayer?
	l, err := daemon.layerStore.Register(archive, "")
	if err != nil {
		return err
	}
	defer layer.ReleaseAndLog(daemon.layerStore, l)

	created := time.Now().UTC()
	imgConfig, err := json.Marshal(&image.Image{
		V1Image: image.V1Image{
			DockerVersion: dockerversion.Version,
			Config:        config,
			Architecture:  runtime.GOARCH,
			OS:            runtime.GOOS,
			Created:       created,
			Comment:       msg,
		},
		RootFS: &image.RootFS{
			Type:    "layers",
			DiffIDs: []layer.DiffID{l.DiffID()},
		},
		History: []image.History{{
			Created: created,
			Comment: msg,
		}},
	})
	if err != nil {
		return err
	}

	id, err := daemon.imageStore.Create(imgConfig)
	if err != nil {
		return err
	}

	// FIXME: connect with commit code and call refstore directly
	if newRef != nil {
		if err := daemon.TagImage(newRef, id.String()); err != nil {
			return err
		}
	}

	daemon.LogImageEvent(id.String(), id.String(), "import")
	outStream.Write(sf.FormatStatus("", id.String()))
	return nil
}
