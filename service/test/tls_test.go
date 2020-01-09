package service_test

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	protest "github.com/go-delve/delve/pkg/proc/test"
	nativetls "github.com/go-delve/delve/pkg/tls"
	"github.com/go-delve/delve/service"
	"github.com/go-delve/delve/service/api"
	"github.com/go-delve/delve/service/rpc2"
	"github.com/go-delve/delve/service/rpccommon"
	"log"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

/*

# Using openssl to generate is also ok.

mtls.sh
	mkdir mtls

	# specity the ip that the server will listen or client will connect
	echo subjectAltName = IP:127.0.0.1 > ./mtls/extfile.cnf

	# generate ca.key/ca.crt
	openssl genrsa -out ./mtls/ca.key 2048
	openssl req -x509 -new -nodes -key ./mtls/ca.key -days 5000 -subj "/CN=go-delve" -out ./mtls/ca.crt

	# generate sever.key/server.scr/server.crt with ca signed
	openssl genrsa -out ./mtls/server.key 2048
	openssl req -new -key ./mtls/server.key -subj "/CN=go-delve" -out ./mtls/server.csr
	openssl x509 -req -in ./mtls/server.csr -CA ./mtls/ca.crt -CAkey ./mtls/ca.key -CAcreateserial -extfile ./mtls/extfile.cnf -out ./mtls/server.crt -days 5000

	# generate client.key/client.scr/client.crt with ca signed
	openssl genrsa -out ./mtls/client.key 2048
	openssl req -new -key ./mtls/client.key -subj "/CN=go-delve" -out ./mtls/client.csr
	openssl x509 -req -in ./mtls/client.csr -CA ./mtls/ca.crt -CAkey ./mtls/ca.key -CAcreateserial -out ./mtls/client.crt -days 5000


tlstoken.sh
    # some parts are borrowed by https://stackoverflow.com/questions/21488845/how-can-i-generate-a-self-signed-certificate-with-subjectaltname-using-openssl
    mkdir tlstoken

	# specity the ip that the server will listen or client will connect
	echo -e "[req]\nx509_extensions = v3_ca\ndistinguished_name = req_distinguished_name\n[req_distinguished_name]\n\n[v3_ca]\n\nsubjectAltName = @alt_names\nbasicConstraints = CA:FALSE\nkeyUsage = nonRepudiation, digitalSignature, keyEncipherment\n\n\n[alt_names]\nIP = 127.0.0.1" > ./tlstoken/config.cnf

    openssl genrsa -out ./tlstoken/server.key 2048
    openssl req -new -x509 -key ./tlstoken/server.key -days 3650 -subj "/CN=go-delve"  -config ./tlstoken/config.cnf -out ./tlstoken/server.crt

    (or use `go run $GOROOT/src/crypto/tls/generate_cert.go --host 127.0.0.1`, be careful to the name and path of files maybe diffrent with above)
*/

// CreateCaFile will create a pair about crt/key (signed with itself / signed with ca) and return paths of those.
// Some parts are borrowed from https://golang.org/src/crypto/tls/generate_cert.go.
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

func createNSetsFileOfCrtsAndKeys(N int, outputPath string, t *testing.T) {
	err := os.MkdirAll(outputPath, 0755)
	if err != nil && os.IsExist(err) {
		t.Fatal(err)
	}

	for i := 0; i < N; i++ {
		CreateCrtKey(filepath.Join(outputPath, fmt.Sprintf("ca%d", i)), "", true, "")
		CreateCrtKey(filepath.Join(outputPath, fmt.Sprintf("server%d", i)), "127.0.0.1", false, filepath.Join(outputPath, fmt.Sprintf("ca%d", i)))
		CreateCrtKey(filepath.Join(outputPath, fmt.Sprintf("client%d", i)), "", false, filepath.Join(outputPath, fmt.Sprintf("ca%d", i)))
	}
}

func getTlsPathArugments(path, name string, idx int) (string, string, string) {
	caCrt := filepath.Join(path, fmt.Sprintf("ca%d.crt", idx))
	crt := filepath.Join(path, fmt.Sprintf("%s%d.crt", name, idx))
	key := filepath.Join(path, fmt.Sprintf("%s%d.key", name, idx))
	return caCrt, crt, key
}

