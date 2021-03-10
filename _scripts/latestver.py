#!/usr/bin/python
import json
import urllib
import sys

ver = sys.argv[1]
d = json.loads(urllib.urlopen('https://golang.org/dl/?mode=json&include=all').read())
for x in d:
	if x['version'][:len(ver)] == ver:
		print x['version']
		exit(0)
