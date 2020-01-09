package tls

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io/ioutil"
	"net"
)

// WrapListenerWithTls return a listener with tls validation.
func WrapListenerWithTls(l net.Listener, crtPath, keyPath string) (net.Listener, error) {
	cert, err := tls.LoadX509KeyPair(crtPath, keyPath)
	if err != nil {
		return nil, err
	}
	config := &tls.Config{Certificates: []tls.Certificate{cert}}
	return tls.NewListener(l, config), nil
}

// DialWithTls return a connection with tls validation.
func DialWithTls(network, addr, svcCrtPath string) (net.Conn, error) {
	pem, err := ioutil.ReadFile(svcCrtPath)
	if err != nil {
		return nil, err
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(pem) {
		return nil, fmt.Errorf("pool append certs from pem failed")
	}
	return tls.Dial(network, addr, &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12})
}
