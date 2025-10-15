# Test fixture for custom commands that trigger continue

def command_test_cmd_before_continue(args):
	"""Test command that runs before continue."""
	print("BEFORE_CONTINUE")

def command_test_continue_cmd(args):
	"""Test command that calls continue."""
	print("CONTINUE_CMD")

	dlv_command("continue")

def command_test_cmd_after_continue(args):
	"""Test command that runs after continue (should not run)."""
	print("AFTER_CONTINUE")
