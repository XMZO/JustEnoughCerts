package esa

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	openapi "github.com/alibabacloud-go/darabonba-openapi/v2/client"
	esaapi "github.com/go-acme/esa-20240910/v3/client"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/contracts"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/retry"
)

// The errors of the API, as the SDK itself returns them, are told apart:
// a site that holds all the certificates it may, something that is not
// there, a refused AccessKey.
func TestSDKErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.Header.Get("x-acs-action") {
		case "SetCertificate":
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, `{"RequestId":"r","Code":"Certificate.QuotaExceeded","Message":"The number of certificates exceeds the quota."}`)
		case "DeleteKv":
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, `{"RequestId":"r","Code":"InvalidKey.NotFound","Message":"The key does not exist."}`)
		default:
			w.WriteHeader(http.StatusForbidden)
			_, _ = io.WriteString(w, `{"RequestId":"r","Code":"InvalidAccessKeyId.NotFound","Message":"Specified access key is not found."}`)
		}
	}))
	defer srv.Close()
	client, err := esaapi.NewClient(new(openapi.Config).SetAccessKeyId("id").SetAccessKeySecret("secret").
		SetEndpoint(strings.TrimPrefix(srv.URL, "http://")).SetProtocol("HTTP"))
	if err != nil {
		t.Fatal(err)
	}
	s := &sdk{client: client, web: srv.Client()}

	_, err = s.upload(t.Context(), 1, "jec-1-abc", "CHAIN", "KEY")
	if !isQuota(err) {
		t.Errorf("a full site: %T %v", err, err)
	}
	if err := s.deleteKV(t.Context(), kvNamespace, "key"); err != nil {
		t.Errorf("deleting what is gone: %v", err)
	}
	_, err = s.siteID(t.Context(), "example.com")
	var p *contracts.Problem
	if err := apiProblem(err); !errors.As(err, &p) || p.Key != i18n.EsaErrorAuth || !retry.IsPermanent(err) {
		t.Errorf("a refused AccessKey: %v", err)
	}
}
