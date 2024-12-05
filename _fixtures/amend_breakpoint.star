bp = get_breakpoint(0, "afuncbreak").Breakpoint
bp.Stacktrace = 2
bp.HitCond = "== 2"
amend_breakpoint(bp)
bp2 = get_breakpoint(0, "afuncbreak").Breakpoint
print(bp)
