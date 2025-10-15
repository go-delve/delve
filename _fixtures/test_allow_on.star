# Test fixture for custom commands with on prefix
# All custom starlark commands automatically allow the on prefix

def command_test_on_allowed(args):
	"""Test command for on prefix functionality."""
	print("test_on_allowed called with:", args)
