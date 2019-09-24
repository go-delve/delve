v = eval(None, "m1").Variable
n = 0
d = {}
for k in v.Value:
        if not d.get(k):
		n = n + 1
		d[k] = True
print("values=", n, sep="")
