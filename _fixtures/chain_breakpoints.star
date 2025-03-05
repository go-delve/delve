def command_chain(args):
	v = args.split(" ")
	
	bp = get_breakpoint(int(v[0]), "").Breakpoint
	bp.HitCond = "== 1"
	amend_breakpoint(bp)
	
	for i in range(1, len(v)):
		bp = get_breakpoint(int(v[i]), "").Breakpoint
		if i != len(v)-1:
			bp.HitCond = "== 1"
		bp.Cond = "delve.bphitcount[" + v[i-1] + "] > 0"
		amend_breakpoint(bp)
