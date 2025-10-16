def command_test_bp1_before_continue(args):
	"""Test command on BP1 before continue."""
	print("BP1_BEFORE_CONTINUE")

def command_test_bp1_continue_cmd(args):
	"""Test command on BP1 that calls continue to hit BP2."""
	print("BP1_CONTINUE_CMD")
	dlv_command("continue")

def command_test_bp1_after_continue(args):
	"""Test command on BP1 after continue (should not run)."""
	print("BP1_AFTER_CONTINUE")

def command_test_bp2_cmd(args):
	"""Test command on BP2 (should not run because BP1 invalidated state)."""
	print("BP2_CMD_EXECUTED")

def command_test_bp3_cmd(args):
	"""Test command on BP3."""
	print("BP3_CMD_EXECUTED")
