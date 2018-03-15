package main

import (
	"fmt"
	"log"
	"regexp"
	"strings"

	//	"golang.org/x/crypto/ssh/terminal"
)

// Blacklist type is a map of Nodes with string keys
type Blacklist map[string]*Node

// Source type is a map of Srcs with string keys
type Source map[string]*Src

// Node configuration record
type Node struct {
	Disable          bool
	IP               string
	Include, Exclude []string
	Source
}

// Src record, struct for Source map
type Src struct {
	Disable bool
	Desc    string
	Prfx    string
	URL     string
}

// String returns pretty print for the Blacklist struct
func (b Blacklist) String() (result string) {
	//	cols, _, _ := terminal.GetSize(int(os.Stdout.Fd()))
	cols := 20
	for pkey := range b {
		result += fmt.Sprintf("Node: %v\n\tDisabled: %v\n", pkey, b[pkey].Disable)
		result += fmt.Sprintf("\tRedirect IP: %v\n\tExclude(s):\n", b[pkey].IP)
		for _, exclude := range b[pkey].Exclude {
			result += fmt.Sprintf("\t\t%v\n", exclude)
		}
		result += fmt.Sprintf("\tInclude(s):\n")
		for _, include := range b[pkey].Include {
			result += fmt.Sprintf("\t\t%v\n", include)
		}
		for skey, src := range b[pkey].Source {
			result += fmt.Sprintf("\tSource: %v\n\t\tDisabled: %v\n", skey, src.Disable)
			result += fmt.Sprintf("\t\tDescription: %v\n", b[pkey].Source[skey].Desc)
			result += fmt.Sprintf("\t\tPrefix: %v\n\t\tURL: %v\n", b[pkey].Source[skey].Prfx, b[pkey].Source[skey].URL)
		}
		result += fmt.Sprintln(strings.Repeat("-", cols/2))
	}
	return result
}

// ToBool converts a string ("true" or "false") to its boolean equivalent
func ToBool(s string) (b bool) {
	if len(s) == 0 {
		log.Fatal("ERROR: variable empty, cannot convert to boolean")
	}
	switch s {
	case "false":
		b = false
	case "true":
		b = true
	}
	return b
}

// Get extracts nodes from a EdgeOS/VyOS configuration structure
func Get(cfg string) {
	type re struct {
		brkt, cmnt, desc, dsbl, leaf, misc, mlti, mpty, name, node *regexp.Regexp
	}

	rx := &re{}
	rx.brkt = regexp.MustCompile(`[}]`)
	rx.cmnt = regexp.MustCompile(`^([\/*]+).*([*\/]+)$`)
	rx.desc = regexp.MustCompile(`^(?:description)+\s"?([^"]+)?"?$`)
	rx.dsbl = regexp.MustCompile(`^(disabled)+\s([\S]+)$`)
	rx.leaf = regexp.MustCompile(`^(source)+\s([\S]+)\s[{]{1}$`)
	rx.misc = regexp.MustCompile(`^([\w-]+)$`)
	rx.mlti = regexp.MustCompile(`^((?:include|exclude)+)\s([\S]+)$`)
	rx.mpty = regexp.MustCompile(`^$`)
	rx.name = regexp.MustCompile(`^([\w-]+)\s([\S]+)$`)
	rx.node = regexp.MustCompile(`^([\w-]+)\s[{]{1}$`)

	cfgtree := make(map[string]*Node)

	var tnode string
	var leaf string

	for _, line := range strings.Split(cfg, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case rx.mlti.MatchString(line):
			{
				IncExc := rx.mlti.FindStringSubmatch(line)
				switch IncExc[1] {
				case "exclude":
					cfgtree[tnode].Exclude = append(cfgtree[tnode].Exclude, IncExc[2])
				case "include":
					cfgtree[tnode].Include = append(cfgtree[tnode].Include, IncExc[2])
				}
			}
		case rx.node.MatchString(line):
			{
				node := rx.node.FindStringSubmatch(line)
				tnode = node[1]
				cfgtree[tnode] = &Node{}
				cfgtree[tnode].Source = make(map[string]*Src)
			}
		case rx.leaf.MatchString(line):
			src := rx.leaf.FindStringSubmatch(line)
			leaf = src[2]

			if src[1] == "source" {
				cfgtree[tnode].Source[leaf] = &Src{}
			}
		case rx.dsbl.MatchString(line):
			{
				disabled := rx.dsbl.FindStringSubmatch(line)
				cfgtree[tnode].Disable = ToBool(disabled[1])
			}
		case rx.name.MatchString(line):
			{
				name := rx.name.FindStringSubmatch(line)
				switch name[1] {
				case "prefix":
					cfgtree[tnode].Source[leaf].Prfx = name[2]
				case "url":
					cfgtree[tnode].Source[leaf].URL = name[2]
				case "description":
					cfgtree[tnode].Source[leaf].Desc = name[2]
				case "dns-redirect-ip":
					cfgtree[tnode].IP = name[2]
				}
			}
		case rx.desc.MatchString(line) || rx.cmnt.MatchString(line) || rx.misc.MatchString(line):
			break
		}
		// fmt.Printf("%s\n", line)
	}
	fmt.Println(cfgtree)
}

