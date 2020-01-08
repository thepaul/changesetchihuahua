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
	Listen(network, address string) (net.Listener, error)
	TLSConfig() *tls.Config
}

type certManager struct {
	*autocert.Manager
}

func NewTLSAutoCertManager(hostPolicy HostPolicy) CertManager {
	autoManager := &autocert.Manager{
		Prompt:      autocert.AcceptTOS,
		Cache:       autocert.DirCache(*certCacheDir),
		HostPolicy:  autocert.HostPolicy(hostPolicy),
		RenewBefore: *certRenewBefore,
		Email:       *operatorEmail,
	}
	return &certManager{autoManager}
}

type listener struct {
	m    *certManager
	conf *tls.Config

	tcpListener net.Listener
}

// Adapted from golang.org/x/crypto/acme/autocert listener.go, to allow binding
// to a different local port than 443
func (m *certManager) Listen(network, bindAddr string) (net.Listener, error) {
	ln := &listener{
		m:    m,
		conf: m.TLSConfig(),
	}
	var err error
	ln.tcpListener, err = net.Listen(network, bindAddr)
	if err != nil {
		return nil, err
	}
	return ln, nil
}

func (ln *listener) Accept() (net.Conn, error) {
	conn, err := ln.tcpListener.Accept()
	if err != nil {
		return nil, err
	}
	tcpConn := conn.(*net.TCPConn)

	_ = tcpConn.SetKeepAlive(true)
	_ = tcpConn.SetKeepAlivePeriod(3 * time.Minute)

	return tls.Server(tcpConn, ln.conf), nil
}

func (ln *listener) Addr() net.Addr {
	return ln.tcpListener.Addr()
}

func (ln *listener) Close() error {
	return ln.tcpListener.Close()
}
