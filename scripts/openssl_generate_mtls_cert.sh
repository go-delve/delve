mkdir mtls

# specity the ip that the server will listen or client will connect
echo subjectAltName = IP:127.0.0.1 > ./mtls/extfile.cnf

# generate ca.key/ca.crt
openssl genrsa -out ./mtls/ca.key 2048
openssl req -x509 -new -nodes -key ./mtls/ca.key -days 5000 -subj "/CN=go-delve" -out ./mtls/ca.crt

# generate sever.key/server.scr/server.crt
openssl genrsa -out ./mtls/server.key 2048
openssl req -new -key ./mtls/server.key -subj "/CN=go-delve" -out ./mtls/server.csr
openssl x509 -req -in ./mtls/server.csr -CA ./mtls/ca.crt -CAkey ./mtls/ca.key -CAcreateserial -extfile ./mtls/extfile.cnf -out ./mtls/server.crt -days 5000

# generate client.key/client.scr/client.crt
openssl genrsa -out ./mtls/client.key 2048
openssl req -new -key ./mtls/client.key -subj "/CN=go-delve" -out ./mtls/client.csr
openssl x509 -req -in ./mtls/client.csr -CA ./mtls/ca.crt -CAkey ./mtls/ca.key -CAcreateserial -out ./mtls/client.crt -days 5000
