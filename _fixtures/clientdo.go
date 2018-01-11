package main

import (
	"net/http"
	"net/url"
)

func main() {
	http.DefaultClient.Do(&http.Request{URL: &url.URL{}})
}
