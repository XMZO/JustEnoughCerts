// Package esa is the module of Alibaba Cloud ESA (Edge Security
// Acceleration) (PLAN.md 5.4). As a deploy target, a site of an account
// gets each new version of a certificate and, because the free plan holds
// two custom certificates per site, the versions uploaded before are
// removed once the new one is there. As a solver, ESA answers HTTP
// validations at its edge, from Edge KV, through a routine JEC sets up.
package esa

import (
	"errors"
	"io/fs"
	"net/http"
	"time"

	"github.com/XMZO/JustEnoughCerts/i18n"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/module"
	"github.com/XMZO/JustEnoughCerts/internal/kernel/providers"
	"github.com/XMZO/JustEnoughCerts/internal/lib/httpx"
)

// Module is the esa module.
type Module struct {
	// web reaches OSS, where the code of the routine goes, and check
	// fetches the files of validations from websites as a CA does (see
	// httpx.Options.SkipVerify). Both reach the public internet only;
	// tests replace them.
	web, check *http.Client
	// newAPI and newEdge make the clients of an instance; tests replace
	// them.
	newAPI  func(in providers.Instance) (siteAPI, error)
	newEdge func(in providers.Instance) (edgeAPI, error)
	// poll and propagate pace the wait for the edge to answer with a file.
	poll, propagate time.Duration
}

// New returns the module.
func New() *Module {
	web, _ := httpx.Client(httpx.Options{Timeout: time.Minute, PublicOnly: true})
	check, _ := httpx.Client(httpx.Options{Timeout: 15 * time.Second, PublicOnly: true, SkipVerify: true})
	m := &Module{web: web, check: check, poll: 5 * time.Second, propagate: 5 * time.Minute}
	m.newAPI = func(in providers.Instance) (siteAPI, error) {
		s, err := newSDK(in, m.web)
		if err != nil {
			return nil, err
		}
		return s, nil
	}
	m.newEdge = func(in providers.Instance) (edgeAPI, error) {
		s, err := newSDK(in, m.web)
		if err != nil {
			return nil, err
		}
		return s, nil
	}
	return m
}

// Manifest describes the module.
func (*Module) Manifest() module.Manifest {
	return module.Manifest{
		ID:          "esa",
		Category:    module.Target,
		Name:        i18n.EsaModuleName,
		Description: i18n.EsaModuleDescription,
	}
}

// Migrations returns nil: the module keeps nothing of its own.
func (*Module) Migrations() fs.FS { return nil }

// Register adds the site type and the edge type.
func (m *Module) Register(r *module.Registry) error {
	return errors.Join(
		r.Providers.Add(siteType(), m.factory),
		r.Providers.Add(edgeType(), m.edgeFactory),
	)
}
