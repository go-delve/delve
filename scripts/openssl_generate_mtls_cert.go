package main

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"flag"
	"fmt"
	"log"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Some parts are borrow from https://golang.org/src/crypto/tls/generate_cert.go.
// Note: generate_cert.go don't support thant signed by ca.

var (
	host   = flag.String("host", "", "Comma-separated hostnames and IPs to generate a certificate for")
	output = flag.String("output", "./", "")
)

func CreateCrtKey(pathPrefix string, host string, isCa bool, caPathPrefix string) {
	x509Crt := &x509.Certificate{
		SerialNumber: big.NewInt(2019),
		Subject:      pkix.Name{},
		NotBefore:    time.Now(),
		NotAfter:     time.Now().AddDate(10, 0, 0),
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth},
		KeyUsage:     x509.KeyUsageDigitalSignature,
	}
	x509CaCrt := x509Crt
	var caPrivKey interface{}
	if isCa {
		x509Crt.IsCA = true
		x509Crt.BasicConstraintsValid = true
		x509Crt.KeyUsage = x509Crt.KeyUsage | x509.KeyUsageCertSign
	} else {
		if host != "" {
			hosts := strings.Split(host, ",")
			for _, h := range hosts {
				if ip := net.ParseIP(h); ip != nil {
					x509Crt.IPAddresses = append(x509Crt.IPAddresses, ip)
				} else {
					x509Crt.DNSNames = append(x509Crt.DNSNames, h)
				}
			}
		}
		if caPathPrefix != "" {
			ca, err := tls.LoadX509KeyPair(fmt.Sprintf("%s.crt", caPathPrefix), fmt.Sprintf("%s.key", caPathPrefix))
			if err != nil {
				log.Fatalf("Error LoadX509KeyPair failed, %s", err)
			}
			x509CaCrt, err = x509.ParseCertificate(ca.Certificate[0])
			if err != nil {
				log.Fatalf("Error ParseX509KeyPair failed, %s", err)
			}
			caPrivKey = ca.PrivateKey
		}
	}
	privKey, err := rsa.GenerateKey(rand.Reader, 4096)
	if err != nil {
		log.Fatalf("rsa generate key failed, %s", err)
	}
	if caPrivKey == nil {
		caPrivKey = privKey
	}
	crtBytes, err := x509.CreateCertificate(rand.Reader, x509Crt, x509CaCrt, &privKey.PublicKey, caPrivKey)
	if err != nil {
		log.Fatalf("create crt failed %s", err)
	}

	// pem encode
	crtPEM, err := os.Create(pathPrefix + ".crt")
	if err != nil {
		log.Fatalf("Failed to open %s for writing: %s", pathPrefix+".crt", err)
	}
	defer crtPEM.Close()
	err = pem.Encode(crtPEM, &pem.Block{
		Type:  "CERTIFICATE",
		Bytes: crtBytes,
	})
	if err != nil {
		log.Fatalf("Failed to encode crt pem %s", err)
	}

	certPrivKeyPEM, err := os.Create(pathPrefix + ".key")
	if err != nil {
		log.Fatalf("Failed to open %s for writing: %s", pathPrefix+".key", err)
	}
	defer certPrivKeyPEM.Close()
	err = pem.Encode(certPrivKeyPEM, &pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(privKey),
	})
	if err != nil {
		log.Fatalf("Failed to encode private pem %s", err)
	}
}

func main() {
	flag.Parse()

	if len(*host) == 0 {
		log.Fatalf("Missing required --host parameter")
	}

	if err := os.MkdirAll(*output, 0755); err != nil && !os.IsExist(err) {
		log.Fatal(err)
	}

	CreateCrtKey(filepath.Join(*output, "ca"), "", true, "")
	CreateCrtKey(filepath.Join(*output, "server"), "127.0.0.1", false, filepath.Join(*output, "ca"))
	CreateCrtKey(filepath.Join(*output, "client"), "", false, filepath.Join(*output, "ca"))
}
