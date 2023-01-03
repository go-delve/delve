package main

import (
	"bufio"
	"bytes"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"regexp"
	"strings"
)

func must(err error, fmtstr string, args ...interface{}) {
	if err != nil {
		log.Fatalf(fmtstr, args...)
	}
}

func usage() {
	os.Stderr.WriteString("gen-faq-to input-path output-path")
	os.Exit(1)
}

var anchor = regexp.MustCompile(`### <a name="(.*?)"></a> (.*)`)

type tocentry struct {
	anchor, title string
}

const (
	startOfToc = "<!-- BEGIN TOC -->"
	endOfToc   = "<!-- END TOC -->"
)

func spliceDocs(docpath string, docs []byte, outpath string) {
	docbuf, err := ioutil.ReadFile(docpath)
	if err != nil {
		log.Fatalf("could not read doc file: %v", err)
	}

	v := strings.Split(string(docbuf), startOfToc)
	if len(v) != 2 {
		log.Fatal("could not find start of mapping table")
	}
	header := v[0]
	v = strings.Split(v[1], endOfToc)
	if len(v) != 2 {
		log.Fatal("could not find end of mapping table")
	}
	footer := v[1]

	outbuf := bytes.NewBuffer(make([]byte, 0, len(header)+len(docs)+len(footer)+len(startOfToc)+len(endOfToc)+1))
	outbuf.Write([]byte(header))
	outbuf.Write([]byte(startOfToc))
	outbuf.WriteByte('\n')
	outbuf.Write(docs)
	outbuf.Write([]byte(endOfToc))
	outbuf.Write([]byte(footer))

	if outpath != "-" {
		err = ioutil.WriteFile(outpath, outbuf.Bytes(), 0664)
		must(err, "could not write documentation file: %v", err)
	} else {
		os.Stdout.Write(outbuf.Bytes())
	}
}

func readtoc(docpath string) []tocentry {
	infh, err := os.Open(docpath)
	must(err, "could not open %s: %v", docpath, err)
	defer infh.Close()
	scan := bufio.NewScanner(infh)
	tocentries := []tocentry{}
	seenAnchors := map[string]bool{}
	for scan.Scan() {
		line := scan.Text()
		if !strings.HasPrefix(line, "### ") {
			continue
		}
		m := anchor.FindStringSubmatch(line)
		if len(m) != 3 {
			log.Fatalf("entry %q does not have anchor", line)
		}
		if seenAnchors[m[1]] {
			log.Fatalf("duplicate anchor %q", m[1])
		}
		anchor, title := m[1], m[2]
		seenAnchors[anchor] = true
		tocentries = append(tocentries, tocentry{anchor, title})

	}
	must(scan.Err(), "could not read %s: %v", scan.Err())
	return tocentries
}

func writetoc(tocentries []tocentry) []byte {
	b := new(bytes.Buffer)
	for _, tocentry := range tocentries {
		fmt.Fprintf(b, "* [%s](#%s)\n", tocentry.title, tocentry.anchor)
	}
	return b.Bytes()
}

func main() {
	if len(os.Args) != 3 {
		usage()
	}

	docpath, outpath := os.Args[1], os.Args[2]

	tocentries := readtoc(docpath)

	spliceDocs(docpath, []byte(writetoc(tocentries)), outpath)
}
