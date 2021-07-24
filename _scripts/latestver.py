#!/usr/bin/python
import json
import urllib
import sys
from distutils.version import LooseVersion

ver = sys.argv[1]
d = json.loads(urllib.urlopen('https://golang.org/dl/?mode=json&include=all').read())
ds = sorted(d, reverse=True, key=lambda it: LooseVersion(it['version'][2:]))
for x in ds:
	if x['version'][:len(ver)] == ver:
		print x['version']
		exit(0)
