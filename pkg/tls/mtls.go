package tls

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io/ioutil"
	"net"
)

func LoadCertPool(caCrtPath string) (*x509.CertPool, error) {
	pem, err := ioutil.ReadFile(caCrtPath)
	if err != nil {
		return nil, err
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(pem) {
		return nil, fmt.Errorf("pool append certs from pem failed")
	}
	return pool, nil
}

// loadMtlsConfig return *tls.Config by parsing caFile, certFile and KeyFile
func loadMtlsConfig(caCrtPath, crtPath, keyPath string) (*tls.Config, error) {
	pool, err := LoadCertPool(caCrtPath)
	if err != nil {
		return nil, fmt.Errorf("load cert pool from (%s): %v", caCrtPath, err)
	}
	cert, err := tls.LoadX509KeyPair(crtPath, keyPath)
	if err != nil {
		return nil, fmt.Errorf("load x509 key pair from (%s, %s): %v", crtPath, keyPath, err)
	}
	cfg := &tls.Config{
		RootCAs:      pool,
		ClientCAs:    pool,
		ClientAuth:   tls.RequireAndVerifyClientCert,
		MinVersion:   tls.VersionTLS12,
		Certificates: []tls.Certificate{cert},
	}
	return cfg, nil
}

// WrapListenerWithMtls return a listener with mtls validation.
func WrapListenerWithMtls(l net.Listener, caCrtPath, crtPath, keyPath string) (net.Listener, error) {
	config, err := loadMtlsConfig(caCrtPath, crtPath, keyPath)
	if err != nil {
		return nil, err
	}
	return tls.NewListener(l, config), nil
}

// DialWithMtls return a connection with mtls validation.
func DialWithMtls(network, addr string, caCrtPath string, crtPath string, keyPath string) (net.Conn, error) {
	config, err := loadMtlsConfig(caCrtPath, crtPath, keyPath)
	if err != nil {
		return nil, err
	}
	return tls.Dial(network, addr, config)
}
