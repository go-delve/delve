def command_goroutine_start_line(args):
	"prints the line of source code that started each currently running goroutine"
	gs = goroutines().Goroutines
	for g in gs:
		if g.StartLoc.File != "":
			line = read_file(g.StartLoc.File).splitlines()[g.StartLoc.Line-1].strip()
		else:
			line = "<nothing>"
		print(g.ID, "\t", g.StartLoc.File + ":" + str(g.StartLoc.Line), "\t", line)

def main():
	dlv_command("config alias goroutine_start_line gsl")
