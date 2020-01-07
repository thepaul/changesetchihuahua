package main

import (
	"context"
	"crypto/tls"
	"flag"
	"net"
	"time"

	"golang.org/x/crypto/acme/autocert"
)

var (
	operatorEmail   = flag.String("operator-email", "", "Contact email address to be submitted to ACME server (e.g. Let's Encrypt) to be put in issued SSL certificates")
	certRenewBefore = flag.Duration("cert-renew-before", time.Hour*24*30, "How early certificates should be renewed before they expire")
	certCacheDir    = flag.String("cert-cache-dir", "./ssl-cert-cache/", "A directory on the local filesystem which will be used for storing SSL certificate information. If it does not exist, the directory will be created with 0700 permissions.")
)

type HostPolicy func(ctx context.Context, hostName string) error

type CertManager interface {
	Listener() net.Listener
	TLSConfig() *tls.Config
}

func NewTLSAutoCertManager(hostPolicy HostPolicy) CertManager {
	return &autocert.Manager{
		Prompt:      autocert.AcceptTOS,
		Cache:       autocert.DirCache(*certCacheDir),
		HostPolicy:  autocert.HostPolicy(hostPolicy),
		RenewBefore: *certRenewBefore,
		Email:       *operatorEmail,
	}
}