func withTestClient2OnMtls(name, genPath string, cKeyPairIdx, sKeyPairIdx int, t *testing.T, fn func(c service.Client, err error)) {
	if testBackend == "rr" {
		protest.MustHaveRecordingAllowed(t)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("couldn't start listener: %s\n", err)
	}
	defer listener.Close()
	var buildFlags protest.BuildFlags
	if buildMode == "pie" {
		buildFlags = protest.BuildModePIE
	}
	sCaCrt, sCrt, sKey := getTlsPathArugments(genPath, "server", sKeyPairIdx)
	listener, err = nativetls.WrapListenerWithMtls(listener, sCaCrt, sCrt, sKey)
	if err != nil {
		t.Fatal(err)
	}

	fixture := protest.BuildFixture(name, buildFlags)
	server := rpccommon.NewServer(&service.Config{
		Listener:       listener,
		ProcessArgs:    []string{fixture.Path},
		Backend:        testBackend,
		CheckGoVersion: true,
	})
	if err := server.Run(); err != nil {
		t.Fatal(err)
	}
	cCaCrt, cCrt, cKey := getTlsPathArugments(genPath, "client", cKeyPairIdx)
	var client *rpc2.RPCClient
	client, err = rpc2.NewMtlsClientWithCrtKeyPath(listener.Addr().String(), cCaCrt, cCrt, cKey)
	if err == nil {
		defer func() {
			client.Detach(true)
		}()
	}

	fn(client, err)
}

func TestRunOnMtls(t *testing.T) {
	genPath := "./test_mtls_path"

	// Generate 2 sets of caCrt, crt and key
	createNSetsFileOfCrtsAndKeys(2, genPath, t)
	defer os.RemoveAll(genPath)

	// If the crts of server and client are signed with different cas, it will expect a error
	withTestClient2OnMtls("testtls", genPath, 0, 1, t, func(c service.Client, err error) {
		assertError(err, t, "get client2 on mtls")
	})

	// If the crts of server and client are signed with the same ca, will run ok
	withTestClient2OnMtls("testtls", genPath, 0, 0, t, func(c service.Client, err error) {
		assertNoError(err, t, "get client2 on mtls")

		_, err = c.CreateBreakpoint(&api.Breakpoint{FunctionName: "main.main", Line: 1})
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}
		stateCh := <-c.Continue()
		if stateCh.Err != nil {
			t.Fatalf("Unexpected error: %v, state: %#v", stateCh.Err, stateCh)
		}
		<-c.Continue()
	})
}

func withTestClient2OnTlsToken(name, pathPrefix, cToken, sToken string, t *testing.T, fn func(c service.Client, err error)) {
	if testBackend == "rr" {
		protest.MustHaveRecordingAllowed(t)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("couldn't start listener: %s\n", err)
	}
	defer listener.Close()
	var buildFlags protest.BuildFlags
	if buildMode == "pie" {
		buildFlags = protest.BuildModePIE
	}
	sCrtPath := pathPrefix + ".crt"
	sKeyPath := pathPrefix + ".key"
	listener, err = nativetls.WrapListenerWithTls(listener, sCrtPath, sKeyPath)
	if err != nil {
		t.Fatal(err)
	}
	fixture := protest.BuildFixture(name, buildFlags)
	server := rpccommon.NewServer(&service.Config{
		Listener:       listener,
		ProcessArgs:    []string{fixture.Path},
		Backend:        testBackend,
		CheckGoVersion: true,
		Token:          sToken,
	})
	if err := server.Run(); err != nil {
		t.Fatal(err)
	}
	var client *rpc2.RPCClient
	client, err = rpc2.NewTlsClientWithToken(listener.Addr().String(), sCrtPath, cToken)
	if err == nil {
		defer func() {
			client.Detach(true)
		}()
	}
	fn(client, err)
}

func TestRunOnTlsToken(t *testing.T) {
	genPath := "./test_tls_path"
	err := os.MkdirAll(genPath, 0755)
	if err != nil && os.IsExist(err) {
		t.Fatal(err)
	}
	defer os.RemoveAll(genPath)
	// Generate the key pair of server
	CreateCrtKey(filepath.Join(genPath, "server"), "127.0.0.1", false, "")

	// If the crts of server/client are signed with the ca and tokens are different, it will expect a error.
	withTestClient2OnTlsToken("testtls", genPath+"/server", "chainhelen-client", "dlv-server", t, func(c service.Client, err error) {
		assertError(err, t, "get client2 on TlsToken")
	})

	// If the crts of server/client are signed with the ca and tokens are same, it will run ok.
	withTestClient2OnTlsToken("testtls", genPath+"/server", "dlv-server", "dlv-server", t, func(c service.Client, err error) {
		assertNoError(err, t, "get client2 on TlsToken")

		_, err = c.CreateBreakpoint(&api.Breakpoint{FunctionName: "main.main", Line: 1})
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}
		state := <-c.Continue()
		if state.Err != nil {
			t.Fatalf("Unexpected error: %v, state: %#v", state.Err, state)
		}
		if state.CurrentThread.Line != 6 {
			t.Fatalf("Program not stopped at correct spot expected %d was %d", 6, state.CurrentThread.Line)
		}
		<-c.Continue()
	})
}
