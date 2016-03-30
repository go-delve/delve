# Installation on OSX

Please use the following steps to build and install Delve on OSX.

## 0) Prerequisites

Ensure you have a proper compilation toolchain.

This should be as simple as:

`xcode-select --install`

## 1) Create a self-signed certificate

You must create a self signed certificate and sign the binary with it:

* Open application “Keychain Access” (/Applications/Utilities/Keychain Access.app)
* Open menu /Keychain Access/Certificate Assistant/Create a Certificate...
* Choose a name (dlv-cert in the example), set “Identity Type” to “Self Signed Root”, set “Certificate Type” to “Code Signing” and select the “Let me override defaults”. Click “Continue”. You might want to extend the predefined 365 days period to 3650 days.
* Click several times on “Continue” until you get to the “Specify a Location For The Certificate” screen, then set “Keychain to System”.
* If you can't store the certificate in the “System” keychain, create it in the “login” keychain, then export it. You can then import it into the “System” keychain.
* In keychains select “System”, and you should find your new certificate. Use the context menu for the *certificate* (not the public or private keys), select “Get Info”, open the “Trust” item, and set “Code Signing” to “Always Trust”.
* [At least on Yosemite:] In keychains select category Keys -> dlv-cert -> right click -> GetInfo -> Access Control -> select "Allow all applications to access this item" -> Save Changes.
* You must quit “Keychain Access” application in order to use the certificate and restart “taskgated” service by killing the current running “taskgated” process. Alternatively you can restart your computer.
* Clone this project: `git clone git@github.com:derekparker/delve.git && cd delve`
* Run the following: `GO15VENDOREXPERIMENT=1 CERT=dlv-cert make install`, which will install the binary and codesign it.
* for more info see this installation video http://www.youtube.com/watch?v=4ndjybtBg74


## 2) Install the binary

Note: If you are using Go 1.5 you must set `GO15VENDOREXPERIMENT=1` before continuing. The `GO15VENDOREXPERIMENT` env var simply opts into the [Go 1.5 Vendor Experiment](https://docs.google.com/document/d/1Bz5-UB7g2uPBdOx-rw5t9MxJwkfpx90cqG9AFL0JAYo/).

All `make` commands assume a CERT environment variables that contains the name of the cert you created above.

Now, simply run:

```
$ CERT=mycert make install
```

The Makefile also assumes that `GOPATH` is single-valued, not colon-separated.

The makefile is only necessary to help facilitate the process of building and codesigning.

## Notes

### Eliminating codesign authorization prompt during builds

If you're prompted for authorization when running `make` using your self-signed certificate, try the following:

* Open application “Keychain Access” (/Applications/Utilities/Keychain Access.app)
* Double-click on the private key corresponding to your self-signed certificate (dlv-cert in the example)
* Choose the "Access Control" tab
* Click the [+] under "Always allow access by these applications", and choose `/usr/bin/codesign` from the Finder dialog
* Click the "Save changes" button

#### Eliminating "Developer tools access" prompt running delve

If you are prompted with this when running `dlv`:

    "Developer tools access needs to take control of another process for debugging to continue. Type your password to allow this"

Try running `DevToolsSecurity -enable` to eliminate the prompt. See `man DevToolsSecurity` for more information.
