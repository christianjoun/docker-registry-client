package registry

import (
	"bytes"
	"io"
	"net/http"
	"net/url"

	"github.com/docker/distribution"
	digest "github.com/opencontainers/go-digest"
	"github.com/pkg/errors"
)

func (registry *Registry) DownloadBlob(repository string, digest digest.Digest) (io.ReadCloser, error) {
	url := registry.url("/v2/%s/blobs/%s", repository, digest)
	registry.Logf("registry.blob.download url=%s repository=%s digest=%s", url, repository, digest)

	resp, err := registry.Client.Get(url)
	if err != nil {
		return nil, err
	}

	return resp.Body, nil
}

// Following docker API specification for Chunked uploads : https://docs.docker.com/registry/spec/api/#listing-repositories
// See UploadBlob for more info about getBody
func (registry *Registry) UploadChunkedBlob(repository string, digest digest.Digest, content io.Reader, getBody func() (io.ReadCloser, error)) error {
	uploadUrl, err := registry.initiateUpload(repository)
	if err != nil {
		return err
	}

	registry.Logf("registry.blob.chunkedUpload url=%s repository=%s digest=%s", uploadUrl, repository, digest)

	upload, err := http.NewRequest("PATCH", uploadUrl.String(), content)
	if err != nil {
		return err
	}
	upload.Header.Set("Content-Type", "application/octet-stream")
	if getBody != nil {
		upload.GetBody = getBody
	}

	resp, err := registry.Client.Do(upload)
	if err != nil {
		return err
	}
	if resp.StatusCode != 202 {
		buf := new(bytes.Buffer)
		buf.ReadFrom(resp.Body)
		return errors.Errorf("retrieving %+v returned %v response: %s", uploadUrl, resp.Status, buf.String())
	}
	_ = resp.Body.Close()

	return registry.completeChunkedUploadBlob(repository, digest, getBody, uploadUrl)
}

// Following docker API specification for Completing Chunked uploads: https://docs.docker.com/registry/spec/api/#listing-repositories
// See UploadBlob for more info about getBody
func (registry *Registry) completeChunkedUploadBlob(repository string, digest digest.Digest, getBody func() (io.ReadCloser, error), uploadUrl *url.URL) error {
	q := uploadUrl.Query()
	q.Set("digest", digest.String())
	uploadUrl.RawQuery = q.Encode()

	upload, err := http.NewRequest("PUT", uploadUrl.String(), nil) //sending zero length body

	registry.Logf("registry.blob.completeChunkedUpload url=%s repository=%s digest=%s", uploadUrl, repository, digest)

	if err != nil {
		return err
	}
	upload.Header.Set("Content-Type", "application/octet-stream")
	upload.Header.Set("Content-Range", "0-0")
	upload.Header.Set("Content-Length", "0")
	if getBody != nil {
		upload.GetBody = getBody
	}

	resp, err := registry.Client.Do(upload)
	if err != nil {
		return err
	}
	if resp.StatusCode != 201 {
		buf := new(bytes.Buffer)
		buf.ReadFrom(resp.Body)
		return errors.Errorf("retrieving %+v returned %v response: %s", uploadUrl, resp.Status, buf.String())
	}

	_ = resp.Body.Close()

	return nil
}

// UploadBlob can be used to upload an FS layer or an image config file into the given repository.
// It uploads the bytes read from content. Digest must match with the hash of those bytes.
// In case of token authentication the HTTP request must be retried after a 401 Unauthorized response
// (see https://docs.docker.com/registry/spec/auth/token/). In this case the getBody function is called
// in order to retrieve a fresh instance of the content reader. This behaviour matches exactly of the
// GetBody parameter of http.Client. This also means that if content is of type *bytes.Buffer,
// *bytes.Reader or *strings.Reader, then GetBody is populated automatically (as explained in the
// documentation of http.NewRequest()), so nil can be passed as the getBody parameter.
func (registry *Registry) UploadBlob(repository string, digest digest.Digest, content io.Reader, getBody func() (io.ReadCloser, error)) error {
	uploadUrl, err := registry.initiateUpload(repository)
	if err != nil {
		return err
	}
	q := uploadUrl.Query()
	q.Set("digest", digest.String())
	uploadUrl.RawQuery = q.Encode()

	registry.Logf("registry.blob.upload url=%s repository=%s digest=%s", uploadUrl, repository, digest)

	upload, err := http.NewRequest("PUT", uploadUrl.String(), content)
	if err != nil {
		return err
	}
	upload.Header.Set("Content-Type", "application/octet-stream")
	if getBody != nil {
		upload.GetBody = getBody
	}

	resp, err := registry.Client.Do(upload)
	if err != nil {
		return err
	}
	_ = resp.Body.Close()
	return nil
}

func (registry *Registry) HasBlob(repository string, digest digest.Digest) (bool, error) {
	checkUrl := registry.url("/v2/%s/blobs/%s", repository, digest)
	registry.Logf("registry.blob.check url=%s repository=%s digest=%s", checkUrl, repository, digest)

	resp, err := registry.Client.Head(checkUrl)
	if resp != nil {
		defer resp.Body.Close()
	}
	if err == nil {
		return resp.StatusCode == http.StatusOK, nil
	}

	urlErr, ok := err.(*url.Error)
	if !ok {
		return false, err
	}
	httpErr, ok := urlErr.Err.(*HttpStatusError)
	if !ok {
		return false, err
	}
	if httpErr.Response.StatusCode == http.StatusNotFound {
		return false, nil
	}

	return false, err
}

func (registry *Registry) BlobMetadata(repository string, digest digest.Digest) (distribution.Descriptor, error) {
	checkUrl := registry.url("/v2/%s/blobs/%s", repository, digest)
	registry.Logf("registry.blob.check url=%s repository=%s digest=%s", checkUrl, repository, digest)

	resp, err := registry.Client.Head(checkUrl)
	if resp != nil {
		defer resp.Body.Close()
	}
	if err != nil {
		return distribution.Descriptor{}, err
	}

	return distribution.Descriptor{
		Digest: digest,
		Size:   resp.ContentLength,
	}, nil
}

func (registry *Registry) initiateUpload(repository string) (*url.URL, error) {
	initiateUrl := registry.url("/v2/%s/blobs/uploads/", repository)
	registry.Logf("registry.blob.initiate-upload url=%s repository=%s", initiateUrl, repository)

	resp, err := registry.Client.Post(initiateUrl, "application/octet-stream", nil)
	if resp != nil {
		defer resp.Body.Close()
	}
	if err != nil {
		return nil, err
	}

	location := resp.Header.Get("Location")
	locationUrl, err := url.Parse(location)
	if err != nil {
		return nil, err
	}
	return locationUrl, nil
}
