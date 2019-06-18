def command_switch_to_main_goroutine(args):
	for g in goroutines().Goroutines:
		if g.CurrentLoc.Function != None and g.CurrentLoc.Function.Name_.startswith("main."):
			print("switching to:", g.ID)
			raw_command("switchGoroutine", GoroutineID=g.ID)
			break