func main() {
	cfgtree := make(Blacklist)
	for _, k := range []string{"root", "hosts", "domains"} {
		cfgtree[k] = &Node{}
		cfgtree[k].Source = make(Source)
	}
	cfgtree["hosts"].Exclude = append(cfgtree["hosts"].Exclude, "rackcdn.com", "schema.org")
	cfgtree["hosts"].Include = append(cfgtree["hosts"].Include, "msdn.com", "badgits.org")
	cfgtree["hosts"].IP = "192.168.168.1"
	cfgtree["hosts"].Source["hpHosts"] = &Src{URL: "http://www.bonzon.com", Prfx: "127.0.0.0"}
	fmt.Println(cfgtree)
	fmt.Println(cfgtree["hosts"])
	Get(testdata)
}

var testdata = `blacklist {
			disabled false
			dns-redirect-ip 0.0.0.0
			domains {
					include adsrvr.org
					include adtechus.net
					include advertising.com
					include centade.com
					include doubleclick.net
					include free-counter.co.uk
					include intellitxt.com
					include kiosked.com
					source malc0de {
							description "List of zones serving malicious executables observed by malc0de.com/database/"
							prefix "zone "
							url http://malc0de.com/bl/ZONES
					}
			}
			exclude 122.2o7.net
			exclude 1e100.net
			exclude adobedtm.com
			exclude akamai.net
			exclude amazon.com
			exclude amazonaws.com
			exclude apple.com
			exclude ask.com
			exclude avast.com
			exclude bitdefender.com
			exclude cdn.visiblemeasures.com
			exclude cloudfront.net
			exclude coremetrics.com
			exclude edgesuite.net
			exclude freedns.afraid.org
			exclude github.com
			exclude githubusercontent.com
			exclude google.com
			exclude googleadservices.com
			exclude googleapis.com
			exclude googleusercontent.com
			exclude gstatic.com
			exclude gvt1.com
			exclude gvt1.net
			exclude hb.disney.go.com
			exclude hp.com
			exclude hulu.com
			exclude images-amazon.com
			exclude msdn.com
			exclude paypal.com
			exclude rackcdn.com
			exclude schema.org
			exclude skype.com
			exclude smacargo.com
			exclude sourceforge.net
			exclude ssl-on9.com
			exclude ssl-on9.net
			exclude static.chartbeat.com
			exclude storage.googleapis.com
			exclude windows.net
			exclude yimg.com
			exclude ytimg.com
			hosts {
					include beap.gemini.yahoo.com
					source adaway {
							description "Blocking mobile ad providers and some analytics providers"
							prefix "127.0.0.1 "
							url http://adaway.org/hosts.txt
					}
					source malwaredomainlist {
							description "127.0.0.1 based host and domain list"
							prefix "127.0.0.1 "
							url http://www.malwaredomainlist.com/hostslist/hosts.txt
					}
					source openphish {
							description "OpenPhish automatic phishing detection"
							prefix http
							url https://openphish.com/feed.txt
					}
					source someonewhocares {
							description "Zero based host and domain list"
							prefix 0.0.0.0
							url http://someonewhocares.org/hosts/zero/
					}
					source volkerschatz {
							description "Ad server blacklists"
							prefix http
							url http://www.volkerschatz.com/net/adpaths
					}
					source winhelp2002 {
							description "Zero based host and domain list"
							prefix "0.0.0.0 "
							url http://winhelp2002.mvps.org/hosts.txt
					}
					source yoyo {
							description "Fully Qualified Domain Names only - no prefix to strip"
							prefix ""
							url http://pgl.yoyo.org/as/serverlist.php?hostformat=nohtml&showintro=1&mimetype=plaintext
					}
			}
	}`
