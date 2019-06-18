def command_linked_list(args):
	"""Prints the contents of a linked list.
	
	linked_list <var_name> <next_field_name> <max_depth>

Prints up to max_depth elements of the linked list variable 'var_name' using 'next_field_name' as the name of the link field.
"""
	var_name, next_field_name, max_depth = args.split(" ")
	max_depth = int(max_depth)
	next_name = var_name
	v = eval(None, var_name).Variable.Value
	for i in range(0, max_depth):
		print(str(i)+":",v)
		if v[0] == None:
			break
		v = v[next_field_name]
