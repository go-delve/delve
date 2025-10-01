# Test fixture for @on_prefix directive

def command_test_on_allowed(args):
	"""Test command that allows on prefix.

	@on_prefix
	"""
	print("test_on_allowed called with:", args)

def command_test_on_not_allowed(args):
	"""Test command without on prefix."""
	print("test_on_not_allowed called with:", args)
