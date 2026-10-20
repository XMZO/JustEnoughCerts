package offsite

import (
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

// TestLiveS3 writes to and deletes from a real S3 compatible service, as
// SeaweedFS's S3 gateway in a container (MinIO publishes no images any
// more): JEC_TEST_S3 is its address, path style, with
// JEC_TEST_S3_BUCKET (made here when missing), JEC_TEST_S3_KEY and
// JEC_TEST_S3_SECRET.
func TestLiveS3(t *testing.T) {
	endpoint := os.Getenv("JEC_TEST_S3")
	if endpoint == "" {
		t.Skip("JEC_TEST_S3 is not set")
	}
	b := &bucket{
		client: &http.Client{Timeout: time.Minute}, endpoint: endpoint, region: "us-east-1", name: os.Getenv("JEC_TEST_S3_BUCKET"),
		pathStyle: true, access: os.Getenv("JEC_TEST_S3_KEY"), secret: os.Getenv("JEC_TEST_S3_SECRET"), now: time.Now,
	}
	// The bucket itself: a PUT of its path, which fails when it is there.
	whole := *b
	whole.name = ""
	_ = whole.put(t.Context(), b.name, nil)
	if err := b.put(t.Context(), "jec/jec-20261020-020000.db", []byte(strings.Repeat("backup ", 1000))); err != nil {
		t.Fatalf("put: %v", err)
	}
	if err := b.remove(t.Context(), "jec/jec-20261020-020000.db"); err != nil {
		t.Errorf("remove: %v", err)
	}
	wrong := *b
	wrong.secret = "wrong"
	if err := wrong.put(t.Context(), "jec/x", []byte("x")); err == nil || !strings.Contains(err.Error(), "SignatureDoesNotMatch") {
		t.Errorf("a wrong secret: %v", err)
	}
}

// TestLiveWebDAV writes to and deletes from a real WebDAV server, as rclone
// serving WebDAV in a container: JEC_TEST_WEBDAV is the address of a
// folder, JEC_TEST_WEBDAV_USER a user:password.
func TestLiveWebDAV(t *testing.T) {
	endpoint := os.Getenv("JEC_TEST_WEBDAV")
	if endpoint == "" {
		t.Skip("JEC_TEST_WEBDAV is not set")
	}
	user, pass, _ := strings.Cut(os.Getenv("JEC_TEST_WEBDAV_USER"), ":")
	f := &folder{client: &http.Client{Timeout: time.Minute}, endpoint: endpoint, user: user, password: pass}
	if err := f.put(t.Context(), "jec/server-1/jec-20261020-020000.db", []byte(strings.Repeat("backup ", 1000))); err != nil {
		t.Fatalf("put: %v", err)
	}
	if err := f.remove(t.Context(), "jec/server-1/jec-20261020-020000.db"); err != nil {
		t.Errorf("remove: %v", err)
	}
	wrong := *f
	wrong.password = "wrong"
	if err := wrong.put(t.Context(), "jec/x", []byte("x")); err == nil {
		t.Error("a wrong password was taken")
	}
}
