package engine

import (
	"errors"
	"net/http"
	"net/http/cookiejar"
	"time"

	"golang.org/x/net/publicsuffix"

	"github.com/fhsinchy/bolt/internal/config"
)

func newHTTPClient(cfg *config.Config) *http.Client {
	transport := &http.Transport{
		MaxIdleConnsPerHost:   32,
		MaxConnsPerHost:       0,
		TLSHandshakeTimeout:  10 * time.Second,
		ResponseHeaderTimeout: 15 * time.Second,
		IdleConnTimeout:       90 * time.Second,
		DisableCompression:    true,
	}

	jar, _ := cookiejar.New(&cookiejar.Options{
		PublicSuffixList: publicsuffix.List,
	})

	return &http.Client{
		Transport: transport,
		Jar:       jar,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return errors.New("too many redirects (max 10)")
			}
			return nil
		},
	}
}
