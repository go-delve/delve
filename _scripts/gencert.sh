#!/bin/bash

# Check if the certificate is already present in keychains
security find-certificate -Z -p -c "dlv-cert" > /dev/null 2>&1
EXIT_CODE=$?
if [ $EXIT_CODE -eq 0 ]; then
  # Certificate has already been generated and installed
  exit 0
fi

CERT="dlv-cert"

# Create the certificate template
cat <<EOF >$CERT.tmpl
[ req ]
default_bits       = 2048        # RSA key size
encrypt_key        = no          # Protect private key
default_md         = sha512      # MD to use
prompt             = no          # Prompt for DN
distinguished_name = codesign_dn # DN template
[ codesign_dn ]
commonName         = "dlv-cert"
[ codesign_reqext ]
keyUsage           = critical,digitalSignature
extendedKeyUsage   = critical,codeSigning
EOF

# Generate a new certificate
openssl req -new -newkey rsa:2048 -x509 -days 3650 -nodes -config $CERT.tmpl -extensions codesign_reqext -batch -out $CERT.cer -keyout $CERT.key > /dev/null 2>&1
EXIT_CODE=$?
if [ $EXIT_CODE -ne 0 ]; then
  # Something went wrong when generating the certificate
  exit 1
fi

# Convert certificate to pkcs2 format
openssl pkcs12 -export -legacy -inkey dlv-cert.key -in dlv-cert.cer -name "My Code Signing" -out dlv-cert.p12 -passout pass:dlv-cert
EXIT_CODE=$?
if [ $EXIT_CODE -ne 0 ]; then
  # Something went wrong when installing the certificate
  exit 1
fi

# Create new CI/CD keychain
security create-keychain -p "cicd" cicd
EXIT_CODE=$?
if [ $EXIT_CODE -ne 0 ]; then
  # Something went wrong when installing the certificate
  exit 1
fi

# Unlock the keychain
security unlock-keychain -p "cicd" cicd
EXIT_CODE=$?
if [ $EXIT_CODE -ne 0 ]; then
  # Something went wrong when installing the certificate
  exit 1
fi

# Import the certificate into the CI/CD keychain
security import dlv-cert.p12 -P "dlv-cert" -k cicd -T /usr/bin/codesign
EXIT_CODE=$?
if [ $EXIT_CODE -ne 0 ]; then
  # Something went wrong when installing the certificate
  exit 1
fi

# Remove generated files
rm $CERT.tmpl $CERT.cer $CERT.key $CERT.p12 > /dev/null 2>&1

# Exit indicating the certificate is now generated and installed
exit 0
