def Waiting():
	deadline = time.now() + time.parse_duration("10s")
	while time.now() < deadline:
		pass
