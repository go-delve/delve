def main():
	for f in functions().Funcs:
		v = f.split('.')
		if len(v) != 2:
			continue
		if v[0] != "main":
			continue
		if v[1][0] >= 'a' and v[1][0] <= 'z':
			create_breakpoint({ "FunctionName": f, "Line": -1 }) # see documentation of RPCServer.CreateBreakpoint
