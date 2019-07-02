def command_find_array(arr, pred):
	"""Calls pred for each element of the array or slice 'arr' returns the index of the first element for which pred returns true.
	
	find_arr <arr>, <pred>
	
Example use:
	
	find_arr "s2", lambda x: x.A == 5
"""
	arrv = eval(None, arr).Variable
	for i in range(0, arrv.Len):
		v = arrv.Value[i]
		if pred(v):
			print("found", i)
			return

	print("not found")
