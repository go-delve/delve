#!/usr/bin/python
import json
import urllib
import sys
import re

def splitver(x):
	v = re.split(r'([^\d]+)', x)
	v[0] = int(v[0])
	if len(v) > 2:
		v[2] = int(v[2])
	if len(v) > 4:
		v[4] = int(v[4])
	# make rc/beta versions sort before normal versions
	if len(v) > 3 and v[3] == '.':
		v[3] = '~' 
	elif len(v) == 3:
		v.append('~')
	return v

ver = sys.argv[1]
d = json.loads(urllib.urlopen('https://go.dev/dl/?mode=json&include=all').read())
ds = sorted(d, reverse=True, key=lambda it: splitver(it['version'][2:]))
for x in ds:
	if x['version'][:len(ver)] == ver:
		print x['version']
		exit(0)
